// Package web embeds the built React frontend for single-binary serving.
//
// The dist/ directory is populated by `vite build` (see `make build`). A
// placeholder index.html is checked in so the embed compiles even before the
// frontend is built; in development the server proxies to Vite instead of
// serving these assets.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the built frontend as a filesystem rooted at dist/.
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
