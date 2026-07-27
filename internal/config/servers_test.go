package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

const tok = "MHDJWEZ5NQ7K2P8RTX4VYB6CFA3GS1WD"
const tok2 = "QK7M2XA9GS1WDMHDJWEZ5NQ7K2P8RTX4"

func TestParseServersHappyPath(t *testing.T) {
	got, err := ParseServers("https://vpn-nl.example.com|"+tok+",https://vpn-de.example.com|"+tok2, "")
	if err != nil {
		t.Fatalf("ParseServers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d servers, want 2", len(got))
	}
	if got[0].URL != "https://vpn-nl.example.com" || got[0].Token != tok {
		t.Errorf("first: got %+v", got[0])
	}
	if got[1].URL != "https://vpn-de.example.com" || got[1].Token != tok2 {
		t.Errorf("second: got %+v", got[1])
	}
}

func TestParseServersSeparators(t *testing.T) {
	// Newlines so a compose block scalar reads naturally; a trailing comma tolerated
	// because it has exactly one possible intent.
	in := "\n  https://a.example.com|" + tok + " ,\n\thttps://b.example.com|" + tok2 + ",\n"
	got, err := ParseServers(in, "")
	if err != nil {
		t.Fatalf("ParseServers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d servers, want 2: %+v", len(got), got)
	}
}

func TestParseServersEmptyIsNotAnError(t *testing.T) {
	// "Not configured yet" is not a typo. The panel must come up so the operator can
	// log in and read an empty state that says which variable to set — rather than a
	// container that dies before they can reach it.
	got, err := ParseServers("", "")
	if err != nil {
		t.Fatalf("ParseServers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d servers, want 0", len(got))
	}
}

