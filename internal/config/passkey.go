package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// RPDisplayName is what an authenticator shows in its prompt. Not configurable: a passkey
// manager already displays the domain, so a second copy of it here is noise.
const RPDisplayName = "vlessvmore panel"

// Passkey is the WebAuthn relying party this panel is, or nil when unconfigured.
type Passkey struct {
	// Origin is compared byte-for-byte against clientDataJSON.origin, so it is stored in
	// the one canonical spelling NormalizeOrigin produces — the same spelling a browser
	// serialises. A lenient comparison here is how an origin check rots; see
	// Config.LookupByOrigin for the same argument about the proxy's allowlist.
	Origin string

	// RPID is Origin's hostname, and is baked into every credential an authenticator
	// stores. Changing the panel's hostname therefore invalidates every passkey.
	RPID string
}

// ParsePasskeyOrigin reads VLESSVMORE_PASSKEY_ORIGIN.
//
// Empty is not an error: it means passkeys are switched off, which is the default and a
// perfectly reasonable way to run this. Anything non-empty and wrong *is* an error, because
// a subtly wrong relying party does not degrade — every ceremony fails inside the browser,
// where the panel never sees it and cannot explain it.
//
// The RP ID is derived from the origin rather than configured beside it. That is a feature:
// a derived RP ID cannot be set wider than the origin it is served from, which is the one
// way this setting could be dangerously wrong.
func ParsePasskeyOrigin(raw string) (*Passkey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %q is not a URL: %w", PasskeyOriginEnv, raw, err)
	}
	switch u.Scheme {
	case "http", "https":
	case "":
		return nil, fmt.Errorf("%s: %q has no scheme; write https://%s", PasskeyOriginEnv, raw, raw)
	default:
		return nil, fmt.Errorf("%s: %q: scheme must be http or https, got %q", PasskeyOriginEnv, raw, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%s: %q has no host", PasskeyOriginEnv, raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%s: %q must not contain a username or password", PasskeyOriginEnv, raw)
	}
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%s: %q must be a bare origin like https://panel.example.com, with no path",
			PasskeyOriginEnv, raw)
	}

	host := strings.ToLower(u.Hostname())

	// An RP ID is a domain, and the spec has no notion of an IP one. Browsers refuse the
	// ceremony outright, so this would otherwise present as passkeys that simply never
	// work — and 127.0.0.1 is exactly what somebody developing locally reaches for.
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return nil, fmt.Errorf("%s: %q is an IP address, and WebAuthn requires a domain; use http://localhost:5173 for development",
			PasskeyOriginEnv, raw)
	}
	if err := validateRPID(host); err != nil {
		return nil, fmt.Errorf("%s: %q: %w", PasskeyOriginEnv, raw, err)
	}

	// WebAuthn needs a secure context. Browsers make one exception, for loopback, which is
	// what makes local development possible at all.
	if u.Scheme == "http" && !HostIsLoopback(u.Host) {
		return nil, fmt.Errorf("%s: %q is plain HTTP on a non-loopback host, which every browser refuses for passkeys; use https://, or http://localhost for development",
			PasskeyOriginEnv, raw)
	}

	return &Passkey{Origin: NormalizeOrigin(u.Scheme, u.Host), RPID: host}, nil
}

// validateRPID rejects hostnames a browser would not accept as a relying party id.
func validateRPID(host string) error {
	if len(host) > 253 {
		return fmt.Errorf("hostname is longer than 253 bytes")
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return fmt.Errorf("hostname has an empty label")
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
		default:
			// An internationalised domain reaches the browser as punycode, and that is
			// the form it puts in clientDataJSON, so it is the form to configure.
			return fmt.Errorf("%q is not a character a WebAuthn relying party id may contain; if this is an internationalised domain, write its punycode form", r)
		}
	}
	return nil
}
