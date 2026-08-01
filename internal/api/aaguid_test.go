package api

import (
	"bytes"
	"net/http"
	"testing"

	"vlessvmorectl/internal/aaguid"
)

const (
	// 1Password, one of the twelve providers shipping a second logo for dark mode, and Bitwarden,
	// which like most ships one that suits both.
	guidWithDark = "bada5566-a7aa-401f-bd96-45619a55120d"
	guidNoDark   = "d548826e-79b4-db40-a3d8-11116f7e8349"
)

func logoPath(t *testing.T, guid string, dark bool) string {
	t.Helper()
	light, darkName := aaguid.Logos(guid)
	name := light
	if dark {
		if darkName == "" {
			t.Fatalf("%s has no dark logo", guid)
		}
		name = darkName
	}
	if name == "" {
		t.Fatalf("%s has no logo", guid)
	}
	return logoURLPrefix + name
}

func TestAAGUIDLogoNeedsNoSession(t *testing.T) {
	h := newHarness(t, "")

	rec := h.doWithHeaders(http.MethodGet, logoPath(t, guidNoDark, false), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without a cookie", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("<svg")) {
		t.Errorf("body is not an svg: %.60q", rec.Body.String())
	}
	// A year, which the filename's content hash is what earns.
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}
}

// The two variants are two files with two hashes, so they are two URLs and cannot be confused by
// a cache — which is the whole reason the theme is not a query parameter on one URL.
func TestAAGUIDDarkLogoIsItsOwnURL(t *testing.T) {
	h := newHarness(t, "")

	light := h.doWithHeaders(http.MethodGet, logoPath(t, guidWithDark, false), nil)
	dark := h.doWithHeaders(http.MethodGet, logoPath(t, guidWithDark, true), nil)
	if light.Code != http.StatusOK || dark.Code != http.StatusOK {
		t.Fatalf("statuses = %d and %d, want 200 for both", light.Code, dark.Code)
	}
	if bytes.Equal(light.Body.Bytes(), dark.Body.Bytes()) {
		t.Error("the two variants served the same bytes")
	}

	// And a provider with one logo has no second URL to serve.
	if _, dark := aaguid.Logos(guidNoDark); dark != "" {
		t.Errorf("%s unexpectedly has a dark logo (%s)", guidNoDark, dark)
	}
}

func TestAAGUIDLogoRevalidates(t *testing.T) {
	h := newHarness(t, "")

	path := logoPath(t, guidNoDark, false)
	first := h.doWithHeaders(http.MethodGet, path, nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	again := h.doWithHeaders(http.MethodGet, path, map[string]string{"If-None-Match": etag})
	if again.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", again.Code)
	}
	if again.Body.Len() != 0 {
		t.Errorf("304 carried %d bytes", again.Body.Len())
	}
}

// A filename reaches this handler from a URL and is joined to a path, so what it can and cannot
// address is worth asserting rather than trusting.
func TestAAGUIDLogoRejectsAnythingButAGeneratedFilename(t *testing.T) {
	h := newHarness(t, "")

	light, _ := aaguid.Logos(guidNoDark)
	for _, name := range []string{
		"bitwarden_light.svg",              // no hash
		"bitwarden_light.0123456789ab.svg", // a hash, but not of anything we hold
		"..%2f..%2f..%2fetc%2fpasswd",      // the obvious one
		"..%2fauthenticators.json",         // and the neighbour worth reaching for
		light + "/",                        // real name, trailing separator
		"nonsense",                         // no shape at all
	} {
		rec := h.doWithHeaders(http.MethodGet, logoURLPrefix+name, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q: status = %d, want 404", name, rec.Code)
		}
	}
}
