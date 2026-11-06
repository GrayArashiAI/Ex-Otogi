// Package web embeds the built control panel: dist/, written by the Vite
// build in this directory. Without a build it holds nothing, and the server
// says so.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Panel returns the built panel's files.
func Panel() fs.FS {
	sub, _ := fs.Sub(dist, "dist") // dist is embedded, so this cannot fail
	return sub
}
