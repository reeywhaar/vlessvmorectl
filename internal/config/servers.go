package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// Server is one vlessvmore node this panel manages.
type Server struct {
	// ID is what the SPA puts in its routes and query keys. Derived from the URL, so
	// it is stable across restarts and across reordering the environment variable —
	// an index would not be, and swapping two entries would silently re-point every
	// bookmark at a different node.
	ID string `json:"id"`

	// URL is the normalised base, e.g. "https://vpn.example.com". No trailing slash,
	// no default port, lowercase scheme and host.
	URL string `json:"url"`

	// Origin is scheme://host, which for a base URL with no path is the same string
	// as URL. Kept as its own field because it is what the proxy's allowlist is keyed
	// on, and conflating "the base I build request URLs from" with "the origin I
	// compare against" is how that check rots.
	Origin string `json:"-"`

	// Token is the bearer secret for this node. It grants complete control of the
	// server, including reading every user's subscription URL.
	//
	// `json:"-"` is load-bearing, not tidiness: it means no incidental marshal can
	// emit it. A debug endpoint, an error that happens to contain the struct, a
	// future `GET /api/servers` that forgets to project — none of them can leak the
	// token, because the only way to serialise it is to name it explicitly. As of
	// today nothing does, and the proxy reads the field directly in Go.
	Token string `json:"-"`
}

// LogValue redacts the token whenever a Server is logged.
//
// Without this, `log.Info("servers", "list", cfg.Servers)` — an entirely reasonable
// line for someone to write while debugging — would print every node's credential into
// the container log, and from there into whatever ships logs off the box.
func (s *Server) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", s.ID),
		slog.String("url", s.URL),
		slog.String("token", "…redacted…"),
	)
}

// ParseServers reads the server list from the raw values of VLESSVMORE_SERVERS and
// its VLESSVMORE_TOKENS alias.
//
// The format is a comma-separated list of `url|token`:
//
//	https://vpn-nl.example.com|MHDJWEZ5NQ7K2P8RTX4VYB6CFA3GS1WD,https://vpn-de.example.com|QK7M2XA9…
//
// Newlines separate entries too, so a compose block scalar reads naturally. Neither a
// URL nor a vlessvmore token can contain whitespace, so that is unambiguous.
//
// The failure policy is deliberately asymmetric. A malformed entry, or two entries for
// the same node, is a typo: fail immediately and loudly, because the operator is about
// to go looking at container logs anyway and a crash-loop is where they will look.
// Zero servers is *not* a typo — it is "not configured yet" — so it returns an empty
// list and no error, letting the operator bring the panel up, log in, and read an empty
// state that tells them what to set.
func ParseServers(serversVar, tokensVar string) ([]*Server, error) {
	raw := strings.TrimSpace(serversVar)
	alias := strings.TrimSpace(tokensVar)

	switch {
	case raw == "":
		raw = alias
	case alias != "" && alias != raw:
		// Silently preferring one would hand the operator a panel talking to the
		// wrong set of nodes, with the other variable sitting right there looking
		// like it took effect.
		return nil, fmt.Errorf("%s and %s are both set and differ; remove one (%s is the current name)",
			ServersEnv, TokensEnv, ServersEnv)
	}
	if raw == "" {
		return nil, nil
	}

	entries := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})

	servers := make([]*Server, 0, len(entries))
	seen := make(map[string]int, len(entries))
	n := 0

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		// The one lenient case, and it earns it: a trailing comma has exactly one
		// possible intent.
		if entry == "" {
			continue
		}
		n++

		s, err := parseServerEntry(entry)
		if err != nil {
			// Identified by position as well as by text, because redact refuses to
			// quote an entry it could not split — and that is precisely the case
			// where the operator most needs to know which one is wrong.
			return nil, fmt.Errorf("%s: entry %d (%s): %w", ServersEnv, n, redact(entry), err)
		}
		if prev, dup := seen[s.Origin]; dup {
			return nil, fmt.Errorf("%s: %s appears twice (entries %d and %d); each node may be listed once",
				ServersEnv, s.URL, prev+1, n)
		}
		seen[s.Origin] = n
		servers = append(servers, s)
	}

	// Impossible unless sha256 collides in twelve hex characters, which it will not.
	// Checked anyway, because the consequence — two nodes sharing a route and a query
	// key — would be baffling rather than merely wrong.
	byID := make(map[string]string, len(servers))
	for _, s := range servers {
		if other, clash := byID[s.ID]; clash {
			return nil, fmt.Errorf("%s: %s and %s produced the same id %s", ServersEnv, other, s.URL, s.ID)
		}
		byID[s.ID] = s.URL
	}
	return servers, nil
}

