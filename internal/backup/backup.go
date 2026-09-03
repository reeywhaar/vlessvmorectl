// Package backup copies the data directory and posts it to a backup agent.
//
// The panel pushes rather than being pulled from: only this process knows whether anything
// has changed since the last copy, so nothing is sent while nothing has.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"

	"vlessvmorectl/internal/store"
)

// Filename is the archive's name, timestamped so an agent can keep a series.
func Filename(t time.Time) string {
	return "vlessvmorectl-" + t.UTC().Format("20060102_150405") + ".tgz"
}

// archiveDir is the archive's single top-level directory, so extracting one over a
// deployment directory puts each file back where the compose file mounts it from.
const archiveDir = "data"

// tempRe matches the files store.writeJSONAtomic leaves between creating a new version and
// renaming it into place. They are half-written by definition.
var tempRe = regexp.MustCompile(`\.tmp\d+$`)

// Archive is one copy of the data directory, and the digest that says which copy it is.
type Archive struct {
	Body []byte

	// Digest covers the archived files rather than the tarball, whose gzip header carries a
	// timestamp, and skips sessions.json, which churns on its own and is rebuilt by signing in.
	Digest []byte
}

// Build reads dir into a gzipped tar, in memory: a tar header needs its entry's size up
// front, and these files are a few kilobytes of JSON.
func Build(dir string, now time.Time, log *slog.Logger) (*Archive, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	sum := sha256.New()
	files := 0

	for _, item := range items {
		name := item.Name()
		full := filepath.Join(dir, name)
		if item.IsDir() {
			// Said out loud rather than skipped quietly: the store keeps a flat directory,
			// so this one is the operator's.
			log.Warn("skipping a directory in the data directory; only files are archived", "path", full)
			continue
		}
		if !item.Type().IsRegular() || tempRe.MatchString(name) {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", full, err)
		}

		// Length-prefixed, and in the name order os.ReadDir guarantees, so no rename or
		// reordering can produce another set's byte stream.
		if name != store.SessionsFile {
			fmt.Fprintf(sum, "%s\x00%d\x00", name, len(data))
			sum.Write(data)
		}

		// 0600 throughout, and no directory entries: the archive holds every share token in
		// the clear, and extracting must not re-chmod a data directory that already exists.
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     path.Join(archiveDir, name),
			Mode:     0o600,
			Size:     int64(len(data)),
			ModTime:  now.UTC().Truncate(time.Second),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write %s header: %w", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
		files++
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("close gzip: %w", err)
	}
	// An empty archive uploads cleanly, satisfies retention, prunes the good copies behind it
	// and is discovered on the day it is needed. Usually an unmounted volume.
	if files == 0 {
		return nil, fmt.Errorf("no files in %s: is the data directory mounted there?", dir)
	}
	return &Archive{Body: body.Bytes(), Digest: sum.Sum(nil)}, nil
}

// maxReply is how much of a rejection is read back: enough for a sentence from the other end,
// not enough for a server answering with HTML to put a page of it in the log.
const maxReply = 2 << 10

// Push posts one archive to a backup agent, multipart with the file under `backup` and the
// name beside it, which is what backio-agent documents.
func Push(ctx context.Context, client *http.Client, url, name string, body []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("backup", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(body); err != nil {
		return err
	}
	if err := w.WriteField("name", name); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// What it said, not just that it said no: the rejected token and the unreachable
		// remote live in the body.
		said, _ := io.ReadAll(io.LimitReader(res.Body, maxReply))
		return fmt.Errorf("the backup agent answered %s: %s", res.Status, bytes.TrimSpace(said))
	}
	return nil
}
