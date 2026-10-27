// Package cors answers cross-origin browser requests from the
// CORS_ALLOW_ORIGINS list.
//
// Only an origin listed by name is trusted with the session: it gets
// Access-Control-Allow-Credentials and may send X-Winterflow-Request, the
// header the CSRF checks require. "*" admits every other origin to
// credential-less requests only ("Access-Control-Allow-Origin: *", no
// credentials, no X-Winterflow-Request), so a foreign page can neither read
// an answer made with the browser's cookie nor pass the CSRF check.
package cors

import (
	"net/http"
	"strings"
)

const (
	allowMethods = "GET,POST,OPTIONS"
	// publicHeaders are what any origin may send: a JSON body and an
	// explicit credential (a bearer token is not sent by the browser on
	// its own, so it needs no origin trust).
	publicHeaders = "Content-Type, Authorization"
	// trustedHeaders add the app-request header for listed origins.
	trustedHeaders = publicHeaders + ", X-Winterflow-Request"
)

// UseCORS is the middleware for origins, a comma-separated list of origins
// and/or "*".
func UseCORS(origins string) func(next http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	allowAll := false
	for _, origin := range strings.Split(origins, ",") {
		o := strings.TrimSpace(origin)
		if o == "" {
			continue
		}
		if o == "*" {
			allowAll = true
			continue
		}
		allowed[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			h := w.Header()
			if origin != "" {
				h.Add("Vary", "Origin")
				if _, ok := allowed[origin]; ok {
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Access-Control-Allow-Credentials", "true")
					h.Set("Access-Control-Allow-Methods", allowMethods)
					h.Set("Access-Control-Allow-Headers", trustedHeaders)
					h.Set("Access-Control-Max-Age", "3600")
				} else if allowAll {
					h.Set("Access-Control-Allow-Origin", "*")
					h.Set("Access-Control-Allow-Methods", allowMethods)
					h.Set("Access-Control-Allow-Headers", publicHeaders)
					h.Set("Access-Control-Max-Age", "3600")
				}
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
