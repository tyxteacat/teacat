// Package webui serves the embedded TeaCat dashboard.
package webui

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

// Handler serves the dashboard for /, /ui and /dashboard. Everything else is a
// 404 so the API mux keeps ownership of its own paths.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html", "/ui", "/ui/", "/dashboard", "/dashboard/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(indexHTML)
		default:
			http.NotFound(w, r)
		}
	})
}
