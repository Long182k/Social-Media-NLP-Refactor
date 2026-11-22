package httpserver

import (
	"net/http"
	"os"
	"path"
	"strings"
)

func NewSPAHandler(staticDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") ||
			staticDir == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
			return
		}

		rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if rel != "" && rel != "index.html" && serveStaticFile(w, r, staticDir, rel) {
			return
		}
		if !serveStaticFile(w, r, staticDir, "index.html") {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
		}
	})
}

func serveStaticFile(w http.ResponseWriter, r *http.Request, root, name string) bool {
	file, err := os.OpenInRoot(root, name)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	switch {
	case strings.HasPrefix(name, "assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case name == "index.html":
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
	return true
}
