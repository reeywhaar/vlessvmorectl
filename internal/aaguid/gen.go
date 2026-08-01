//go:build ignore

// Regenerates the embedded authenticator list. Run from the repository root:
//
//	go run internal/aaguid/gen.go
//
// The source is the community list at passkeydeveloper/passkey-authenticator-aaguids, which the
// passkey providers themselves contribute to. It is fetched rather than vendored as JSON because
// the JSON is 6.8 MB of base64, and what this keeps is 170 KB.
//
// Two reductions get it there.
//
// Only the SVG icons are kept. The rest are PNGs, and they belong almost entirely to hardware
// security keys, whose AAGUID a client usually strips before we ever see it (see aaguidUUID in
// internal/api). Embedding two megabytes of logos that will rarely resolve is a poor trade
// against a name and the panel's own key glyph, which is what those fall back to.
//
// And identical logos are stored once. That is not a marginal saving: Yubico ships one logo
// across 45 AAGUIDs, and folding the duplicates away halves the whole set.
//
// A surviving file is named for the provider *and* the hash of its bytes — bitwarden_light.<hash>
// .svg — which is what lets one be served immutable for a year. The name keeps the directory
// legible and says in devtools which logo a request is for; the hash means a logo the upstream
// list redraws arrives under a new URL rather than sitting stale in caches behind the old one.
// Where a group of ids shares one logo, the shortest of their names wins: the 45 Yubico entries
// yield yubikey_5_series_light.<hash>.svg rather than whichever variant came first.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const source = "https://raw.githubusercontent.com/passkeydeveloper/passkey-authenticator-aaguids/main/combined_aaguid.json"

type entry struct {
	Name      string `json:"name"`
	IconLight string `json:"icon_light"`
	IconDark  string `json:"icon_dark"`
}

// record is one authenticator as the panel needs it: what to call it, and which stored files
// draw it. Light is empty for the 282 entries that ship no SVG; Dark only when it differs, and
// the panel falls back to Light for a provider whose one logo suits both themes.
type record struct {
	Name  string `json:"name"`
	Light string `json:"light,omitempty"`
	Dark  string `json:"dark,omitempty"`
}

// logo is one distinct image, and every provider name that shares it.
type logo struct {
	svg     []byte
	variant string
	names   []string
}

// An SVG reaches the browser through <img>, which does not run script — but it is also a
// document if somebody opens the URL directly. The panel's CSP already forbids inline script
// there, so this is the second of two locks rather than the only one. A hit means the upstream
// list has changed character and wants a human, not a workaround.
var dangerous = regexp.MustCompile(`(?i)<script|<foreignObject|javascript:|\son\w+\s*=`)

var guidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	// Mirrors the path these are served at, so that a URL in devtools and a file in the tree are
	// the same string. See logoURLPrefix in internal/api.
	dir := filepath.Join("internal", "aaguid", "icons", "webauthn")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	list, err := fetch()
	if err != nil {
		return err
	}

	// Wholesale, so a logo the upstream list drops does not linger in the binary for ever.
	old, err := filepath.Glob(filepath.Join(dir, "*.svg"))
	if err != nil {
		return err
	}
	for _, p := range old {
		if err := os.Remove(p); err != nil {
			return err
		}
	}

	guids := make([]string, 0, len(list))
	for guid := range list {
		if !guidPattern.MatchString(guid) {
			return fmt.Errorf("%q is not an aaguid", guid)
		}
		guids = append(guids, guid)
	}
	// Sorted, so that which name a shared logo is filed under does not depend on map order.
	sort.Strings(guids)

	logos := map[string]*logo{}
	records := map[string]*record{}

	claim := func(svg []byte, name, variant string) (string, error) {
		if dangerous.Match(svg) {
			return "", fmt.Errorf("%s: the svg carries script; check the upstream list", name)
		}
		key := string(svg)
		if l, ok := logos[key]; ok {
			l.names = append(l.names, name)
			return key, nil
		}
		logos[key] = &logo{svg: svg, variant: variant, names: []string{name}}
		return key, nil
	}

	for _, guid := range guids {
		e := list[guid]
		rec := &record{Name: e.Name}

		// Every name, not only the ones with a logo: a hardware key that resolves to
		// "YubiKey 5 Series" beside the panel's own key glyph is the good outcome here.
		if light, ok := svg(e.IconLight); ok {
			if rec.Light, err = claim(light, e.Name, "light"); err != nil {
				return err
			}
			// Claimed only when it differs. Most providers ship one logo that works on both
			// themes, and the panel falls back to Light when Dark is empty.
			if dark, ok := svg(e.IconDark); ok && !bytes.Equal(dark, light) {
				if rec.Dark, err = claim(dark, e.Name, "dark"); err != nil {
					return err
				}
			}
		}
		records[guid] = rec
	}

	filenames, written, err := writeLogos(dir, logos)
	if err != nil {
		return err
	}

	out := make(map[string]record, len(records))
	for guid, rec := range records {
		if rec.Light != "" {
			rec.Light = filenames[rec.Light]
		}
		if rec.Dark != "" {
			rec.Dark = filenames[rec.Dark]
		}
		out[guid] = *rec
	}

	// Indented and with sorted keys — encoding/json sorts a map's — so that regenerating this
	// produces a diff a reviewer can read, rather than one line that changed everywhere.
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join("internal", "aaguid", "authenticators.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return err
	}

	uses := 0
	for _, l := range logos {
		uses += len(l.names)
	}
	fmt.Printf("%d authenticators (%d KB), %d logo references in %d files (%d KB, %d duplicates folded away)\n",
		len(out), len(encoded)/1024, uses, len(logos), written/1024, uses-len(logos))
	return nil
}

// writeLogos names each distinct image after the shortest provider that uses it, and returns the
// name to record for it.
func writeLogos(dir string, logos map[string]*logo) (map[string]string, int, error) {
	keys := make([]string, 0, len(logos))
	for key := range logos {
		keys = append(keys, key)
	}
	// By the name each will take rather than by image bytes, so that the suffix a collision
	// gets is stable across runs instead of following whichever image sorted first.
	sort.Slice(keys, func(i, j int) bool {
		a, b := base(logos[keys[i]]), base(logos[keys[j]])
		if a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})

	filenames := make(map[string]string, len(logos))
	taken := map[string]bool{}
	written := 0
	for _, key := range keys {
		l := logos[key]
		stem := base(l)
		for i := 2; taken[stem]; i++ {
			// Two unrelated providers whose names slugify the same. Rare, and the mapping is
			// what resolves an id either way — this only keeps the filenames distinct.
			stem = fmt.Sprintf("%s_%d", base(l), i)
		}
		taken[stem] = true

		sum := sha256.Sum256(l.svg)
		name := stem + "." + hex.EncodeToString(sum[:6]) + ".svg"
		filenames[key] = name
		if err := os.WriteFile(filepath.Join(dir, name), l.svg, 0o644); err != nil {
			return nil, 0, err
		}
		written += len(l.svg)
	}
	return filenames, written, nil
}

// base is the filename a logo takes, without its extension.
func base(l *logo) string {
	shortest := l.names[0]
	for _, n := range l.names[1:] {
		if len(n) < len(shortest) || (len(n) == len(shortest) && n < shortest) {
			shortest = n
		}
	}
	return slug(shortest) + "_" + l.variant
}

func slug(name string) string {
	s := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(name), "_"), "_")
	// Some upstream names are a sentence. The mapping carries the real one; this is a filename.
	if len(s) > 48 {
		s = strings.Trim(s[:48], "_")
	}
	if s == "" {
		return "authenticator"
	}
	return s
}

func fetch() (map[string]entry, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(source)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", source, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var list map[string]entry
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("%s: no entries", source)
	}
	return list, nil
}

// svg decodes one data URI, and reports false for anything that is not an SVG.
func svg(uri string) ([]byte, bool) {
	const prefix = "data:image/svg+xml;base64,"
	if !strings.HasPrefix(uri, prefix) {
		return nil, false
	}
	payload := strings.TrimPrefix(uri, prefix)
	// Some entries are unpadded, which StdEncoding rejects outright.
	b, err := base64.StdEncoding.WithPadding(base64.NoPadding).DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		return nil, false
	}
	return b, true
}
