// Package web serves the dashboard: a single-page app built from pier/web
// into dist/ and embedded in pierd, so the whole product is one binary.
package web

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the dashboard's files. Any path that isn't a file gets
// index.html, so links like /projects/blog work when opened directly.
func Handler() http.Handler {
	root, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if fi, err := fs.Stat(root, name); err == nil && !fi.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					// Vite names these by their content, so they never change.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := root.Open("index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, "The dashboard hasn't been built into this pierd. Run `make pier` (it builds pier/web first).\n")
			return
		}
		defer index.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-src http: https:; frame-ancestors 'none'; base-uri 'none'; form-action 'self' https://github.com/settings/apps/new")
		io.Copy(w, index)
	})
}
