// Package cors answers cross-origin browser requests from the
// CORS_ALLOW_ORIGINS list.
//
// Only an origin listed by name is trusted with the session: it gets
// Access-Control-Allow-Credentials and may send X-Winterflow-Request, the
// header the CSRF checks require. "*" admits every other origin to
// credential-less requests only ("Access-Control-Allow-Origin: *", no
// credentials, no X-Winterflow-Request), so a foreign page can neither read
// an answer made with the browser's cookie nor pass the CSRF check.
//
// The MCP endpoint additionally admits the MCP transport's headers and
// exposes WWW-Authenticate (the OAuth discovery challenge a browser client
// reads off a 401) to the same origins: it authenticates a bearer token,
// so these headers need no more trust than Authorization does.
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
	// mcpPath is the MCP endpoint (webmcp.Path).
	mcpPath = "/mcp"
	// mcpHeaders are the MCP Streamable HTTP transport's request headers
	// (the endpoint is stateless, so a session id is never issued, but a
	// client may still send one).
	mcpHeaders = ", MCP-Protocol-Version, Mcp-Session-Id"
	// mcpExposed are the MCP answer's headers a browser client reads.
	mcpExposed = "WWW-Authenticate, Mcp-Session-Id"
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
				mcp := r.URL.Path == mcpPath
				admit := func(headers string) {
					h.Set("Access-Control-Allow-Methods", allowMethods)
					if mcp {
						headers += mcpHeaders
						h.Set("Access-Control-Expose-Headers", mcpExposed)
					}
					h.Set("Access-Control-Allow-Headers", headers)
					h.Set("Access-Control-Max-Age", "3600")
				}
				if _, ok := allowed[origin]; ok {
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Access-Control-Allow-Credentials", "true")
					admit(trustedHeaders)
				} else if allowAll {
					h.Set("Access-Control-Allow-Origin", "*")
					admit(publicHeaders)
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
