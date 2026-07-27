// Package web owns the built React bundle on the Go side.
//
// The embed lives here, rather than in internal/api where it is used, for a reason that
// is not negotiable: //go:embed patterns cannot contain "..", so a package under
// internal/ simply cannot reach web/dist. Something at or above this directory has to
// declare it. A package here — rather than in main — keeps main.go at ten lines and
// lets internal/api take an fs.FS parameter, so its tests can drive a synthetic
// fstest.MapFS and exercise the fallback rules precisely, which is better than testing
// against whatever the real bundle happens to contain.
//
// The "all:" prefix is required, not decorative. A plain //go:embed dist walks the
// directory but skips every entry whose name begins with "." or "_", which would skip
// .gitkeep — and .gitkeep is the only thing standing between a fresh clone and
// "pattern dist: no matching files found" at compile time.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the built frontend, rooted so that index.html is at the top.
//
// On a checkout that has never run `npm run build`, this contains only .gitkeep and
// api.NewSPA falls back to its placeholder page. That is deliberate: `go build`,
// `go vet` and `go test ./...` all work without Node installed.
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Only possible if the embed directive above is wrong, which is a compile-time
		// concern rather than a runtime one.
		panic(err)
	}
	return sub
}
