package config

import (
	"strings"
	"testing"
)

func TestParsePasskeyOriginAccepts(t *testing.T) {
	tests := []struct {
		name, raw, origin, rpid string
	}{
		{"plain https", "https://panel.example.com", "https://panel.example.com", "panel.example.com"},
		// A default port is the same origin as none, and a browser omits it. Normalising
		// them apart would make every comparison fail.
		{"explicit 443", "https://panel.example.com:443", "https://panel.example.com", "panel.example.com"},
		{"case folded", "HTTPS://Panel.Example.COM", "https://panel.example.com", "panel.example.com"},
		{"trailing slash", "https://panel.example.com/", "https://panel.example.com", "panel.example.com"},
		// The dev setup: Vite serves the SPA, so its origin is the one the browser sees.
		// The port stays on the origin and off the RP ID.
		{"vite on localhost", "http://localhost:5173", "http://localhost:5173", "localhost"},
		{"plain localhost", "http://localhost", "http://localhost", "localhost"},
		{"whitespace", "  https://panel.example.com  ", "https://panel.example.com", "panel.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePasskeyOrigin(tc.raw)
			if err != nil {
				t.Fatalf("ParsePasskeyOrigin(%q): %v", tc.raw, err)
			}
			if got == nil {
				t.Fatal("got nil, want a relying party")
			}
			if got.Origin != tc.origin {
				t.Errorf("Origin = %q, want %q", got.Origin, tc.origin)
			}
			if got.RPID != tc.rpid {
				t.Errorf("RPID = %q, want %q", got.RPID, tc.rpid)
			}
		})
	}
}

// Unset is how the feature is switched off, which is the default. Not an error.
func TestParsePasskeyOriginEmptyDisables(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n"} {
		got, err := ParsePasskeyOrigin(raw)
		if err != nil {
			t.Errorf("ParsePasskeyOrigin(%q): %v", raw, err)
		}
		if got != nil {
			t.Errorf("ParsePasskeyOrigin(%q) = %+v, want nil", raw, got)
		}
	}
}

// Every one of these fails inside the browser, where the panel cannot see it and cannot
// explain it — so they fail at startup instead.
func TestParsePasskeyOriginRejects(t *testing.T) {
	tests := []struct{ name, raw string }{
		{"no scheme", "panel.example.com"},
		{"wrong scheme", "ftp://panel.example.com"},
		{"no host", "https://"},
		{"userinfo", "https://alice:pw@panel.example.com"},
		{"a path", "https://panel.example.com/panel"},
		{"a query", "https://panel.example.com?a=1"},
		{"a fragment", "https://panel.example.com#x"},
		// The one an operator reaches for, and the reason the message names localhost.
		{"loopback IP", "http://127.0.0.1"},
		{"loopback IP with a port", "http://127.0.0.1:8080"},
		{"public IP", "https://203.0.113.4"},
		{"IPv6", "https://[::1]"},
		// Would never reach a secure context, so no ceremony could ever start.
		{"plain http on a real host", "http://panel.example.com"},
		{"empty label", "https://panel..example.com"},
		{"trailing dot", "https://panel.example.com."},
		{"underscore", "https://panel_1.example.com"},
		{"non-ascii", "https://пример.test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePasskeyOrigin(tc.raw)
			if err == nil {
				t.Fatalf("ParsePasskeyOrigin(%q) = %+v, want an error", tc.raw, got)
			}
			// Naming the variable is the difference between a fixable message and a shrug.
			if !strings.Contains(err.Error(), PasskeyOriginEnv) {
				t.Errorf("error does not name %s: %v", PasskeyOriginEnv, err)
			}
		})
	}
}

func TestLoadReadsThePasskeyOrigin(t *testing.T) {
	cfg, err := Load(func(k string) string {
		if k == PasskeyOriginEnv {
			return "https://panel.example.com"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Passkey == nil || cfg.Passkey.RPID != "panel.example.com" {
		t.Fatalf("Passkey = %+v", cfg.Passkey)
	}

	// And a bad one stops the process rather than quietly disabling the feature.
	if _, err := Load(func(k string) string {
		if k == PasskeyOriginEnv {
			return "http://panel.example.com"
		}
		return ""
	}); err == nil {
		t.Error("Load accepted plain http on a public host")
	}
}
