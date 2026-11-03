package ui

import (
	_ "embed"
	"net/http"
)

//go:embed assets/index.html
var IndexHTML []byte

// Handler serves the embedded self-contained UI at GET /
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(IndexHTML)
	})
}
