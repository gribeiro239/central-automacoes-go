package httpapi

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"

	"central-automacoes/internal/webui"
)

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

func (a *API) panel(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	content, err := webui.Read("index.html")
	if err != nil {
		problem(w, 500, "painel indisponível")
		return
	}
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}

func (a *API) asset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\%") {
		http.NotFound(w, r)
		return
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			http.NotFound(w, r)
			return
		}
	}
	types := map[string]string{".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml", ".png": "image/png", ".webp": "image/webp", ".ico": "image/x-icon", ".woff2": "font/woff2"}
	mime, ok := types[path.Ext(name)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	content, err := webui.Read(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("Cache-Control", "no-cache")
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(content))
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}
