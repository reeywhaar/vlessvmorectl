// Package config models vlessvmorectl's configuration, which is entirely
// environment-driven.
//
// There is no config file, and that is deliberate. The only thing this program needs
// to be told is which vlessvmore nodes it manages and the token for each — one
// variable, naturally expressed by `docker compose --env-file`, and awkward to express
// as a mounted file that then has to be kept in sync with the compose stanza that
// mounts it. The sibling project has a config.json because it has thirteen fields; this
// has one that matters.
//
// The listen address is not configurable at all. It is :80 inside the container and the
// operator remaps it with a port binding, which is the same stance vlessvmore takes and
// for the same reason: a port number inside a container is not a thing an operator
// should have to think about twice.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// ListenAddr is where `serve` listens, inside the container. Not configurable —
// remap it with `docker run -p 8080:80`.
const ListenAddr = ":80"

// Environment variables read by this package.
const (
	// ServersEnv is the canonical name.
	ServersEnv = "VLESSVMORE_SERVERS"
	// TokensEnv is accepted as an alias for ServersEnv. It is the name the variable
	// had before entries grew a URL, and honouring it costs three lines.
	TokensEnv = "VLESSVMORE_TOKENS"

	// LogLevelEnv sets the slog level: debug, info, warn or error.
	LogLevelEnv = "VLESSVMORE_LOG_LEVEL"

	// PasskeyOriginEnv is the address operators open in a browser, e.g.
	// https://panel.example.com. Unset disables passkeys entirely.
	//
	// It has to be told, and cannot be inferred: Host and X-Forwarded-Host are both
	// client-supplied, and this service takes nothing from them. See the comment above
	// AccessPath in internal/api/subscribers.go, which is the same argument.
	PasskeyOriginEnv = "VLESSVMORE_PASSKEY_ORIGIN"
)

// Config is everything the process was told at startup.
type Config struct {
	Servers  []*Server
	LogLevel slog.Level

	// Passkey is nil when PasskeyOriginEnv is unset, and that nil is the single "are
	// passkeys on?" predicate in the program: the routes are not even registered without
	// it, so the endpoints do not exist rather than existing and refusing.
	Passkey *Passkey

	// byOrigin is the proxy's allowlist: an exact map from a normalised
	// "scheme://host" to the server it belongs to. A map rather than a scan over
	// Servers because the lookup happens on every proxied request, and — far more
	// importantly — because a map hit is an exact match and cannot be accidentally
	// written as a prefix comparison later. See LookupByOrigin.
	byOrigin map[string]*Server
}

// Load reads the environment. It returns an error for anything malformed; see
// ParseServers for why zero servers is not one of those things.
func Load(getenv func(string) string) (*Config, error) {
	servers, err := ParseServers(getenv(ServersEnv), getenv(TokensEnv))
	if err != nil {
		return nil, err
	}

	level := slog.LevelInfo
	if v := strings.TrimSpace(getenv(LogLevelEnv)); v != "" {
		if err := level.UnmarshalText([]byte(v)); err != nil {
			return nil, fmt.Errorf("%s: %q is not a log level (debug, info, warn, error)", LogLevelEnv, v)
		}
	}

	passkey, err := ParsePasskeyOrigin(getenv(PasskeyOriginEnv))
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Servers:  servers,
		LogLevel: level,
		Passkey:  passkey,
		byOrigin: make(map[string]*Server, len(servers)),
	}
	for _, s := range servers {
		cfg.byOrigin[s.Origin] = s
	}
	return cfg, nil
}

// LoadFromEnv is Load against the real process environment.
func LoadFromEnv() (*Config, error) { return Load(os.Getenv) }

// LookupByOrigin resolves a normalised origin to the server it identifies.
//
// This is the proxy's allowlist check and the single most security-sensitive function
// in the package. It takes an already-normalised origin — see NormalizeOrigin — and
// answers only on an exact match.
//
// It must never be replaced with, or supplemented by, a prefix comparison against
// Server.URL. "https://vpn.example.com.attacker.test" has "https://vpn.example.com" as
// a prefix and is a completely different host; a proxy that accepted it would forward
// this operator's bearer token to whoever registered that domain.
func (c *Config) LookupByOrigin(origin string) (*Server, bool) {
	s, ok := c.byOrigin[origin]
	return s, ok
}

// ServerByID resolves the id the SPA uses in its routes.
func (c *Config) ServerByID(id string) (*Server, bool) {
	for _, s := range c.Servers {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

// HTTPTimeout bounds a single proxied request. Generous enough for a `users` listing
// on a node with a cold SQLite page cache, short enough that a hung upstream does not
// pin a browser connection until it gives up on its own.
const HTTPTimeout = 15 * time.Second