func TestParseServersTokensAlias(t *testing.T) {
	got, err := ParseServers("", "https://a.example.com|"+tok)
	if err != nil {
		t.Fatalf("alias alone: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("alias alone: got %d servers, want 1", len(got))
	}

	both := "https://a.example.com|" + tok
	if _, err := ParseServers(both, both); err != nil {
		t.Errorf("both set and identical should be fine, got %v", err)
	}

	// Silently preferring one would hand the operator a panel talking to the wrong
	// nodes, with the other variable sitting right there looking like it took effect.
	_, err = ParseServers(both, "https://b.example.com|"+tok2)
	if err == nil {
		t.Fatal("both set and differing: want an error")
	}
	if !strings.Contains(err.Error(), ServersEnv) || !strings.Contains(err.Error(), TokensEnv) {
		t.Errorf("error should name both variables, got %v", err)
	}
}

func TestParseServersRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // substring of the message
	}{
		{"no separator", "https://a.example.com " + tok, `missing "|"`},
		{"empty token", "https://a.example.com|", "token is empty"},
		{"empty url", "|" + tok, "url is empty"},
		{"no scheme", "a.example.com|" + tok, "no scheme"},
		{"bad scheme", "ftp://a.example.com|" + tok, "must be http or https"},
		{"no host", "https://|" + tok, "no host"},
		{"userinfo", "https://user:pw@a.example.com|" + tok, "username or password"},
		{"query", "https://a.example.com?x=1|" + tok, "query or fragment"},
		{"path", "https://a.example.com/panel|" + tok, "must not have a path"},
		{"token with space", "https://a.example.com|abc def", "Authorization header"},
		{"token with control char", "https://a.example.com|abc\x01def", "Authorization header"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseServers(tc.in, "")
			if err == nil {
				t.Fatalf("want an error for %q", tc.in)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

// TestParseServersDetectsDuplicatesAcrossSpellings is the case a naive normaliser
// misses. If ":443" and the bare host do not fold together, the duplicate check passes,
// the SPA renders one node twice under two ids, and the proxy allowlist gains an entry
// that never matches what the SPA sends.
func TestParseServersDetectsDuplicatesAcrossSpellings(t *testing.T) {
	tests := []string{
		"https://a.example.com|" + tok + ",https://a.example.com:443|" + tok2,
		"http://a.example.com|" + tok + ",http://a.example.com:80|" + tok2,
		"https://a.example.com|" + tok + ",https://A.EXAMPLE.COM|" + tok2,
		"https://a.example.com|" + tok + ",https://a.example.com/|" + tok2,
	}
	for _, in := range tests {
		if _, err := ParseServers(in, ""); err == nil {
			t.Errorf("want a duplicate error for %q", redact(in))
		}
	}
}

func TestNormalizeOrigin(t *testing.T) {
	tests := []struct{ scheme, host, want string }{
		{"https", "a.example.com", "https://a.example.com"},
		{"https", "a.example.com:443", "https://a.example.com"},
		{"https", "a.example.com:8443", "https://a.example.com:8443"},
		{"http", "a.example.com:80", "http://a.example.com"},
		{"http", "a.example.com:443", "http://a.example.com:443"}, // not http's default
		{"HTTPS", "A.Example.COM", "https://a.example.com"},
	}
	for _, tc := range tests {
		if got := NormalizeOrigin(tc.scheme, tc.host); got != tc.want {
			t.Errorf("NormalizeOrigin(%q, %q) = %q, want %q", tc.scheme, tc.host, got, tc.want)
		}
	}
}

// TestServerIDIsStableAcrossReordering: an index would not be. Swapping two entries in
// the environment variable must not silently re-point every bookmark and cached query
// key at a different node.
func TestServerIDIsStableAcrossReordering(t *testing.T) {
	a := "https://a.example.com|" + tok
	b := "https://b.example.com|" + tok2

	first, err := ParseServers(a+","+b, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseServers(b+","+a, "")
	if err != nil {
		t.Fatal(err)
	}

	if first[0].ID != second[1].ID {
		t.Errorf("id for a.example.com changed with position: %s vs %s", first[0].ID, second[1].ID)
	}
	if first[1].ID != second[0].ID {
		t.Errorf("id for b.example.com changed with position: %s vs %s", first[1].ID, second[0].ID)
	}
	if first[0].ID == first[1].ID {
		t.Error("two different nodes produced the same id")
	}
}

// TestErrorsNeverContainTheToken is a permanent guard against someone later
// "improving" one of these messages by quoting the whole entry.
func TestErrorsNeverContainTheToken(t *testing.T) {
	const secret = "SUPERSECRETTOKENVALUE0123456789A"
	malformed := []string{
		"https://a.example.com" + secret,           // no separator at all
		"https://a.example.com " + secret,          // space instead of a pipe
		"ftp://a.example.com|" + secret,            // bad scheme
		"https://a.example.com/panel|" + secret,    // path
		"https://user:pw@a.example.com|" + secret,  // userinfo
		"https://a.example.com?q=1|" + secret,      // query
		"a.example.com|" + secret,                  // no scheme
		"https://a.example.com|" + secret + "\x01", // unsendable byte
	}
	for _, in := range malformed {
		_, err := ParseServers(in, "")
		if err == nil {
			t.Errorf("want an error for %q", in)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the token leaked into an error message: %v", err)
		}
	}
}

// TestServerRedactsInLogs guards the other half: `log.Info("servers", "list",
// cfg.Servers)` is an entirely reasonable line for someone to write while debugging,
// and without LogValue it prints every node's credential into the container log.
func TestServerRedactsInLogs(t *testing.T) {
	const secret = "SUPERSECRETTOKENVALUE0123456789A"
	servers, err := ParseServers("https://a.example.com|"+secret, "")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	log.Info("servers", "list", servers[0], "all", servers)

	if strings.Contains(buf.String(), secret) {
		t.Errorf("the token leaked into a log line:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "a.example.com") {
		t.Errorf("redaction removed the useful part too:\n%s", buf.String())
	}
}

func TestLookupByOrigin(t *testing.T) {
	cfg, err := Load(func(k string) string {
		if k == ServersEnv {
			return "https://a.example.com|" + tok
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.LookupByOrigin("https://a.example.com"); !ok {
		t.Error("the configured origin does not resolve")
	}
	// The whole point: an exact match, not a prefix one.
	for _, miss := range []string{
		"https://a.example.com.evil.test",
		"https://evil.test",
		"http://a.example.com", // different scheme is a different origin
		"https://a.example.com:8443",
	} {
		if _, ok := cfg.LookupByOrigin(miss); ok {
			t.Errorf("%q resolved, but it is not a configured origin", miss)
		}
	}
}

func TestLoadRejectsBadLogLevel(t *testing.T) {
	_, err := Load(func(k string) string {
		if k == LogLevelEnv {
			return "verbose"
		}
		return ""
	})
	if err == nil {
		t.Fatal("want an error for an unknown log level")
	}
}
