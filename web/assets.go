// Package web embarque l'interface compilée (web/dist) dans le binaire.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler sert l'interface ; les chemins inconnus renvoient index.html (SPA).
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			p = "index.html"
		}
		if p == "manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		if p == "sw.js" {
			w.Header().Set("Service-Worker-Allowed", "/")
		}
		if p == "index.html" || p == "sw.js" || p == "manifest.webmanifest" || strings.HasPrefix(p, "icons/") {
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
