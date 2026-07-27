package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

// SPA serves the built React bundle.
//
// Every file is read into memory once, at construction, with its ETag and content type
// precomputed. A Vite bundle is a few hundred kilobytes, so the memory is free, and
// holding it as bytes sidesteps every http.FileServerFS quirk that would otherwise need
// working around: the implicit /index.html redirect, directory listings, and range
// handling we do not want. It also means the content type comes from an explicit table
// rather than mime.TypeByExtension, which on a minimal container depends on an
// /etc/mime.types that may not be installed — the same reasoning, and the same shape,
// as the sibling's internal/api/static.go.
type SPA struct {
	assets map[string]asset
	index  asset
	log    *slog.Logger
}

// asset holds a file in both the form it is stored in and the form a client without
// gzip support needs.
//
// The build gzips the bundle before it is embedded, which is worth doing twice over: it
// takes ~480 KB off the binary, and it means the compressed bytes are served straight
// from memory rather than being recompressed on every request. `plain` is decompressed
// once at startup for the rare client that sends no Accept-Encoding — a cost of a few
// hundred kilobytes of memory, paid once, to avoid a decompression on a request path.
type asset struct {
	body    []byte // as stored: gzipped when gzipped is true
	plain   []byte // always uncompressed
	etag    string
	ctype   string
	gzipped bool
}

// placeholderIndex stands in when web/dist holds no build.
//
// It lives here, in reviewable Go source, rather than as a committed stub index.html.
// A stub file would be overwritten by every local `npm run build` and show up forever
// as a modified file in git status; and diagnosing the missing build once at startup,
// where it can be logged, beats serving a blank page and letting someone work out why.
const placeholderIndex = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>vlessvmorectl</title>
<style>
 body{font:16px/1.6 system-ui,sans-serif;max-width:34rem;margin:4rem auto;padding:0 1rem;
      background:#131317;color:#ececf1}
 code{background:#1c1c22;border:1px solid #2c2c35;border-radius:6px;padding:.15em .4em}
 a{color:#8f8fdc}
</style></head>
<body>
<h1>The frontend has not been built</h1>
<p>This binary was compiled without a React bundle in <code>web/dist</code>, so there is
   no interface to serve. The API is running normally.</p>
<p>Build it:</p>
<pre><code>cd web &amp;&amp; npm ci &amp;&amp; npm run build</code></pre>
<p>…and rebuild the binary, or use the published image
   <code>ghcr.io/reeywhaar/vlessvmorectl</code>, which always contains one.</p>
</body></html>
`

// NewSPA loads dist into memory. A missing index.html is not fatal: the API is still
// useful and the placeholder explains itself.
func NewSPA(dist fs.FS, log *slog.Logger) (*SPA, error) {
	s := &SPA{assets: make(map[string]asset), log: log}

	err := fs.WalkDir(dist, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(dist, p)
		if err != nil {
			return err
		}

		// The build stage gzips the bundle before it is embedded. Registering a
		// "foo.js.gz" under "/foo.js" means nothing else — not this package, not the
		// bundle's own asset references — has to know the difference.
		name, gzipped := strings.CutSuffix(p, ".gz")
		if !gzipped {
			s.assets["/"+p] = asset{body: b, plain: b, etag: etagOf(b), ctype: contentType(p)}
			return nil
		}

		plain, err := gunzip(b)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		s.assets["/"+name] = asset{
			body:  b,
			plain: plain,
			// Keyed on the uncompressed bytes, so both representations of a file share
			// one validator. That is what makes Vary: Accept-Encoding correct rather
			// than a way for a cache to hand someone the wrong encoding.
			etag:    etagOf(plain),
			ctype:   contentType(name),
			gzipped: true,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if idx, ok := s.assets["/index.html"]; ok {
		s.index = idx
	} else {
		b := []byte(placeholderIndex)
		s.index = asset{body: b, plain: b, etag: etagOf(b), ctype: "text/html; charset=utf-8"}
		log.Warn("serving a placeholder page: web/dist/index.html is missing, so this build has no frontend")
	}
	return s, nil
}

func gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func (s *SPA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	clean := path.Clean(r.URL.Path)
	if a, ok := s.assets[clean]; ok && clean != "/index.html" {
		// Vite content-hashes these names, so a given URL's bytes never change and
		// the browser can be told to keep them for a year.
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		s.serve(w, r, a)
		return
	}

	// Fall back to the SPA only for things that look like navigation. A missing
	// /app.js should be a 404 in devtools, not an HTML document served with a
	// JavaScript content type — that failure presents as a MIME-type error with no
	// hint that the file simply is not there.
	if !looksLikeNavigation(r, clean) {
		writeError(w, http.StatusNotFound, "not found: "+r.URL.Path)
		return
	}

	// no-cache, never no-store: index.html is tiny, a 304 is the common case, and
	// caching it hard would strand a browser on a stale bundle reference after a
	// deploy.
	w.Header().Set("Cache-Control", "no-cache")
	s.serve(w, r, s.index)
}

func (s *SPA) serve(w http.ResponseWriter, r *http.Request, a asset) {
	h := w.Header()
	h.Set("ETag", a.etag)
	h.Set("Content-Type", a.ctype)

	body := a.plain
	if a.gzipped {
		// Vary regardless of which representation this particular request gets: a cache
		// that stored the compressed bytes without it would later hand them to a client
		// that cannot read them.
		h.Set("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			h.Set("Content-Encoding", "gzip")
			body = a.body
		}
	}

	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// acceptsGzip is a substring check rather than a full Accept-Encoding parse.
//
// The only thing that could go wrong is `gzip;q=0`, which means "explicitly do not send
// me gzip" — vanishingly rare, and every browser made this century sends a plain `gzip`.
// Handling it costs a parser; getting it wrong costs one unreadable response to a client
// that went out of its way to ask.
func acceptsGzip(r *http.Request) bool {
	ae := r.Header.Get("Accept-Encoding")
	return strings.Contains(ae, "gzip") && !strings.Contains(ae, "gzip;q=0")
}

// looksLikeNavigation distinguishes "the browser is loading a page" from "the page is
// loading a resource that does not exist".
func looksLikeNavigation(r *http.Request, clean string) bool {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		return true
	}
	// No extension on the last segment: /login, /servers/a3f21c8ee4b1 — a route.
	return path.Ext(clean) == ""
}

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

// contentType is an explicit table rather than mime.TypeByExtension, which reads
// /etc/mime.types and therefore answers differently on a scratch container than on the
// machine the code was tested on.
func contentType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".map":
		return "application/json; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
