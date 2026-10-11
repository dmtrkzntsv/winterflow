// Package api is the typed route table shared by the HTTP router and the
// OpenAPI generator: each handler package describes its endpoints as []Route,
// routes.go registers them with chi, and internal/app/web/api/openapi renders
// the very same slice into api/openapi.yaml. One table, so the served API and
// the documented one cannot drift.
package api

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Middleware is the chi middleware shape.
type Middleware = func(http.Handler) http.Handler

// Auth says which authentication a route requires.
type Auth int

const (
	// Public routes run without the auth middleware.
	Public Auth = iota
	// User routes run behind authMW (PAT bearer, else the JWT session).
	User
)

// Guards are the shared middlewares routes.go builds from live dependencies
// and hands to every handler table. Handler tables put them in
// Route.Middleware per route. The zero value (all nil) is only for spec
// generation: Register refuses a nil middleware.
//
// There is no role guard: every member of an organization may call every
// route. Authorization is authentication plus org ownership of the
// addressed server (webutil.RequireServerAccess, inside the handlers).
type Guards struct {
	// AuthLimit is the per-IP limiter for bcrypt-priced endpoints.
	AuthLimit Middleware
}

// Route describes one endpoint: how to serve it and how to document it.
type Route struct {
	Method  string           // http.MethodGet / http.MethodPost
	Path    string           // "/api/v1/app/save-app"
	Handler http.HandlerFunc // nil only when Mounted
	Auth    Auth
	// Middleware runs after authMW (when Auth is User), in order.
	Middleware []Middleware

	Summary string
	// Description is optional longer prose for the operation (CommonMark).
	Description string
	Tag         string // "apps", "docker", "server", "user", "org", "auth", "system"

	Query    any // struct with `query:"..."` tags, or nil
	Request  any // JSON body type, or nil
	Response any // `data` payload type of the 200 envelope, or nil (data is null)
	// Accepted routes answer 202 with data {request_id}; the agent's reply
	// arrives over SSE as an operation_result whose payload is Result.
	Accepted bool
	Result   any
	// Events marks a text/event-stream route; each event's data is Events.
	Events any
	// EventPayloads documents, per event type, the payload an Events value
	// carries. Events must then have "type" and "payload" JSON properties.
	EventPayloads []EventPayload
	// Raw marks a library route whose body is Response itself, not the
	// webutil envelope; its errors are the library's {"error": "..."}.
	Raw bool
	// ContentType is a Raw route's response media type (default JSON).
	ContentType string
	// Form is an application/x-www-form-urlencoded alternative to Request.
	Form any
	// Errors lists documented error statuses besides the automatic 401 on
	// User routes (e.g. 400, 403, 429).
	Errors []int
	// Mounted routes are served by a library mounted elsewhere (go-pkgz
	// /auth, /avatar). They are documented but Register skips them.
	Mounted bool
}

// EventPayload names the payload of one event type on an Events route.
type EventPayload struct {
	Type string // the event's "type" value, e.g. "server_status"
	// Payload is a value of the payload's named type. Nil means the payload
	// is the x-operation-result of the route that dispatched the event.
	Payload any
}

// Register adds every non-Mounted route to r. User routes get authMW first,
// then the route's own middleware. It panics on a nil handler or middleware:
// a guard that was never wired must stop the process at startup, not silently
// open the route.
func Register(r chi.Router, routes []Route, authMW Middleware) {
	for _, rt := range routes {
		if rt.Mounted {
			continue
		}
		if rt.Handler == nil {
			panic(fmt.Sprintf("api: %s %s has no handler", rt.Method, rt.Path))
		}
		var chain []Middleware
		if rt.Auth == User {
			if authMW == nil {
				panic(fmt.Sprintf("api: %s %s needs auth but authMW is nil", rt.Method, rt.Path))
			}
			chain = append(chain, authMW)
		}
		for i, mw := range rt.Middleware {
			if mw == nil {
				panic(fmt.Sprintf("api: %s %s middleware %d is nil", rt.Method, rt.Path, i))
			}
			chain = append(chain, mw)
		}
		if len(chain) == 0 {
			r.Method(rt.Method, rt.Path, rt.Handler)
			continue
		}
		r.With(chain...).Method(rt.Method, rt.Path, rt.Handler)
	}
}
