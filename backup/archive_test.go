package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// readArchive extracts a tgz into a name → contents map, plus the headers, so a test can
// assert on the mode as well as the payload.
func readArchive(t *testing.T, path string) (map[string]string, map[string]*tar.Header) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open the archive: %s", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("the archive is not gzip: %s", err)
	}
	defer gz.Close()

	files := map[string]string{}
	headers := map[string]*tar.Header{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read the tar: %s", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %s", hdr.Name, err)
		}
		files[hdr.Name] = string(data)
		headers[hdr.Name] = hdr
	}
	return files, headers
}

func keys(m map[string]string) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// dataDir writes a plausible data directory: the panel's three files, one of the temporary
// files it leaves mid-rename, and a subdirectory.
func dataDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	for path, content := range map[string]string{
		"admins.json":               `{"version":1,"admins":[]}`,
		"sessions.json":             `{"sessions":{}}`,
		"subscribers.json":          `{"subscribers":[]}`,
		"admins.json.tmp1234567890": `{"version":1,"adm`,
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %s", path, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "backups"), 0o700); err != nil {
		t.Fatalf("create the subdirectory: %s", err)
	}
	return dir
}

func TestWriteArchive(t *testing.T) {
	source := dataDir(t)
	dest := filepath.Join(t.TempDir(), "vlessvmorectl-20260730_031500.tgz")
	now := time.Date(2026, 7, 30, 3, 15, 0, 123456789, time.UTC)

	if err := writeArchive(source, dest, now); err != nil {
		t.Fatalf("writeArchive: %s", err)
	}

	files, headers := readArchive(t, dest)

	// Everything under data/, so extracting over a deployment directory puts each file back
	// where the compose file mounts it from. The temp file and the subdirectory are absent.
	want := "data/admins.json, data/sessions.json, data/subscribers.json"
	if got := keys(files); got != want {
		t.Errorf("archive holds %s\nwant           %s", got, want)
	}
	if got := files["data/admins.json"]; got != `{"version":1,"admins":[]}` {
		t.Errorf("data/admins.json = %q", got)
	}

	for archivedName, hdr := range headers {
		if hdr.Mode != 0o600 {
			t.Errorf("%s is mode %#o, want 0600", archivedName, hdr.Mode)
		}
		if hdr.Typeflag != tar.TypeReg {
			t.Errorf("%s is type %q, want a regular file", archivedName, hdr.Typeflag)
		}
		if !hdr.ModTime.Equal(now.Truncate(time.Second)) {
			t.Errorf("%s has ModTime %s, want %s", archivedName, hdr.ModTime, now.Truncate(time.Second))
		}
	}

	// A leftover .part is a name retention does not recognise, so it would sit there forever.
	if _, err := os.Stat(dest + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the .part file was left behind: %v", err)
	}
}

// Two runs over unchanged data produce identical bytes, so "did anything change" is
// answerable by comparing checksums.
func TestWriteArchiveIsDeterministic(t *testing.T) {
	source := dataDir(t)
	out := t.TempDir()
	now := time.Date(2026, 7, 30, 3, 15, 0, 0, time.UTC)

	var archives [2][]byte
	for i := range archives {
		dest := filepath.Join(out, string(rune('a'+i))+".tgz")
		if err := writeArchive(source, dest, now); err != nil {
			t.Fatalf("writeArchive: %s", err)
		}
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read the archive: %s", err)
		}
		archives[i] = data
	}
	if !bytes.Equal(archives[0], archives[1]) {
		t.Error("two archives over the same data differ")
	}
}

// An empty source directory means the volume is not mounted where this expects it, and the
// archive it would otherwise produce holds nothing on the day it is needed.
func TestWriteArchiveRefusesAnEmptyDirectory(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "vlessvmorectl-20260730_031500.tgz")

	err := writeArchive(t.TempDir(), dest, testNow)
	if err == nil {
		t.Fatal("an empty data directory produced an archive")
	}
	if !strings.Contains(err.Error(), "mounted") {
		t.Errorf("the error does not name the likely cause: %s", err)
	}
	for _, path := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after the failure", filepath.Base(path))
		}
	}
}

// A directory holding nothing but temp files is the same failure: nothing archivable.
func TestWriteArchiveRefusesTempFilesOnly(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "admins.json.tmp42"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write the temp file: %s", err)
	}
	dest := filepath.Join(t.TempDir(), "vlessvmorectl-20260730_031500.tgz")

	if err := writeArchive(source, dest, testNow); err == nil {
		t.Fatal("a directory of temp files produced an archive")
	}
}

func TestWriteArchiveMissingDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "absent")
	dest := filepath.Join(t.TempDir(), "vlessvmorectl-20260730_031500.tgz")

	err := writeArchive(source, dest, testNow)
	if err == nil {
		t.Fatal("a missing data directory produced an archive")
	}
	if !strings.Contains(err.Error(), source) {
		t.Errorf("the error does not name the directory: %s", err)
	}
}
