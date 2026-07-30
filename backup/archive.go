package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"
)

// archiveDataDir is the archive's single top-level directory, named after the one mount a
// deployment has, so restoring is extracting the archive over the deployment directory:
//
//	docker compose stop vlessvmorectl
//	tar xzf vlessvmorectl-20260730_031500.tgz -C /srv/vlessvmorectl
//	docker compose start vlessvmorectl
const archiveDataDir = "data"

// tempRe matches the files store.writeJSONAtomic leaves in the data directory between
// creating a new version and renaming it into place. They are half-written by definition.
var tempRe = regexp.MustCompile(`\.tmp\d+$`)

type archiveEntry struct {
	name string
	data []byte
}

// writeArchive tars source into dest, gzipped, via a .part file renamed on success — so an
// interrupted run leaves nothing for retention to treat as a real archive.
func writeArchive(source, dest string, now time.Time) error {
	entries, err := snapshot(source)
	if err != nil {
		return err
	}
	// An empty archive would upload cleanly, satisfy retention, prune the good copies
	// behind it, and be discovered at restore time. Usually an unmounted volume.
	if len(entries) == 0 {
		return fmt.Errorf("no files in %s: is the data directory mounted there?", source)
	}

	part := dest + ".part"
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := writeTarGz(f, entries, now); err != nil {
		f.Close()
		os.Remove(part)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(part)
		return err
	}
	return os.Rename(part, dest)
}

// snapshot reads the data directory into memory. The files are a few kilobytes of JSON, and
// a tar header needs its entry's size up front.
//
// The panel writes each file by rename, so every file read here is a whole one. There is no
// transaction across files and none is needed: the worst a write landing between two reads
// can do is archive a session whose administrator is not in it, which restores as a browser
// that has to sign in again.
func snapshot(dir string) ([]archiveEntry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	// os.ReadDir sorts by name and the prefix below is the same for every entry, so two
	// runs over unchanged data produce identical archives.
	var out []archiveEntry
	for _, item := range items {
		name := item.Name()
		if item.IsDir() {
			// The store keeps a flat directory, so this is the operator's — most likely
			// /backups mounted inside it. Said out loud rather than skipped quietly.
			logError("archive", "Skipping directory "+filepath.Join(dir, name)+
				": only files at the top level are archived")
			continue
		}
		if !item.Type().IsRegular() || tempRe.MatchString(name) {
			continue
		}
		full := filepath.Join(dir, name)
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", full, err)
		}
		out = append(out, archiveEntry{name: path.Join(archiveDataDir, name), data: data})
	}
	return out, nil
}

func writeTarGz(w io.Writer, entries []archiveEntry, now time.Time) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		// 0600 throughout: the archive holds every share token in the clear. No directory
		// entries, so extracting does not re-chmod a data directory that already exists.
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     e.name,
			Mode:     0o600,
			Size:     int64(len(e.data)),
			ModTime:  now.UTC().Truncate(time.Second),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write %s header: %w", e.name, err)
		}
		if _, err := tw.Write(e.data); err != nil {
			return fmt.Errorf("write %s: %w", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	return nil
}
