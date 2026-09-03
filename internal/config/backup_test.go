package config

import "testing"

func TestParseBackupURLAccepts(t *testing.T) {
	for _, raw := range []string{
		"http://backup:8080/backup",
		"https://backup.example.com/backup",
		"  http://backup:8080/backup  ",
	} {
		got, err := ParseBackupURL(raw)
		if err != nil {
			t.Errorf("ParseBackupURL(%q): %v", raw, err)
		}
		if got == "" {
			t.Errorf("ParseBackupURL(%q) = %q, want an address", raw, got)
		}
	}
}

// Unset is how backups are switched off, which is the default. Not an error.
func TestParseBackupURLEmptyDisables(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n"} {
		got, err := ParseBackupURL(raw)
		if err != nil {
			t.Errorf("ParseBackupURL(%q): %v", raw, err)
		}
		if got != "" {
			t.Errorf("ParseBackupURL(%q) = %q, want empty", raw, got)
		}
	}
}

// Each of these otherwise fails five minutes after startup, in a log line nobody is reading.
func TestParseBackupURLRejects(t *testing.T) {
	for _, raw := range []string{
		"backup:8080/backup",
		"ftp://backup:8080/backup",
		"http://",
		"/backup",
		"://backup",
	} {
		if got, err := ParseBackupURL(raw); err == nil {
			t.Errorf("ParseBackupURL(%q) = %q, want an error", raw, got)
		}
	}
}
