// Package web embeds the built frontend (web/dist) into the binary.
// Run the frontend build before `go build`; see README.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built frontend rooted at its index.html, or ok=false if
// the frontend has not been built (dev mode: use the Vite proxy instead).
func Dist() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
