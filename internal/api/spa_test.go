package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testSPA(t *testing.T, files fstest.MapFS) *SPA {
	t.Helper()
	spa, err := NewSPA(files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSPA: %v", err)
	}
	return spa
}

var bundle = fstest.MapFS{
	"index.html":            {Data: []byte("<!doctype html><title>panel</title>")},
	"access.html":           {Data: []byte("<!doctype html><title>share</title>")},
	"favicon.svg":           {Data: []byte("<svg/>")},
	"assets/app-abc123.js":  {Data: []byte("console.log(1)")},
	"assets/app-def456.css": {Data: []byte("body{}")},
}

func get(spa *SPA, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	spa.ServeHTTP(rec, req)
	return rec
}

func TestSPAServesAssets(t *testing.T) {
	spa := testSPA(t, bundle)

	rec := get(spa, "/assets/app-abc123.js", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if rec.Body.String() != "console.log(1)" {
		t.Errorf("body: %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type: got %q", ct)
	}
	// Vite content-hashes these names, so the bytes at a given URL never change.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control: got %q, want immutable", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag")
	}
}

func TestSPAConditionalRequest(t *testing.T) {
	spa := testSPA(t, bundle)

	first := get(spa, "/assets/app-abc123.js", nil)
	etag := first.Header().Get("ETag")

	second := get(spa, "/assets/app-abc123.js", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Errorf("got %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("a 304 carried a body: %q", second.Body.String())
	}

	stale := get(spa, "/assets/app-abc123.js", map[string]string{"If-None-Match": `"nope"`})
	if stale.Code != http.StatusOK {
		t.Errorf("a stale ETag: got %d, want 200", stale.Code)
	}
}

func TestSPAFallsBackForRoutes(t *testing.T) {
	spa := testSPA(t, bundle)

	for _, path := range []string{
		"/", "/login", "/servers/a3f21c8ee4b1", "/servers/a3f21c8ee4b1/users/u_1",
		"/subscribers",
	} {
		rec := get(spa, path, map[string]string{"Accept": "text/html"})
		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<title>panel") {
			t.Errorf("%s: did not serve index.html", path)
		}
		// Never no-store: index.html is tiny, a 304 is the common case, and caching it
		// hard would strand a browser on a stale bundle reference after a deploy.
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control = %q, want no-cache", path, cc)
		}
	}
}

// The two islands are two documents, and which one a navigation gets is the whole of the
// boundary that keeps operator code out of a subscriber's browser. Pinned here rather than
// left to inspection, because it is one prefix check away from silently regressing into
// "everyone gets the panel".
func TestSPAServesTheRightIslandShell(t *testing.T) {
	spa := testSPA(t, bundle)

	const token = "QK7M2XA9TESTTKEN0123456789ABCDEF"
	cases := map[string]string{
		"/access/" + token: "share",
		// A truncated link, with and without the trailing slash. Both are the island's:
		// falling through to the panel here would show a login form to somebody who has
		// never heard of this panel.
		"/access":  "share",
		"/access/": "share",
		// Not the island. "/accessories" starts with "/access" as a string and is an
		// ordinary panel route; a prefix check written without the separator would hand
		// it the wrong document.
		"/accessories": "panel",
		"/":            "panel",
		"/subscribers": "panel",
	}

	for path, want := range cases {
		rec := get(spa, path, map[string]string{"Accept": "text/html"})
		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "<title>"+want) {
			t.Errorf("%s: served the wrong island's shell, want %q", path, want)
		}
	}
}

// A build with no access.html should still load *something* for a share link. A link that
// dead-ends looks to its holder like the link itself is wrong, and they will ask for
// another one that behaves identically.
func TestSPAFallsBackToThePanelShellWithoutAccessHTML(t *testing.T) {
	spa := testSPA(t, fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>panel</title>")},
	})
	rec := get(spa, "/access/QK7M2XA9TESTTKEN0123456789ABCDEF", map[string]string{"Accept": "text/html"})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<title>panel") {
		t.Error("expected the panel shell as a fallback")
	}
}

// TestSPADoesNotFallBackForMissingAssets: a broken script reference should be a 404 in
// devtools, not an HTML document served with a JavaScript content type — that presents
// as a MIME-type error with no hint the file is simply absent.
func TestSPADoesNotFallBackForMissingAssets(t *testing.T) {
	spa := testSPA(t, bundle)

	for _, path := range []string{"/missing.js", "/assets/gone-000000.js", "/logo.png", "/style.css"} {
		rec := get(spa, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype") {
			t.Errorf("%s: served index.html for a missing asset", path)
		}
	}
}

