// Package web embeds the built frontend (web/dist). The compiled assets are
// committed so `go install` works without Node; rebuild them with
// `npm run build` in this directory after changing src/.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded frontend.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time; cannot fail
	}
	return http.FileServerFS(sub)
}
