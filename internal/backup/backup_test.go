package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seed writes a data directory holding the four files the panel owns.
func seed(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func defaultFiles() map[string]string {
	return map[string]string{
		"admins.json":      `{"version":1,"admins":[]}`,
		"sessions.json":    `{"version":1,"sessions":[]}`,
		"subscribers.json": `{"version":1,"subscribers":[]}`,
		"passkeys.json":    `{"version":1,"passkeys":[]}`,
	}
}

// entries reads an archive back into name -> contents.
func entries(t *testing.T, body []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Mode != 0o600 {
			t.Errorf("%s: mode = %o, want 600", hdr.Name, hdr.Mode)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = string(data)
	}
	return out
}

// Every file goes in under one top-level data/, so extracting over a deployment directory
// puts each one back where compose mounts it from.
func TestBuildCarriesEveryFileUnderData(t *testing.T) {
	dir := seed(t, defaultFiles())

	got, err := Build(dir, time.Now(), discard())
	if err != nil {
		t.Fatal(err)
	}

	files := entries(t, got.Body)
	for name, want := range defaultFiles() {
		if files["data/"+name] != want {
			t.Errorf("data/%s = %q, want %q", name, files["data/"+name], want)
		}
	}
	if len(files) != 4 {
		t.Errorf("archived %d files, want 4: %v", len(files), files)
	}
}

// Half-written files and the operator's own subdirectories are not the panel's data.
func TestBuildSkipsTempFilesAndDirectories(t *testing.T) {
	files := defaultFiles()
	files["subscribers.json.tmp1234"] = `{"half":`
	dir := seed(t, files)
	if err := os.Mkdir(filepath.Join(dir, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := Build(dir, time.Now(), discard())
	if err != nil {
		t.Fatal(err)
	}

	for name := range entries(t, got.Body) {
		if name == "data/subscribers.json.tmp1234" || name == "data/backups" {
			t.Errorf("archived %s, want it skipped", name)
		}
	}
}

// An empty archive would upload cleanly, satisfy retention and be discovered on the day it
// is needed. Usually an unmounted volume.
func TestBuildEmptyDirectoryIsAnError(t *testing.T) {
	if _, err := Build(t.TempDir(), time.Now(), discard()); err == nil {
		t.Fatal("Build on an empty directory succeeded, want an error")
	}
}

func TestBuildMissingDirectoryIsAnError(t *testing.T) {
	if _, err := Build(filepath.Join(t.TempDir(), "nope"), time.Now(), discard()); err == nil {
		t.Fatal("Build on a missing directory succeeded, want an error")
	}
}

// The digest is over the files, not the tarball — whose gzip header carries a timestamp, so
// hashing it would make every check find a change.
func TestDigestIsStableAcrossTime(t *testing.T) {
	dir := seed(t, defaultFiles())

	first, err := Build(dir, time.Date(2026, 7, 30, 3, 15, 0, 0, time.UTC), discard())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(dir, time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC), discard())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first.Digest, second.Digest) {
		t.Error("digest changed with the clock alone")
	}
	if bytes.Equal(first.Body, second.Body) {
		t.Error("bodies are identical, so this test is not proving anything about the digest")
	}
}

func TestDigestIgnoresSessions(t *testing.T) {
	dir := seed(t, defaultFiles())
	before, err := Build(dir, time.Now(), discard())
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "sessions.json"),
		[]byte(`{"version":1,"sessions":[{"hash":"abc"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := Build(dir, time.Now(), discard())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(before.Digest, after.Digest) {
		t.Error("a write to sessions.json changed the digest, so signing in would trigger a backup")
	}
	// It still travels, so a restore does not sign every operator out.
	if got := entries(t, after.Body)["data/sessions.json"]; got != `{"version":1,"sessions":[{"hash":"abc"}]}` {
		t.Errorf("data/sessions.json = %q, want the new contents", got)
	}
}

func TestDigestFollowsTheOtherFiles(t *testing.T) {
	for _, name := range []string{"admins.json", "subscribers.json", "passkeys.json"} {
		t.Run(name, func(t *testing.T) {
			dir := seed(t, defaultFiles())
			before, err := Build(dir, time.Now(), discard())
			if err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"version":1,"changed":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			after, err := Build(dir, time.Now(), discard())
			if err != nil {
				t.Fatal(err)
			}

			if bytes.Equal(before.Digest, after.Digest) {
				t.Errorf("a write to %s left the digest alone, so the change would never be backed up", name)
			}
		})
	}
}

// A rename between two files must not produce the byte stream of another set.
func TestDigestSeparatesFileBoundaries(t *testing.T) {
	one, err := Build(seed(t, map[string]string{"admins.json": "ab", "passkeys.json": "c"}), time.Now(), discard())
	if err != nil {
		t.Fatal(err)
	}
	two, err := Build(seed(t, map[string]string{"admins.json": "a", "passkeys.json": "bc"}), time.Now(), discard())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(one.Digest, two.Digest) {
		t.Error("two different directories share a digest")
	}
}

func TestFilename(t *testing.T) {
	got := Filename(time.Date(2026, 7, 30, 3, 15, 0, 0, time.UTC))
	if want := "vlessvmorectl-20260730_031500.tgz"; got != want {
		t.Errorf("Filename = %q, want %q", got, want)
	}
}

func TestPushPostsTheArchive(t *testing.T) {
	var (
		method, name, filename string
		body                   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Error(err)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "backup":
				filename, body = part.FileName(), data
			case "name":
				name = string(data)
			}
		}
	}))
	defer srv.Close()

	if err := Push(context.Background(), srv.Client(), srv.URL, "vlessvmorectl-20260730_031500.tgz", []byte("payload")); err != nil {
		t.Fatal(err)
	}

	if method != http.MethodPost {
		t.Errorf("method = %s, want POST", method)
	}
	if want := "vlessvmorectl-20260730_031500.tgz"; filename != want || name != want {
		t.Errorf("filename = %q, name field = %q, want %q for both", filename, name, want)
	}
	if string(body) != "payload" {
		t.Errorf("body = %q, want %q", body, "payload")
	}
}

// The agent's own words, because "it answered 500" is a fact nobody can act on.
func TestPushReportsWhatTheAgentSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "the remote rejected the token", http.StatusForbidden)
	}))
	defer srv.Close()

	err := Push(context.Background(), srv.Client(), srv.URL, "a.tgz", []byte("x"))
	if err == nil {
		t.Fatal("Push against a 403 succeeded, want an error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("the remote rejected the token")) {
		t.Errorf("error = %q, want it to carry the agent's reply", err)
	}
}
