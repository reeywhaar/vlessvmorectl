// Command backup archives vlessvmorectl's data directory and forwards it to backio.
//
// One shot per invocation: archive, optionally encrypt, upload, prune. The schedule lives
// in backup_loop.sh, so a stuck run cannot silently skip the next one.
//
// A close sibling of vlessvmore's backup sidecar and of
// github.com/Reeywhaar/vaultwarden_backup: same environment variables, same log format.
// Retention follows vlessvmore's rather than vaultwarden_backup's — see retention.go.
//
// Unlike vlessvmore's it needs nothing from the panel: that one fetches its archive from an
// HTTP endpoint because a live SQLite database cannot be copied file by file, while everything
// this panel owns is JSON written by atomic rename.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// defaultBackupDir is where archives are kept locally. Mount a volume over it to keep
// copies that do not depend on the remote being reachable.
const defaultBackupDir = "/backups"

// defaultDataDir is store.DefaultDir, where the panel keeps its files inside its own
// container. Mounting the volume at the same path here lets one VLESSVMORECTL_DATA_DIR mean
// the same thing to both.
//
// Not imported from internal/store, which pulls in bcrypt: importing only the standard
// library is what lets this image build without fetching a module.
const defaultDataDir = "/var/lib/vlessvmorectl"

type config struct {
	dir          string
	source       string
	url          string
	provider     string
	subdirectory string
	token        string
	password     string
}

func main() {
	if err := backup(); err != nil {
		logError("backup", err.Error())
		os.Exit(1)
	}
}

func backup() error {
	cfg := config{
		dir:          envOr("BACKUP_DIR", defaultBackupDir),
		source:       envOr("VLESSVMORECTL_DATA_DIR", defaultDataDir),
		url:          envOr("BACKIO_URL", "http://backio:8080"),
		provider:     envOr("BACKIO_PROVIDER", "gdrive"),
		subdirectory: os.Getenv("BACKIO_SUBDIRECTORY"),
		token:        os.Getenv("BACKUP_TOKEN"),
		password:     os.Getenv("BACKUP_PASSWORD"),
	}
	if cfg.subdirectory == "" {
		return fmt.Errorf("BACKIO_SUBDIRECTORY is not set")
	}

	log("backup", "Starting backup")

	if err := os.MkdirAll(cfg.dir, 0o700); err != nil {
		return err
	}

	// One reading of the clock, so the entry timestamps and the filename cannot straddle
	// a second.
	now := time.Now()
	ts := now.UTC().Format(timeLayout)
	archiveName := fmt.Sprintf("vlessvmorectl-%s.tgz", ts)
	archive := filepath.Join(cfg.dir, archiveName)

	if err := writeArchive(cfg.source, archive, now); err != nil {
		return err
	}
	log("archive", "Wrote "+archiveName+" from "+cfg.source)

	if cfg.password != "" {
		encryptedName := fmt.Sprintf("vlessvmorectl-%s.zip", ts)
		encrypted := filepath.Join(cfg.dir, encryptedName)

		log("encrypt", "Encrypting to "+encryptedName)
		// -mx=1: the payload is already gzipped, so compressing again buys nothing.
		if err := run("7z", "a", "-tzip", "-p"+cfg.password, "-mem=AES256", "-mx=1",
			encrypted, archive); err != nil {
			return err
		}
		// 7z creates the file at the umask, unlike writeArchive, which opens it 0600.
		// Encrypted or not, the contents are every share token this panel has minted.
		if err := os.Chmod(encrypted, 0o600); err != nil {
			return err
		}
		if err := os.Remove(archive); err != nil {
			return err
		}
		archive, archiveName = encrypted, encryptedName
	}

	if err := uploadToBackio(archive, archiveName, cfg); err != nil {
		return err
	}

	cleanupLocalBackups(cfg)
	cleanupRemoteBackups(cfg)
	return nil
}

