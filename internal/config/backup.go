package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseBackupURL validates the backup agent's address. Empty disables backups; anything
// malformed fails here rather than in a log line five minutes after startup.
func ParseBackupURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%s: %q is not a URL: %w", BackupURLEnv, s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s: %q must be an http:// or https:// URL, e.g. http://backup:8080/backup", BackupURLEnv, s)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%s: %q has no host, e.g. http://backup:8080/backup", BackupURLEnv, s)
	}
	return s, nil
}
