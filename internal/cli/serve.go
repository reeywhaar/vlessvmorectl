package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"vlessvmorectl/internal/api"
	"vlessvmorectl/internal/config"
	"vlessvmorectl/internal/session"
	"vlessvmorectl/internal/store"
	"vlessvmorectl/web"
)

// shutdownGrace is how long in-flight requests get to finish on SIGTERM. A proxied
// call to a slow node is the longest thing in flight, and config.HTTPTimeout already
// bounds that.
const shutdownGrace = 20 * time.Second

func newServeCmd() *cobra.Command {
	var dataDir string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the control panel",
		Long: `Run the control panel.

Listens on ` + config.ListenAddr + `, which is not configurable — remap it with a
docker port binding. Put a TLS-terminating reverse proxy in front of it: this service
hands out session cookies and proxies credentialed calls, and neither belongs on a
plaintext connection.

Configuration is entirely environment:

    VLESSVMORE_SERVERS   url|token pairs, comma-separated
    VLESSVMORE_LOG_LEVEL debug, info (default), warn or error`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd, dataDir)
		},
	}
	cmd.Flags().StringVar(&dataDir, "data-dir", dataDirDefault(), "path to the data directory")
	return cmd
}

func runServe(cmd *cobra.Command, dataDir string) error {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: cfg.LogLevel}))

	// OpenForDaemon rather than Open: this process is the only writer of
	// subscribers.json, and the read-only handle every other call site gets is what
	// stops a future CLI command quietly corrupting it. See the store's package doc.
	st, err := store.OpenForDaemon(dataDir)
	if err != nil {
		return err
	}

	// The one place admins.json is brought up to the current format. Not fatal: the ids
	// are already right in memory, so a failure here only means the file stays on the old
	// version and the next write stamps it.
	if n, err := st.Admins.Migrate(); err != nil {
		log.Warn("could not rewrite admins.json in the current format; continuing",
			"path", st.Admins.Path(), "error", err)
	} else if n > 0 {
		log.Info("gave existing administrators a permanent id", "administrators", n)
	}

	spa, err := api.NewSPA(web.Dist(), log)
	if err != nil {
		return fmt.Errorf("loading the frontend: %w", err)
	}

	// Restored from the data directory, so `docker compose restart` does not sign every
	// operator out. Only hashes are stored; see the session package.
	sessions := session.New(store.OpenSessionFile(st.Dir()), log)
	srv := api.New(cfg, st, sessions, spa, log, time.Now)

	// Installed before anything starts, so a signal arriving during startup is not
	// missed.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Generate the constant-time dummy hash now rather than inside the first failed
	// login, where it would make that one request measurably slower than the rest and
	// briefly reopen the username oracle it exists to close.
	go store.Warm()

	done := make(chan struct{})
	defer close(done)
	go sessions.RunJanitor(done, time.Now)

	warnAboutConfiguration(log, cfg, st)

	httpSrv := &http.Server{
		Addr:    config.ListenAddr,
		Handler: srv.Handler(),
		// Bounds a client that opens a connection and dribbles headers. The body has
		// no deadline because a proxied request may legitimately take a while.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	log.Info("vlessvmorectl started",
		"version", versionString(),
		"addr", config.ListenAddr,
		"data_dir", st.Dir(),
		"admins", st.Admins.Count(),
		"subscribers", st.Subscribers.Count(),
		// Counts only. Never the URLs at info level, and never the tokens at any level.
		"servers", len(cfg.Servers))

	errc := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	}
}

// warnAboutConfiguration says out loud the things that otherwise fail silently hours
// later. Each of these is a state the service can run in perfectly happily while being
// useless to the person who deployed it.
func warnAboutConfiguration(log *slog.Logger, cfg *config.Config, st *store.Store) {
	if st.Admins.Count() == 0 {
		// Deliberately not auto-creating one, and deliberately not printing a
		// generated password: it would land in `docker logs` and from there in
		// whatever ships logs off this box. Started-but-unenterable is the safe state.
		log.Warn("no administrators exist, so nobody can log in. Create one with: " +
			"docker exec vlessvmorectl vlessvmorectl users add alice")
	}

	if len(cfg.Servers) == 0 {
		log.Warn(`no vlessvmore servers configured; the panel will show an empty list. ` +
			`Set VLESSVMORE_SERVERS="https://vpn.example.com|<token>,…"`)
		return
	}

	var plaintext []string
	for _, s := range cfg.Servers {
		if strings.HasPrefix(s.URL, "http://") {
			plaintext = append(plaintext, s.URL)
		}
	}
	if len(plaintext) > 0 {
		// Not an error. Reaching a node over http:// on a private Docker network is
		// the recommended topology precisely because proxying means the node need not
		// publish its API at all. But it should be a visible choice, since the same
		// spelling over the public internet would put a full-control bearer token on
		// the wire in the clear.
		log.Info("some nodes are configured over plain http; make sure that traffic stays on a private network",
			"servers", strings.Join(plaintext, " "))
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
