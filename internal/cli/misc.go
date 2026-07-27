package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"

	"vlessvmorectl/internal/config"
)

// Version is overridable at build time with
// -ldflags "-X vlessvmorectl/internal/cli.Version=v1.2.3".
var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Print version information",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "vlessvmorectl %s\n", versionString())
			return nil
		},
	}
}

func versionString() string {
	if Version != "dev" {
		return Version
	}
	// A `go install`ed binary knows its module version even without ldflags.
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return Version
}

// newHealthcheckCmd backs the image's HEALTHCHECK.
//
// A loopback HTTP GET rather than a TCP probe: it exercises the listener, the router
// and the handler chain, so a process that is running but wedged fails the check. The
// sibling dials its unix socket for the same reason; this service has no socket, so
// loopback is the equivalent.
//
// Hidden, because it is for the container runtime rather than for a person.
func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "healthcheck",
		Short:        "Probe the local listener (used by the container HEALTHCHECK)",
		Hidden:       true,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()

			url := "http://127.0.0.1" + config.ListenAddr + "/healthz"
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return fmt.Errorf("healthz returned %s", res.Status)
			}
			var body struct {
				OK bool `json:"ok"`
			}
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil || !body.OK {
				return fmt.Errorf("healthz did not report ok")
			}
			return nil
		},
	}
}
