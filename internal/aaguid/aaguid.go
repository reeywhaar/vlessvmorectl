// Package aaguid identifies the passkey provider a credential lives in — Apple Passwords,
// Bitwarden — from the authenticator id the credential was registered with.
//
// Names and logos are both here, rather than split with the names in the frontend's own
// dependency. They come from one upstream list, and generating them from two snapshots of it is
// how a provider ends up with a name and no logo, or a logo and no name. One source, one
// regeneration step.
//
// The two travel differently, which is the only asymmetry: a name is short and every row wants
// one, so it rides along with the credential it belongs to, while a logo is kilobytes only one
// row will ever draw and is fetched by URL when that row draws it.
//
// Regenerate with `go run internal/aaguid/gen.go`; see that file for what is kept and why.
package aaguid

import (
	"embed"
	"encoding/json"
	"regexp"
	"sync"
)

// Identical logos are one file, named for the provider that shares it most simply and for a hash
// of its bytes — the 45 Yubico ids all resolve to yubikey_5_series_light.<hash>.svg. The hash is
// what makes a logo cacheable for ever: redrawing one upstream produces a different filename, so
// no cache is left holding an image the panel has moved on from.
//
//go:embed icons
var icons embed.FS

//go:embed authenticators.json
var listJSON []byte

type record struct {
	Name  string `json:"name"`
	Light string `json:"light,omitempty"`
	Dark  string `json:"dark,omitempty"`
}

// Decoded once, on the first credential that needs it, rather than at init: a panel with
// passkeys switched off never asks, and this is 373 entries it would parse anyway.
var list = sync.OnceValue(func() map[string]record {
	var m map[string]record
	if err := json.Unmarshal(listJSON, &m); err != nil {
		// Unreachable short of a corrupt binary — the file is generated, embedded and parsed by
		// tests. Degrading to "no name, no logo" beats taking the panel down over a caption.
		return nil
	}
	return m
})

// A filename arrives from a URL and is about to be joined to a path, so it is matched rather
// than sanitised. Nothing outside the shape gen.go writes can address anything, which is a
// stronger statement than any amount of cleaning "..", and it is the whole of the defence.
var filename = regexp.MustCompile(`^[a-z0-9_]+\.[0-9a-f]{12}\.svg$`)

// Name returns what to call an authenticator, or "" for one the list does not cover.
//
// Never a trust signal, and never an input to a decision. The id this looks up is asserted by
// the authenticator about itself and verified by nothing; see aaguidUUID in internal/api.
func Name(id string) string {
	return list()[id].Name
}

// Logos returns the filenames of an authenticator's logo in each theme.
//
// Light is "" when we hold none, which is the ordinary answer for a hardware key and for any id
// the list does not cover — the panel draws its own key glyph for those. Dark is "" whenever the
// provider ships one logo that suits both, which is most of them, and the caller uses Light.
func Logos(id string) (light, dark string) {
	rec := list()[id]
	return rec.Light, rec.Dark
}

// File returns one logo by the filename Logos gave out.
func File(name string) ([]byte, bool) {
	if !filename.MatchString(name) {
		return nil, false
	}
	b, err := icons.ReadFile("icons/webauthn/" + name)
	if err != nil {
		return nil, false
	}
	return b, true
}
