package backup

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Delay is how long a change waits, and the throttle: nothing here reacts to a write, so an
// operator making six edits in a minute gets one archive holding all six.
const Delay = 5 * time.Minute

// pushTimeout bounds one pass, end to end. The archive is kilobytes of JSON; the upload is
// the part that leaves this machine.
const pushTimeout = 2 * time.Minute

// Pusher copies the data directory to a backup agent whenever it has changed.
type Pusher struct {
	Dir string
	Log *slog.Logger

	// URL is where an agent takes an archive. Empty means nothing is sent at all.
	URL string

	// Every defaults to [Delay]. A field only so a test need not wait five minutes.
	Every time.Duration

	Client *http.Client

	// last is the digest the agent last accepted, kept in memory: a file recording it would
	// live in the directory being hashed, and every check would find a change it had caused.
	last []byte
}

// Run works until ctx is done, starting with an immediate pass — a process that has just
// started is the one most likely to have been restarted onto a new volume.
func (p *Pusher) Run(ctx context.Context) {
	if p.URL == "" {
		return
	}
	if p.Every <= 0 {
		p.Every = Delay
	}
	p.Log.Info("backing up on change", "to", p.URL, "checked_every", p.Every)

	for {
		// Logged and left for the next pass rather than retried here: what fails is either
		// transient or needs a person, and a cancelled push on shutdown is neither.
		if err := p.Once(ctx); err != nil && ctx.Err() == nil {
			p.Log.Error("backup failed", "error", err)
		}

		timer := time.NewTimer(p.Every)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Pusher) client() *http.Client {
	if p.Client == nil {
		p.Client = &http.Client{Timeout: pushTimeout}
	}
	return p.Client
}

// Once builds a copy and sends it if it differs from the last one accepted. Exported so a
// test can drive a pass without waiting on the clock.
func (p *Pusher) Once(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()

	now := time.Now()
	// Built before it is known to be needed. These files are a few kilobytes of JSON, so
	// building the archive is the same read any cheaper check would have done.
	archive, err := Build(p.Dir, now, p.Log)
	if err != nil {
		return err
	}
	if bytes.Equal(p.last, archive.Digest) {
		p.Log.Debug("nothing to back up; the data directory is as it was")
		return nil
	}
	first := p.last == nil

	name := Filename(now)
	if err := Push(ctx, p.client(), p.URL, name, archive.Body); err != nil {
		return err
	}
	// Only now. Recorded before the agent accepted it, a rejected upload would leave this
	// process believing a copy exists that does not.
	p.last = archive.Digest

	p.Log.Info("backed up", "name", name, "bytes", len(archive.Body), "first", first)
	return nil
}