func parseServerEntry(entry string) (*Server, error) {
	rawURL, token, ok := strings.Cut(entry, "|")
	if !ok {
		return nil, errors.New(`missing "|": each entry is url|token, e.g. https://vpn.example.com|MHDJWEZ5…`)
	}
	rawURL = strings.TrimSpace(rawURL)
	token = strings.TrimSpace(token)

	if rawURL == "" {
		return nil, errors.New("the url is empty")
	}
	if token == "" {
		return nil, errors.New("the token is empty")
	}
	if err := validateToken(token); err != nil {
		return nil, err
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%q is not a URL: %w", rawURL, err)
	}
	switch u.Scheme {
	case "http", "https":
	case "":
		return nil, fmt.Errorf("%q has no scheme; write https://%s", rawURL, rawURL)
	default:
		return nil, fmt.Errorf("%q: scheme must be http or https, got %q", rawURL, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%q has no host", rawURL)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%q must not contain a username or password; the token is the credential", rawURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%q must be a bare origin with no query or fragment", rawURL)
	}
	// vlessvmore serves /api at the root of its origin. A path prefix here would
	// produce requests to routes that do not exist, and every one of them would come
	// back as the same opaque 404 that a bad token produces — a genuinely hard
	// afternoon.
	if p := strings.Trim(u.Path, "/"); p != "" {
		return nil, fmt.Errorf("%q must not have a path; vlessvmore serves /api at the origin root", rawURL)
	}

	origin := NormalizeOrigin(u.Scheme, u.Host)
	return &Server{
		ID:     originID(origin),
		URL:    origin,
		Origin: origin,
		Token:  token,
	}, nil
}

// NormalizeOrigin renders scheme+host in the one canonical spelling this program uses:
// lowercase, and with the scheme's default port stripped.
//
// Stripping the default port is not cosmetic. "https://x" and "https://x:443" are the
// same origin; if they normalise differently then the duplicate check misses, the SPA
// renders one node twice under two ids, and — the part that matters — the proxy's
// allowlist has an entry that never matches what the SPA actually sends. The sibling's
// config.go strips default ports from cors_origins for the same reason.
func NormalizeOrigin(scheme, host string) string {
	scheme = strings.ToLower(scheme)
	host = strings.ToLower(host)

	switch scheme {
	case "https":
		host = strings.TrimSuffix(host, ":443")
	case "http":
		host = strings.TrimSuffix(host, ":80")
	}
	return scheme + "://" + host
}

// originID is the twelve-hex-character handle the SPA routes on.
//
// Hashed rather than using the hostname so a node's address does not end up in the
// browser's URL bar, history and any screen recording of the panel. Twelve characters
// of opacity is cheap; for this particular kind of tool it is worth having.
func originID(origin string) string {
	sum := sha256.Sum256([]byte(origin))
	return hex.EncodeToString(sum[:])[:12]
}

// validateToken rejects anything that could not be sent as a bearer credential.
//
// The token goes straight into an Authorization header. Go refuses to transmit a header
// containing a control character, and the resulting error surfaces at request time as
// a transport failure with no obvious cause — much better to refuse it at startup where
// the message can say which entry is at fault.
func validateToken(token string) error {
	for i := 0; i < len(token); i++ {
		if c := token[i]; c < '!' || c > '~' {
			return fmt.Errorf("the token contains a character that cannot be sent in an Authorization header (byte %d is %q)", i, c)
		}
	}
	return nil
}

// redact truncates an entry at its first "|" so an error message can quote what the
// operator wrote without echoing a credential into the logs.
//
// Every error in this file routes through it, including the "missing |" case: an entry
// that failed to split may well be `https://x TOKEN` with a typo'd separator, and that
// is exactly when a naive error message would print the whole thing.
func redact(entry string) string {
	if url, _, ok := strings.Cut(entry, "|"); ok {
		return strings.TrimSpace(url) + "|…"
	}
	// No separator found. We cannot tell which part is which, so quote nothing.
	return "…"
}
