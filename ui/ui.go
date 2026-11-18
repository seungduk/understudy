// Package ui is understudy's web UI: static files built from ui/web (see
// ui/package.json) into ui/dist and embedded here. It reads the control API
// under /_understudy/ of the same address and keeps no state of its own.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Prefix is where understudy serve mounts the UI.
const Prefix = "/_understudy/ui/"

//go:embed dist
var dist embed.FS

// Files is the built UI: index.html, app.js, app.css and the icons.
func Files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Handler serves the UI under prefix (such as Prefix): GET and HEAD only, with
// index.html for the prefix itself. Every response is revalidated, so a new
// build shows at once.
func Handler(prefix string) http.Handler {
	files := Files()
	fsrv := http.StripPrefix(prefix, http.FileServerFS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "the UI is read with GET", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path+"/" == prefix {
			http.Redirect(w, r, prefix, http.StatusMovedPermanently)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), path.Clean(prefix))
		name = strings.TrimPrefix(name, "/")
		if name != "" && name != "index.html" {
			if _, err := fs.Stat(files, name); err != nil {
				http.Error(w, "no such UI file: "+name+"; the UI is at "+prefix, http.StatusNotFound)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		fsrv.ServeHTTP(w, r)
	})
}