func TestSPARejectsTraversal(t *testing.T) {
	spa := testSPA(t, bundle)

	for _, path := range []string{"/../../etc/passwd", "/assets/../../etc/passwd", "/%2e%2e/%2e%2e/etc/passwd"} {
		rec := get(spa, path, nil)
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "root:") {
			t.Errorf("%s: served something from outside the bundle", path)
		}
	}
}

// TestSPAPlaceholderWhenUnbuilt: `go build` works without Node installed, so a binary
// with no bundle is a real state. It must explain itself rather than 500.
func TestSPAPlaceholderWhenUnbuilt(t *testing.T) {
	spa := testSPA(t, fstest.MapFS{".gitkeep": {Data: []byte{}}})

	rec := get(spa, "/", map[string]string{"Accept": "text/html"})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "has not been built") {
		t.Errorf("the placeholder does not say what is wrong: %s", body)
	}
	if !strings.Contains(body, "npm run build") {
		t.Errorf("the placeholder does not say how to fix it: %s", body)
	}
}

// TestSPAServesPrecompressedAssets covers the build-time gzip.
//
// The bundle is stored compressed to keep it out of the binary uncompressed, and served
// compressed to avoid recompressing it on every request. Both representations have to
// work, and they must share one ETag or a cache will eventually hand somebody bytes they
// cannot read.
func TestSPAServesPrecompressedAssets(t *testing.T) {
	const source = "console.log('hello world hello world hello world')"

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(source)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	spa := testSPA(t, fstest.MapFS{
		"index.html":              {Data: []byte("<!doctype html><title>panel</title>")},
		"assets/app-abc123.js.gz": {Data: buf.Bytes()},
	})

	// Registered under its real name: nothing in the bundle's own asset references
	// knows the file was compressed.
	t.Run("gzip client gets the stored bytes", func(t *testing.T) {
		rec := get(spa, "/assets/app-abc123.js", map[string]string{"Accept-Encoding": "gzip, deflate, br"})
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
			t.Errorf("Content-Encoding: got %q, want gzip", enc)
		}
		if !bytes.Equal(rec.Body.Bytes(), buf.Bytes()) {
			t.Error("the compressed bytes were not served verbatim")
		}
		if v := rec.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
			t.Errorf("Vary: got %q, want Accept-Encoding", v)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("Content-Type: got %q — .gz must not leak into the type", ct)
		}
	})

	t.Run("a client without gzip gets it decompressed", func(t *testing.T) {
		rec := get(spa, "/assets/app-abc123.js", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", rec.Code)
		}
		if enc := rec.Header().Get("Content-Encoding"); enc != "" {
			t.Errorf("Content-Encoding: got %q, want none", enc)
		}
		if rec.Body.String() != source {
			t.Errorf("body: got %q, want the decompressed source", rec.Body.String())
		}
		// Still varies, or a cache could store this copy and later serve it to a client
		// that asked for gzip — or worse, the reverse.
		if v := rec.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
			t.Errorf("Vary: got %q, want Accept-Encoding", v)
		}
	})

	t.Run("one ETag across both representations", func(t *testing.T) {
		zipped := get(spa, "/assets/app-abc123.js", map[string]string{"Accept-Encoding": "gzip"})
		plain := get(spa, "/assets/app-abc123.js", nil)

		etag := zipped.Header().Get("ETag")
		if etag == "" || etag != plain.Header().Get("ETag") {
			t.Fatalf("ETags differ: %q vs %q", etag, plain.Header().Get("ETag"))
		}
		rec := get(spa, "/assets/app-abc123.js", map[string]string{
			"Accept-Encoding": "gzip",
			"If-None-Match":   etag,
		})
		if rec.Code != http.StatusNotModified {
			t.Errorf("revalidation: got %d, want 304", rec.Code)
		}
	})

	t.Run("the .gz name itself is not routable", func(t *testing.T) {
		if rec := get(spa, "/assets/app-abc123.js.gz", nil); rec.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404", rec.Code)
		}
	})
}

func TestSPARejectsNonGET(t *testing.T) {
	spa := testSPA(t, bundle)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	spa.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("got %d, want 405", rec.Code)
	}
}

func TestSPAHeadHasNoBody(t *testing.T) {
	spa := testSPA(t, bundle)

	req := httptest.NewRequest(http.MethodHead, "/assets/app-abc123.js", nil)
	rec := httptest.NewRecorder()
	spa.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned a body: %q", rec.Body.String())
	}
}
