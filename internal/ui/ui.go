// Package ui serves the authoring tool's React app, which `make ui` builds
// from ui/ into dist/ and the binary embeds, so the binary is still the
// whole deploy. The app is static: it signs in through the server's /login
// page and does everything else through /api, as any other client does.
//
// A checkout that has not built the UI still compiles (dist/ keeps a
// placeholder), and serves a page saying how to build it.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

var files, _ = fs.Sub(dist, "dist")

// CSP lets the app run its own script and nothing else: no inline script,
// nothing from another origin. The notes it shows are text a model wrote.
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

func headers(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", CSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
}

// Index serves the app's page, for every route the app handles.
func Index(w http.ResponseWriter, _ *http.Request) {
	headers(w)
	w.Header().Set("Cache-Control", "no-cache")
	b, err := fs.ReadFile(files, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("The authoring tool's UI is not built. Run `make ui`, then rebuild the server.\n"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// Assets serves the app's built files under /assets/. Their names carry a
// hash of their contents, so they can be cached for good.
func Assets() http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		headers(w)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	})
}
