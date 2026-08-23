// Package spa serves the built single-page application from an fs.FS (the
// SPA embedded in the binary) with history-mode fallback: any GET that does
// not map to an existing file and is not an API or internal route is served
// index.html so the client-side router can take over.
package spa

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// indexFile is the SPA entrypoint served for client-routed paths.
const indexFile = "index.html"

// Handler returns an http.Handler that serves static files from fsys, falling
// back to index.html for unknown paths (SPA history mode). Requests under the
// server-side route groups (/api, /auth, /avatar, /_) are never rewritten to
// index.html — if they reach here they 404 honestly rather than masquerade as
// the SPA shell.
func Handler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only page/asset reads are the SPA's business; other methods on
		// unrouted paths are honest 404s.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		// Clean the request path to prevent directory traversal. path.Clean on
		// an absolute-rooted path collapses ".." segments safely.
		upath := r.URL.Path
		if !strings.HasPrefix(upath, "/") {
			upath = "/" + upath
		}
		cleaned := path.Clean(upath)

		// API and internal routes must not fall back to the SPA shell.
		if isReservedPath(cleaned) {
			http.NotFound(w, r)
			return
		}

		// Map the cleaned URL path onto an fs.FS name. path.Clean already
		// collapsed any ".." against the leading "/", but fs.ValidPath
		// re-asserts containment (rooted, no ".."), so the lookup can never
		// escape fsys even if the cleaning above is later weakened.
		name := strings.TrimPrefix(cleaned, "/")
		if name == "" {
			name = "."
		}
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}

		if fileExists(fsys, name) {
			setCacheControl(w, cleaned)
			fileServer.ServeHTTP(w, r)
			return
		}

		// A missing path that LOOKS like a static asset (has a file extension)
		// must 404, not fall back to the SPA shell: returning index.html (200)
		// for a missing .js/.svg/.png hands HTML to <img>/fetch() consumers and
		// suppresses their error paths. Client routes are extensionless and
		// still fall through to index.html below.
		if path.Ext(cleaned) != "" {
			http.NotFound(w, r)
			return
		}

		// SPA fallback: serve index.html for client-side routes.
		setCacheControl(w, "/"+indexFile)
		http.ServeFileFS(w, r, fsys, indexFile)
	})
}

// setCacheControl picks the caching policy by path. Vite-fingerprinted files
// under /assets/ are content-addressed, so they never change and cache
// forever. Everything else (index.html, icons) keeps its name across deploys
// and must revalidate on every load; no-cache still allows storing —
// revalidation is a cheap 304 via Last-Modified/If-Modified-Since.
func setCacheControl(w http.ResponseWriter, cleaned string) {
	if strings.HasPrefix(cleaned, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// isReservedPath reports whether the path belongs to a server-side route
// group (API, auth, avatars, internal) that must never be served the SPA
// shell.
func isReservedPath(p string) bool {
	for _, prefix := range []string{"/api", "/auth", "/avatar", "/_"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// fileExists reports whether name is an existing regular file (not a
// directory). Directories fall through to the SPA fallback so that e.g.
// "/apps" does not accidentally serve a directory listing.
func fileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
