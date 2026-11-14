package main

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

// The playground and the admin page are plain files served from the binary.
// There is no build step and nothing is loaded from another site.
//
//go:embed web
var webFiles embed.FS

var webTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
}

// handleWeb serves /ui/ (the playground), /ui/admin and their assets. The
// pages hold no secrets: every call they make carries the key the visitor
// typed in or was sent, kept in the browser only.
func handleWeb(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/ui")
	switch name {
	case "":
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
		return
	case "/":
		name = "/index.html"
	case "/admin", "/admin/":
		name = "/admin.html"
	}
	data, err := webFiles.ReadFile("web" + path.Clean(name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", webTypes[path.Ext(name)])
	// Program output is untrusted. Scripts may only come from this origin,
	// and the pages may not be framed or talk to anyone else.
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-cache")
	w.Write(data)
}
