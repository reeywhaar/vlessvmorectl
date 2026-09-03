package backup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// agent counts what it was sent, and answers whatever status is set on it.
type agent struct {
	*httptest.Server
	calls  atomic.Int64
	status atomic.Int64
	names  chan string
}

func newAgent(t *testing.T) *agent {
	t.Helper()
	a := &agent{names: make(chan string, 16)}
	a.status.Store(http.StatusOK)
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.calls.Add(1)
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			select {
			case a.names <- r.FormValue("name"):
			default:
			}
		}
		if code := int(a.status.Load()); code != http.StatusOK {
			http.Error(w, "refused", code)
		}
	}))
	t.Cleanup(a.Close)
	return a
}

func newPusher(t *testing.T, dir string, a *agent) *Pusher {
	t.Helper()
	return &Pusher{Dir: dir, URL: a.URL, Log: discard(), Client: a.Client()}
}

func TestPusherSendsTheFirstCopyThenGoesQuiet(t *testing.T) {
	a := newAgent(t)
	p := newPusher(t, seed(t, defaultFiles()), a)

	for range 3 {
		if err := p.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	if got := a.calls.Load(); got != 1 {
		t.Errorf("sent %d archives over three unchanged passes, want 1", got)
	}
}

func TestPusherSendsAgainAfterAChange(t *testing.T) {
	a := newAgent(t)
	dir := seed(t, defaultFiles())
	p := newPusher(t, dir, a)

	if err := p.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subscribers.json"), []byte(`{"version":1,"changed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Once(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := a.calls.Load(); got != 2 {
		t.Errorf("sent %d archives, want 2", got)
	}
}

// Signing in writes sessions.json and nothing else, and that is not worth an archive.
func TestPusherIgnoresSessionChurn(t *testing.T) {
	a := newAgent(t)
	dir := seed(t, defaultFiles())
	p := newPusher(t, dir, a)

	if err := p.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`{"version":1,"sessions":[{"hash":"a"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Once(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := a.calls.Load(); got != 1 {
		t.Errorf("sent %d archives, want 1: a login should not trigger a backup", got)
	}
}

// A rejected upload must not be remembered as accepted, or the next change would be the only
// thing that ever made it try again.
func TestPusherRetriesAfterARejection(t *testing.T) {
	a := newAgent(t)
	a.status.Store(http.StatusInternalServerError)
	p := newPusher(t, seed(t, defaultFiles()), a)

	if err := p.Once(context.Background()); err == nil {
		t.Fatal("Once against a failing agent succeeded, want an error")
	}

	a.status.Store(http.StatusOK)
	if err := p.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := a.calls.Load(); got != 2 {
		t.Errorf("the agent saw %d attempts, want 2", got)
	}
}

func TestPusherNamesTheArchive(t *testing.T) {
	a := newAgent(t)
	p := newPusher(t, seed(t, defaultFiles()), a)

	if err := p.Once(context.Background()); err != nil {
		t.Fatal(err)
	}

	name := <-a.names
	if _, err := time.Parse("20060102_150405", name[len("vlessvmorectl-"):len(name)-len(".tgz")]); err != nil {
		t.Errorf("name = %q, want vlessvmorectl-<timestamp>.tgz", name)
	}
}

// An unset address is how backups are switched off, and it must not start a loop that logs
// a failure every five minutes.
func TestRunWithoutAnAddressDoesNothing(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Pusher{Dir: t.TempDir(), Log: discard()}).Run(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run with no URL did not return")
	}
}

func TestRunBacksUpImmediatelyAndStopsWithTheContext(t *testing.T) {
	a := newAgent(t)
	p := newPusher(t, seed(t, defaultFiles()), a)
	p.Every = time.Hour // so only the immediate first pass can run

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx)
	}()

	select {
	case <-a.names:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Run did not back up at startup")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}
}
