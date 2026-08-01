package api

import (
	"net/http"

	"vlessvmorectl/internal/aaguid"
)

// logoURLPrefix is where the passkey provider logos are served, and mirrors the directory they
// are embedded from. The listing hands out whole URLs built on this, so the panel assembles no
// paths of its own.
const logoURLPrefix = "/assets/icons/webauthn/"

// logoURL is the URL for one logo filename, or "" for the empty filename that means we hold none.
func logoURL(filename string) string {
	if filename == "" {
		return ""
	}
	return logoURLPrefix + filename
}

// aaguidLogo serves one passkey provider's logo.
//
// Outside /api and outside requireSession, because it is a static asset: these are 54 public
// logos, identical for every visitor, and putting them behind a session would only mean the
// account page draws broken images for a second while it authenticates them. Nothing here reads
// the request beyond the filename in the path.
func (s *Server) aaguidLogo(w http.ResponseWriter, r *http.Request) {
	body, ok := aaguid.File(r.PathValue("file"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such logo")
		return
	}

	etag := etagOf(body)
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml")
	h.Set("ETag", etag)
	// A year, on the same reasoning that earns Vite's bundle one: the filename carries a hash of
	// these exact bytes, so this URL's content cannot change. A logo the upstream list redraws
	// gets a new filename and a new URL, and no cache is left holding the old one behind a name
	// it no longer matches.
	h.Set("Cache-Control", "public, max-age=31536000, immutable")

	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