func cleanupLocalBackups(cfg config) {
	log("cleanup", "Running local retention policy: 3 today, 3 daily, 1 weekly, 1 monthly")

	names, err := listLocalBackups(cfg.dir)
	if err != nil {
		logError("cleanup", "Failed to list local backups: "+err.Error())
		return
	}
	for _, name := range toRemove(names) {
		log("cleanup", "Removing local: "+name)
		if err := os.Remove(filepath.Join(cfg.dir, name)); err != nil {
			logError("cleanup", fmt.Sprintf("Failed to remove local %s: %s", name, err))
		}
	}
}

func cleanupRemoteBackups(cfg config) {
	if cfg.token == "" {
		return
	}
	log("remote", "Running remote retention policy: 3 today, 3 daily, 1 weekly, 1 monthly")

	names, err := listBackioBackups(cfg)
	if err != nil {
		logError("remote", "Failed to list remote backups: "+err.Error())
		return
	}
	for _, name := range toRemove(names) {
		log("remote", "Removing remote: "+name)
		if err := deleteBackioBackup(name, cfg); err != nil {
			logError("remote", fmt.Sprintf("Failed to delete remote %s: %s", name, err))
		}
	}
}

func uploadToBackio(archive, archiveName string, cfg config) error {
	if cfg.token == "" {
		log("remote", "Skipping upload: BACKUP_TOKEN not set")
		return nil
	}

	log("remote", "Uploading "+archiveName+" to "+cfg.provider)

	fileBytes, err := os.ReadFile(archive)
	if err != nil {
		return err
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("backup", archiveName)
	if err != nil {
		return err
	}
	if _, err := part.Write(fileBytes); err != nil {
		return err
	}
	for k, v := range map[string]string{
		"name":         archiveName,
		"subdirectory": cfg.subdirectory,
		"provider":     cfg.provider,
	} {
		if err := w.WriteField(k, v); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, cfg.url+"/backup", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	responseBytes, _ := io.ReadAll(resp.Body)
	responseText := string(responseBytes)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload failed (%d): %s", resp.StatusCode, responseText)
	}

	var result struct {
		Status      string `json:"status"`
		Destination string `json:"destination"`
	}
	if err := json.Unmarshal(responseBytes, &result); err != nil {
		return fmt.Errorf("invalid response: %s", responseText)
	}
	if result.Status != "ok" {
		return fmt.Errorf("upload failed: %s", responseText)
	}

	log("remote", "Remote backup success: "+result.Destination)
	return nil
}

func listBackioBackups(cfg config) ([]string, error) {
	params := url.Values{}
	params.Set("provider", cfg.provider)
	params.Set("subdirectory", cfg.subdirectory)

	req, err := http.NewRequest(http.MethodGet, cfg.url+"/backup?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// A create-only token is a legitimate configuration: it produces working backups
	// with no remote pruning, rather than an error every hour.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		log("remote", "Skipping remote list: insufficient permissions")
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("list failed (%d): %s", resp.StatusCode, string(text))
	}

	var items []struct {
		Name  string `json:"Name"`
		IsDir bool   `json:"IsDir"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}

	var names []string
	for _, item := range items {
		if !item.IsDir {
			names = append(names, item.Name)
		}
	}
	return names, nil
}

func deleteBackioBackup(name string, cfg config) error {
	params := url.Values{}
	params.Set("provider", cfg.provider)
	params.Set("subdirectory", cfg.subdirectory)
	params.Set("name", name)

	req, err := http.NewRequest(http.MethodDelete, cfg.url+"/backup?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logError("remote", "Skipping remote delete "+name+": insufficient permissions")
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("delete failed (%d): %s", resp.StatusCode, string(text))
	}
	return nil
}

func listLocalBackups(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func log(operation, message string) {
	logMsg("info", operation, message)
}

func logError(operation, message string) {
	logMsg("error", operation, message)
}

func logMsg(level, operation, message string) {
	entry := map[string]string{
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		"level":     level,
		"operation": operation,
		"message":   message,
	}
	b, _ := json.Marshal(entry)
	if level == "error" {
		fmt.Fprintln(os.Stderr, string(b))
	} else {
		fmt.Fprintln(os.Stdout, string(b))
	}
}

// timeLayout is the archive name's timestamp, UTC, matching what retention.go parses back
// out of a name.
const timeLayout = "20060102_150405"

func run(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s failed: %s", cmd, msg)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
