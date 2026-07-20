package cors

import (
	"net/http"
	"strings"
)

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
			allowOrigin := ""
			if allowAll && origin != "" {
				allowOrigin = origin
			} else if _, ok := allowed[origin]; ok {
				allowOrigin = origin
			}

			if allowOrigin != "" {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", allowOrigin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				h.Set("Access-Control-Max-Age", "3600")
				h.Add("Vary", "Origin")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
