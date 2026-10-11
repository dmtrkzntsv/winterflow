# CI PR 2 — Typed Route Table and OpenAPI 3.1 Generated From Code Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every HTTP route is described once, in a typed table next to its handler. The same table registers the routes with chi and generates the committed `api/openapi.yaml` (OpenAPI 3.1). Tests prove the two cannot drift and that the documented envelope matches the bytes on the wire. There is no behaviour change.

**Architecture:** A new dependency-free package `internal/app/web/api` defines `Route`, `Auth`, `Guards` and `Register`. Each handler package exports `(h *Handler) Routes(g api.Guards) []api.Route`, and the go-pkgz `/auth` and `/avatar` mounts are documented by `internal/app/web/auth.Routes()`. `routes.go` builds the guards (admin, auth rate limiter) and calls `api.Register` over the collected table. `APIRoutes()` returns the same table built over zero dependencies for documentation. A separate package `internal/app/web/api/openapi` uses `swaggest/openapi-go`'s `openapi31` reflector to render the table: it wraps every JSON response in the webutil envelope, adds the shared `Error` schema, a 202 `{request_id}` plus `x-operation-result` for agent-bound routes, `text/event-stream` with the `Notification` schema for SSE, and the `bearer`/`cookie` security schemes. `cmd/openapi` prints it, and `make openapi` writes `api/openapi.yaml`. A golden `chi.Walk` listing is captured before the refactor (Task 1) and must stay byte-identical. Tests run `kin-openapi` validation, check the document is up to date, and validate real `webutil` output against it.

**Tech Stack:** Go 1.25 (module `winterflow`), chi v5.2.5, `github.com/swaggest/openapi-go` v0.2.61 + `github.com/swaggest/jsonschema-go` v0.3.79 (generator), `github.com/getkin/kin-openapi` v0.149.0 (tests only).

**Spec:** `docs/superpowers/specs/2026-10-10-ci-and-api-contracts-design.md`, section "PR 2 — OpenAPI 3.1 generated from code" only. PR 1 (CI + coverage gates) is the base branch `ci/tests-and-coverage`. PR 3 (contract suite) gets its own plan and consumes `api/openapi.yaml`, `x-operation-result` and `api.Route.Mounted`.

**Branch:** create `ci/openapi` from the tip of `ci/tests-and-coverage` (`git switch -c ci/openapi ci/tests-and-coverage`). The diff gate for this stacked branch is `make diffcov DIFFCOV_BASE=ci/tests-and-coverage`.

**Verified at plan time:** all code in this plan was applied to a scratch copy of `ci/tests-and-coverage` (tip `1b180ff`). Results: compiled, `gofmt`-clean and `go vet`-clean; `go test -race ./...` green; `go mod tidy` stable. `tools/diffcov` on the change reported 546/552 changed Go lines covered (98.9%). The remaining uncovered lines are the SPA branch in `routes.go`, which is moved and only runs with a built `web/dist`, and one unreachable `ToSimpleMap` error branch. The generated document has 49 operations, 20 of them with `x-operation-result`, 87 component schemas and about 3.3k lines, and it passes `kin-openapi` `Validate`. Go coverage total went from 61.5 to 63.8.

**Resolved spec ambiguities (decided here):**
- `Routes` takes the shared guards: `func (h *Handler) Routes(g api.Guards) []api.Route` instead of `Routes()`. The admin guard and the auth rate limiter stay per-route `Middleware` entries (`g.Admin`, `g.AuthLimit`) without handler packages importing `rbac`/`ratelimit`. Removing roles in PR 4 is one line per table: `admin := []api.Middleware{g.Admin}` becomes `nil`, and `server`'s single route drops `g.Admin`.
- `api.Route` gets five fields beyond the spec's list, each needed to describe a real route: `Events` (SSE event payload type), `Raw` + `ContentType` (go-pkgz bodies that are not the webutil envelope), `Form` (form-encoded login body) and `Mounted` (named in the spec).
- `/_/health` and `GET /api/v1/sse` have Go handlers, so they are ordinary table routes, registered by `api.Register`, not `Mounted`. Only the go-pkgz endpoints are `Mounted`.
- The SPA fallback is chi's `NotFound` handler, not a route. It stays in `registerRoutes` and is not in the table or the document.
- The documented go-pkgz endpoints are: `POST /auth/local/login`, `GET /auth/logout`, `GET /auth/status`, `GET /auth/user`, `GET /auth/list` and `GET /avatar/{avatar}`. `/auth/google/*` exists only when Google OAuth is configured (distributed + client id/secret), so it is not listed.
- Security schemes: `bearer` is `type: http, scheme: bearer`. Its description says the token is a PAT (`wfp_…`), also accepted as the Basic-auth password. go-pkgz does not accept a JWT as `Authorization: Bearer`, so the scheme does not promise that. `cookie` is `apiKey` in cookie `JWT`, with the `X-JWT` header noted in the description. A User route lists both as alternatives.
- Errors: every User route gets a 401 automatically. It has two bodies: the `Error` JSON from `webutil.Unauthorized` and `text/plain` from go-pkgz's `http.Error(w, "Unauthorized", 401)`. Other statuses come from `Route.Errors`. 429 is `text/plain` (rate limiter `http.Error`). Errors on `Raw` routes use `LibraryError {"error": string}` (go-pkgz `rest.SendErrorJSON`). All webutil errors are 400 today, whatever the cause. The document says so and does not invent 404/409.
- `required` in schemas: only the envelope (`success`, `data`), `Error`, `AcceptedData`, `LibraryError`, and query parameters the handler rejects when empty (`required:"true"` tags) are required. Body structs get no `required`, because handlers decode missing fields as zero values. Shared components serve both requests and responses, so marking every non-`omitempty` field required would reject valid requests.
- Component names are the bare Go type name (`SaveAppRequest`, `App`, `Notification`), not swaggest's package-prefixed default. With the prefix, the app handler package (named `server`) would produce `ServerSaveAppRequest`. Two different Go types with one name make `Build` fail; they do not silently shadow each other. The go-pkgz `token.User` is documented as `auth.SessionUser` (`type SessionUser token.User`).
- `info.version` is the constant `"1"`, not the build version, so `TestOpenAPIUpToDate` does not change with every release. `operationId` is derived from the path (`/api/v1/app/save-app` → `appSaveApp`).
- `x-operation-result` is `{$ref: '#/components/schemas/<Result>'}` where `<Result>` is the agent's `command.*Response`. `codec.NotificationFromResponse` puts the agent's response payload verbatim into the SSE Notification's `payload`.

## Global Constraints

- Go module `winterflow`, `go 1.25.3` in `go.mod`, no `toolchain` line.
- New `require` lines, exactly: `github.com/swaggest/openapi-go v0.2.61` (MIT), `github.com/swaggest/jsonschema-go v0.3.79` (MIT), `github.com/getkin/kin-openapi v0.149.0` (MIT, imported only from `_test.go` files). Their new indirect modules: `github.com/swaggest/refl v1.4.0` (MIT), `gopkg.in/yaml.v2 v2.4.0` (Apache-2.0), `github.com/go-openapi/jsonpointer v0.22.5` and `github.com/go-openapi/swag/jsonname v0.25.5` (Apache-2.0), `github.com/oasdiff/yaml v0.1.1` (MIT), `github.com/oasdiff/yaml3 v0.0.14` (MIT/Apache-2.0), `github.com/santhosh-tekuri/jsonschema/v6 v6.0.3` (Apache-2.0). No GPL/AGPL module may enter `go.mod` (the O'Saasy license requires permissive deps).
- The server binaries must not link the generator: `go list -deps ./cmd/standalone ./cmd/api | grep -E 'swaggest|kin-openapi'` prints nothing. Only `cmd/openapi` and tests import `internal/app/web/api/openapi`.
- No behaviour change. `internal/app/web/testdata/routes.golden`, captured in Task 1, must be byte-identical at the end. Existing handler tests stay unchanged and green. Every response stays byte-identical: renamed structs keep their field order and JSON tags.
- Diff coverage: changed Go lines must be ≥80% covered per language (PR 1's gate; `cmd/**/main.go` is exempt). Each package's own tests must cover its own new lines, because CI runs `go test -coverprofile ./...` without `-coverpkg`. That is why every handler package gets its own `routes_test.go`.
- `gofmt -l $(git ls-files '*.go')` prints nothing; `go vet ./...` passes.
- No env vars are added, so `.env.example` / `web/.env.example` stay unchanged.
- Never touch `web/src/components/ui/**`.
- Commits: Conventional Commits, imperative mood. Each message ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.

## Review Focus

1. **A guard that was never wired** (nil `Guards.Admin` passed to `Register`, e.g. if someone registers `APIRoutes()`) must stop the process at startup. It must not serve an admin route unprotected. Pinned by `TestRegisterFailsClosed` (Task 2) and by `TestRoutes` in each handler package asserting exactly which routes carry the admin guard (Task 4).
2. **Nil slices and absent data on the wire**: `"data":null`, `"claims":null` from a nil `[]DomainClaim`, an empty token list encoded as `null`, and `message` omitted when empty. All must validate against the document, or PR 3's response validation rejects real traffic. Pinned by the `null data`, `null data, no message` cases of `TestEnvelopeSchemas` (Task 6) and the `success without data`, `success with empty message` and `nil list` cases of `TestOpenAPIMatchesWire` (Task 7).
3. **Non-JSON error bodies.** The go-pkgz JWT middleware answers 401 as `text/plain` "Unauthorized", and the rate limiter answers 429 as `text/plain`. A client or validator expecting the JSON envelope would break. Pinned by the `401 text`/`429 text` cases (Task 6) and `go-pkgz unauthorized`/`rate limited` cases (Task 7).
4. **Two Go types sharing a name** (e.g. a future HTTP `ControlAppRequest` next to `command.ControlAppRequest`, or a second `ServerQuery`). The first would silently define the component and the second would be documented with the wrong shape. `Build` must fail instead. Pinned by the `name clash` case of `TestBuildErrors` (Task 6).
5. **A stale or unstable document.** A handler's response struct changes but `make openapi` isn't run, or map ordering makes regeneration nondeterministic and the up-to-date check flaky. Pinned by `TestOpenAPIUpToDate` (Task 7, fails with "run make openapi") and `TestYAMLDeterministic` (Task 6).

---

## File Map

| Path | Action | Responsibility |
|---|---|---|
| `internal/app/web/routes.go` | modify (Task 1, Task 5) | Task 1: `health` becomes a named func. Task 5: guards + `api.Register(routeTable(...))`, `APIRoutes()`, `routeTable()` |
| `internal/app/web/routes_golden_test.go` | create (Task 1) | `testServer`, `funcID`, `walkRoutes`, `TestRoutesGolden` (`-update` flag) |
| `internal/app/web/testdata/routes.golden` | create (Task 1) | `chi.Walk` listing captured before the refactor |
| `internal/app/web/api/route.go` | create (Task 2) | `Middleware`, `Auth` (`Public`/`User`), `Guards`, `Route`, `Register` |
| `internal/app/web/api/query.go` | create (Task 2) | `ServerQuery` (shared `server_id` query) |
| `internal/app/web/api/route_test.go` | create (Task 2) | chain order, `Mounted` skip, fail-closed panics |
| `internal/app/web/handler/{app,server,user,org,auth}/*.go` | modify (Task 3) | export request/response types; replace anonymous response structs with named types (same bytes) |
| `internal/app/web/handler/app/check_domain_test.go` | create (Task 3) | `CheckDomain` success path |
| `internal/app/web/handler/server/handler_success_test.go` | create (Task 3) | `GetPublicKey` (capability + cert fallback), `Register` success |
| `internal/app/web/handler/*/routes.go` | create (Task 4) | each package's `Routes(g api.Guards) []api.Route` |
| `internal/app/web/handler/*/routes_test.go` | create (Task 4) | per-package table assertions (admin / validation / limiter placement) |
| `internal/app/web/auth/routes.go`, `routes_test.go` | create (Task 4) | `Routes()` documenting the go-pkgz mounts (`Mounted`, `Raw`) |
| `internal/app/web/routes_table_test.go` | create (Task 5) | `TestRouteTableMatchesRouter`, `TestHealth` |
| `internal/app/web/api/openapi/openapi.go` | create (Task 6) | `Build`, `YAML`, envelope/`Error`/`Null`/`AcceptedData`/`LibraryError`, `x-operation-result`, `operationID` |
| `internal/app/web/api/openapi/openapi_test.go` | create (Task 6) | generator tests over a synthetic table, validated with kin-openapi |
| `go.mod`, `go.sum` | modify (Task 6) | swaggest + kin-openapi |
| `cmd/openapi/main.go` | create (Task 7) | prints `openapi.YAML(web.APIRoutes())` |
| `api/openapi.yaml` | create (Task 7) | the committed, generated document |
| `internal/app/web/openapi_test.go` | create (Task 7) | `TestOpenAPIUpToDate`, `TestOpenAPIDocumentsEveryRoute`, `TestOpenAPIMatchesWire` |
| `Makefile` | modify (Task 7) | `openapi` target |
| `AGENTS.md`, `CLAUDE.md` | modify (Task 8) | document the route table and `make openapi` |
| `coverage-baseline.json` | modify (Task 8) | raise `go` to the measured total if the ratchet asks |

---

### Task 1: Golden route list, captured before the refactor

**Files:**
- Modify: `internal/app/web/routes.go` (the `/_/health` registration at the top of `registerRoutes`, plus a new func at the end of the file)
- Create: `internal/app/web/routes_golden_test.go`
- Create: `internal/app/web/testdata/routes.golden` (generated)

**Interfaces:**
- Consumes: nothing (first task).
- Produces (package `web`, test-only, used by Tasks 5 and 7):
  - `func testServer(t *testing.T) *chi.Mux` builds the real router via `NewServer` over zero-value deps.
  - `func funcID(f any) string` gives a stable identity for a handler/middleware. Symbol names are used for named funcs and method values; closures become `closure@<pkgdir>/<file>:<line>`.
  - `func walkRoutes(t *testing.T, r chi.Routes) []string` returns sorted `"METHOD PATH handler <- mw1, mw2, ..."` lines.
  - `TestRoutesGolden` with flag `-update`.
- Produces (package `web`): `func health(w http.ResponseWriter, _ *http.Request)`.

Why the health extraction comes first: today `/_/health` is an inline closure. Its symbol name is `web.(*Server).registerRoutes.func1`, which would change the moment the code moves. Naming it before the golden is captured keeps the golden invariant through Task 5. The other closures (patauth, rbac, cors, logger, chi's Mount) are identified by the file:line of their literal, because the compiler names an inlined closure after its caller (e.g. `registerRoutes.Middleware.func2`), and that name changes when the construction moves.

- [ ] **Step 1: Name the health handler (pure move, same bytes)**

In `internal/app/web/routes.go`, replace:

```go
	s.Router.Get("/_/health", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "healthy", nil)
	})
```

with:

```go
	s.Router.Get("/_/health", health)
```

and append at the end of the file:

```go

// health is the liveness probe: always 200 {"success":true,"message":"healthy"}.
func health(w http.ResponseWriter, _ *http.Request) {
	util.Success(w, "healthy", nil)
}
```

- [ ] **Step 2: Write the golden test**

Create `internal/app/web/routes_golden_test.go`:

```go
package web

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/routes.golden")

// testServer builds the real router with zero-value dependencies. Nothing is
// called at construction time, so nil services are fine for walking routes.
func testServer(t *testing.T) *chi.Mux {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("WEB_URL", "http://localhost")
	t.Setenv("AVATARS_STORAGE_PATH", t.TempDir())
	deps := &bootstrap.Deps{
		Log: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"}),
		Cfg: config.NewServerConfig("standalone"),
	}
	srv := NewServer(context.Background(), deps)
	return srv.Handler.(*chi.Mux)
}

// funcID names a handler or middleware stably. Named functions and method
// values keep their symbol name. Closures are named by where their literal
// lives (package dir/file:line): the compiler names an inlined closure after
// its caller (e.g. "registerRoutes.Middleware.func2"), which would change as
// soon as the construction moves, even though the middleware is the same.
func funcID(f any) string {
	pc := reflect.ValueOf(f).Pointer()
	fn := runtime.FuncForPC(pc)
	name := fn.Name()
	if !closureName.MatchString(name) {
		return name
	}
	file, line := fn.FileLine(pc)
	return fmt.Sprintf("closure@%s/%s:%d", filepath.Base(filepath.Dir(file)), filepath.Base(file), line)
}

var closureName = regexp.MustCompile(`\.func\d+(\.\d+)?$`)

// walkRoutes renders every registered route as
// "METHOD PATH handler <- mw1, mw2, ..." (global middleware included), sorted.
func walkRoutes(t *testing.T, r chi.Routes) []string {
	t.Helper()
	var out []string
	err := chi.Walk(r, func(method, route string, h http.Handler, mws ...func(http.Handler) http.Handler) error {
		names := make([]string, 0, len(mws))
		for _, mw := range mws {
			names = append(names, funcID(mw))
		}
		out = append(out, method+" "+route+" "+funcID(h)+" <- "+strings.Join(names, ", "))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(out)
	return out
}

func TestRoutesGolden(t *testing.T) {
	got := strings.Join(walkRoutes(t, testServer(t)), "\n") + "\n"
	path := filepath.Join("testdata", "routes.golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run go test ./internal/app/web/ -run TestRoutesGolden -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("registered routes changed.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
```

- [ ] **Step 3: Run it to see it fail (no golden yet)**

Run: `go test ./internal/app/web/ -run TestRoutesGolden`
Expected: FAIL with `read golden (run go test ./internal/app/web/ -run TestRoutesGolden -update): open testdata/routes.golden: no such file or directory`.

- [ ] **Step 4: Capture the golden from the unrefactored router**

```bash
mkdir -p internal/app/web/testdata
go test ./internal/app/web/ -run TestRoutesGolden -update
go test ./internal/app/web/ -run TestRoutesGolden
wc -l internal/app/web/testdata/routes.golden
grep -E '^(GET /_/health|GET /api/v1/org/get-members|POST /api/v1/auth/register|GET /auth/\*) ' internal/app/web/testdata/routes.golden
```

Expected: both runs `ok`. `wc -l` prints `61`: 43 API routes plus 9 methods × 2 mounts. The grep prints exactly these four lines:

```
GET /_/health winterflow/internal/app/web.health <- closure@logger/logger.go:9, github.com/go-chi/chi/v5/middleware.RequestID, github.com/go-chi/chi/v5/middleware.RealIP, closure@cors/cors.go:23, github.com/go-chi/chi/v5/middleware.Recoverer
GET /api/v1/org/get-members winterflow/internal/app/web/handler/org.(*Handler).GetMembers-fm <- closure@logger/logger.go:9, github.com/go-chi/chi/v5/middleware.RequestID, github.com/go-chi/chi/v5/middleware.RealIP, closure@cors/cors.go:23, github.com/go-chi/chi/v5/middleware.Recoverer, closure@patauth/patauth.go:29, closure@rbac/rbac.go:23
POST /api/v1/auth/register winterflow/internal/app/web/handler/auth.(*Handler).Register-fm <- closure@logger/logger.go:9, github.com/go-chi/chi/v5/middleware.RequestID, github.com/go-chi/chi/v5/middleware.RealIP, closure@cors/cors.go:23, github.com/go-chi/chi/v5/middleware.Recoverer, winterflow/internal/app/web/middleware/ratelimit.(*Limiter).Middleware-fm
GET /auth/* closure@v5@v5.2.5/mux.go:309 <- closure@logger/logger.go:9, github.com/go-chi/chi/v5/middleware.RequestID, github.com/go-chi/chi/v5/middleware.RealIP, closure@cors/cors.go:23, github.com/go-chi/chi/v5/middleware.Recoverer, winterflow/internal/app/web/middleware/ratelimit.(*Limiter).Middleware-fm
```

If a line differs only in a closure's line number, a middleware file differs from the plan-time tree. Accept the captured file: it is the "before" state by definition. **From here on, never run `-update` again in this PR.**

- [ ] **Step 5: Commit**

```bash
git add internal/app/web/routes.go internal/app/web/routes_golden_test.go internal/app/web/testdata/routes.golden
git commit -m "$(printf 'test(web): pin registered routes and middleware in a golden file\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 2: `internal/app/web/api` — the route table type and `Register`

**Files:**
- Create: `internal/app/web/api/route.go`
- Create: `internal/app/web/api/query.go`
- Test: `internal/app/web/api/route_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (package `api`, imported by every handler package, `routes.go` and the generator):
  - `type Middleware = func(http.Handler) http.Handler`
  - `type Auth int`; `const ( Public Auth = iota; User )`
  - `type Guards struct { Admin Middleware; AuthLimit Middleware }`
  - `type Route struct { Method, Path string; Handler http.HandlerFunc; Auth Auth; Middleware []Middleware; Summary, Tag string; Query, Request, Response any; Accepted bool; Result, Events any; Raw bool; ContentType string; Form any; Errors []int; Mounted bool }`
  - `func Register(r chi.Router, routes []Route, authMW Middleware)`: skips `Mounted`. User routes run `authMW` first, then `Middleware` in order. It panics on a nil handler, a nil `authMW` for a User route, or a nil middleware entry. A route with no middleware is registered with `r.Method` directly, which keeps chi's walk output identical to today's `Router.Get`.
  - `type ServerQuery struct { ServerID string \`query:"server_id" required:"true" ...\` }`

- [ ] **Step 1: Write the failing tests**

Create `internal/app/web/api/route_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// tag returns a middleware that appends name to the X-Chain header, so a
// request through the router reveals the order middleware ran in.
func tag(name string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("X-Chain", name)
			next.ServeHTTP(w, r)
		})
	}
}

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }

func serve(t *testing.T, r http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestRegisterChains(t *testing.T) {
	r := chi.NewRouter()
	Register(r, []Route{
		{Method: http.MethodGet, Path: "/public", Handler: ok},
		{Method: http.MethodPost, Path: "/limited", Handler: ok, Middleware: []Middleware{tag("limit")}},
		{Method: http.MethodGet, Path: "/user", Handler: ok, Auth: User},
		{Method: http.MethodPost, Path: "/admin", Handler: ok, Auth: User, Middleware: []Middleware{tag("admin"), tag("extra")}},
		{Method: http.MethodGet, Path: "/auth/user", Auth: Public, Mounted: true},
	}, tag("auth"))

	cases := []struct {
		method, path, chain string
		status              int
	}{
		{http.MethodGet, "/public", "", http.StatusTeapot},
		{http.MethodPost, "/limited", "limit", http.StatusTeapot},
		{http.MethodGet, "/user", "auth", http.StatusTeapot},
		{http.MethodPost, "/admin", "auth,admin,extra", http.StatusTeapot},
		{http.MethodPost, "/public", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "/auth/user", "", http.StatusNotFound}, // Mounted: not registered
	}
	for _, tc := range cases {
		rec := serve(t, r, tc.method, tc.path)
		if rec.Code != tc.status {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.path, rec.Code, tc.status)
		}
		if got := strings.Join(rec.Header().Values("X-Chain"), ","); got != tc.chain {
			t.Errorf("%s %s: chain %q, want %q", tc.method, tc.path, got, tc.chain)
		}
	}
}

func TestRegisterFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		route  Route
		authMW Middleware
		want   string
	}{
		{"nil handler", Route{Method: http.MethodGet, Path: "/a"}, tag("auth"), "has no handler"},
		{"nil authMW", Route{Method: http.MethodGet, Path: "/a", Handler: ok, Auth: User}, nil, "authMW is nil"},
		{"nil guard", Route{Method: http.MethodGet, Path: "/a", Handler: ok, Auth: User, Middleware: []Middleware{nil}}, tag("auth"), "middleware 0 is nil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p := recover()
				if p == nil || !strings.Contains(p.(string), tc.want) {
					t.Fatalf("panic = %v, want it to contain %q", p, tc.want)
				}
			}()
			Register(chi.NewRouter(), []Route{tc.route}, tc.authMW)
		})
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/web/api/`
Expected: FAIL to build with `undefined: Middleware`, `undefined: Register`, `undefined: Route`, `undefined: User`, `undefined: Public`.

- [ ] **Step 3: Implement the package**

Create `internal/app/web/api/route.go`:

```go
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
type Guards struct {
	// Admin restricts a route to org owners/admins (rbac.RequireAdmin).
	Admin Middleware
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
	Tag     string // "apps", "docker", "server", "user", "org", "auth", "system"

	Query    any // struct with `query:"..."` tags, or nil
	Request  any // JSON body type, or nil
	Response any // `data` payload type of the 200 envelope, or nil (data is null)
	// Accepted routes answer 202 with data {request_id}; the agent's reply
	// arrives over SSE as an operation_result whose payload is Result.
	Accepted bool
	Result   any
	// Events marks a text/event-stream route; each event's data is Events.
	Events any
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
```

Create `internal/app/web/api/query.go`:

```go
package api

// ServerQuery is the query of every route addressed to one server: the
// handler 400s without server_id and 403s unless the caller's org owns it.
type ServerQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Target server id."`
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/app/web/api/ && go vet ./internal/app/web/api/`
Expected: `ok  	winterflow/internal/app/web/api`, vet silent.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web/api
git commit -m "$(printf 'feat(web): add the typed api route table and fail-closed Register\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 3: Export handler request/response types (same bytes on the wire)

The generator reflects Go types, so every body and every `data` payload must be a named, exported type. This task renames unexported request structs and turns anonymous response structs into named ones. Field order and JSON tags stay identical, so every response stays byte-identical. It also adds tests for three touched success paths that no existing test runs, so the changed lines are covered.

**Files:**
- Modify: `internal/app/web/handler/app/save_app.go`, `lifecycle.go`, `get_apps.go`, `get_apps_status.go`, `refresh_apps.go`, `check_domain.go`
- Modify: `internal/app/web/handler/server/get_servers.go`, `get_servers_status.go`, `get_public_key.go`, `register.go`
- Modify: `internal/app/web/handler/user/handler.go`, `internal/app/web/handler/org/handler.go`, `internal/app/web/handler/auth/handler.go`
- Test: `internal/app/web/handler/app/check_domain_test.go`, `internal/app/web/handler/server/handler_success_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (exported names Task 4's tables reference; rename map, old → new):
  - `handler/app` (Go package name `server`): `saveAppRequest`→`SaveAppRequest`, `contentItemRequest`→`ContentItemRequest`, `sourceRequest`→`SourceRequest`, `controlAppRequest`→`ControlAppRequest`, `deleteAppRequest`→`DeleteAppRequest`, `renameAppRequest`→`RenameAppRequest`, `rollbackAppRequest`→`RollbackAppRequest`. New: `AppWithDomains` (was the func-local `appWithDomains`), `GetAppsResponse`, `CheckDomainResponse`. `GetAppsStatus` now writes `command.GetAppsStatusResponse` (same shape as its anonymous struct). `SaveApp`/`RefreshApps` use the package's existing `accepted()` helper (same `{request_id}` bytes).
  - `handler/server`: `registerServerRequest`→`RegisterServerRequest`, `publicKeyResponse`→`PublicKeyResponse`. New: `RegisterServerResponse`, `GetServersResponse`, `GetServersStatusResponse`. `serverStatusDTO` is deleted in favour of the identical `status.ServerStatus` (`{server_id, liveness}`).
  - `handler/user`: `changePasswordRequest`→`ChangePasswordRequest`, `createTokenRequest`→`CreateTokenRequest`, `deleteTokenRequest`→`DeleteTokenRequest`. New: `Profile`, `CreateTokenResponse`.
  - `handler/org`: `createUserRequest`→`CreateUserRequest`, `updateMemberRequest`→`UpdateMemberRequest`, `memberRequest`→`MemberRequest`, `updateOrganizationRequest`→`UpdateOrganizationRequest`. New: `CreateUserResponse`, `ResetMemberPasswordResponse`.
  - `handler/auth`: `registerRequest`→`RegisterRequest`. New: `RegisterResponse`, `StateResponse`.
  - Unchanged (already exported or domain types used directly): `command.*Request`/`*Response`, `model.Member`, `model.UserToken`, `model.Organization`, `model.Notification`.

- [ ] **Step 1: Write the failing tests**

Create `internal/app/web/handler/app/check_domain_test.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckDomainAvailable(t *testing.T) {
	h, _, _ := newHandler(t)
	w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?domain=app.example.com", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Data CheckDomainResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Data.Available || len(body.Data.Claims) != 0 {
		t.Fatalf("data = %+v, want available with no claims", body.Data)
	}
}
```

Create `internal/app/web/handler/server/handler_success_test.go`:

```go
package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
)

// publicKeyOf calls GetPublicKey for serverID and returns data.public_key.
func publicKeyOf(t *testing.T, h *Handler, userID, serverID string) string {
	t.Helper()
	r := authedAs(httptest.NewRequest("GET", "/api/v1/server/get-public-key?server_id="+serverID, nil), userID)
	w := httptest.NewRecorder()
	h.GetPublicKey(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Data PublicKeyResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data.PublicKey
}

func TestGetPublicKeyFromCapability(t *testing.T) {
	h, userID, serverID, _ := newEnv(t)
	if err := h.servers.SaveCapabilities(context.Background(), serverID, map[string]string{"public_key": "BASE64POINT"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := publicKeyOf(t, h, userID, serverID); got != "BASE64POINT" {
		t.Fatalf("public_key = %q, want the capability value", got)
	}
}

func TestGetPublicKeyFallsBackToAgentCert(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HUB_CERT_DIR", dir)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "winterflow-agent"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "agent.crt"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	h, userID, serverID, _ := newEnv(t)
	if got := publicKeyOf(t, h, userID, serverID); got == "" {
		t.Fatal("public_key is empty; want the agent certificate's point")
	}
}

func TestRegisterClaimsPendingServer(t *testing.T) {
	h, userID, _, _ := newEnv(t)
	err := h.servers.RegisterServer(context.Background(), dto.ServerRegistrationDTO{
		ServerID:             "srv-2",
		CertificateID:        "cert-2",
		Hostname:             "box2",
		Code:                 "654321",
		ExpiresAt:            time.Now().Add(time.Hour),
		Certificate:          []byte("PEM"),
		CertificateExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	r := authedAs(httptest.NewRequest("POST", "/api/v1/server/register", strings.NewReader(`{"code":"654321"}`)), userID)
	w := httptest.NewRecorder()
	h.Register(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Data RegisterServerResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Server.ID != "srv-2" {
		t.Fatalf("server = %+v, want srv-2", body.Data.Server)
	}
}
```

(`newEnv`, `authedAs`, `newHandler`, `do` and `authed` already exist in the packages' `handler_test.go`. `newEnv` saves only a `server_ip` capability, so the cert test exercises the fallback.)

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/web/handler/app/ ./internal/app/web/handler/server/`
Expected: FAIL to build with `undefined: CheckDomainResponse`, `undefined: PublicKeyResponse`, `undefined: RegisterServerResponse`.

- [ ] **Step 3: Export the app handler types**

Replace `internal/app/web/handler/app/get_apps.go` with:

```go
package server

import (
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

const serverIDKey = "server_id"

// AppWithDomains is one app in the get-apps listing: the catalog record plus
// the hostnames its ingress claims (omitted when it claims none).
type AppWithDomains struct {
	model.App
	Domains []model.AppDomainInfo `json:"domains,omitempty"`
}

// GetAppsResponse is the data of GET /api/v1/app/get-apps.
type GetAppsResponse struct {
	Apps []AppWithDomains `json:"apps"`
}

func (h *Handler) GetApps(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get(serverIDKey)
	if _, ok := h.authorize(w, r, serverID); !ok {
		return
	}

	apps, err := h.usecase.GetApps(r.Context(), serverID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	domains, err := h.usecase.ListDomains(r.Context(), serverID)
	if err != nil {
		// Chips are decoration; the listing must not fail over the index.
		// Still worth a log line so a stale/broken app_domains index doesn't
		// go unnoticed.
		if h.log != nil {
			h.log.Warn("GetApps: list domains", "error", err, "server_id", serverID)
		}
		domains = nil
	}
	out := make([]AppWithDomains, 0, len(apps))
	for _, a := range apps {
		out = append(out, AppWithDomains{App: a, Domains: domains[a.ID]})
	}
	util.Success(w, "", GetAppsResponse{Apps: out})
}

func GetAppsValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverID := r.URL.Query().Get(serverIDKey)
		if serverID == "" {
			util.Error(w, "serverID is required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

In `internal/app/web/handler/app/get_apps_status.go`, replace:

```go
	util.Success(w, "ok", struct {
		Apps []command.AppStatus `json:"apps"`
	}{Apps: statuses})
```

with:

```go
	util.Success(w, "ok", command.GetAppsStatusResponse{Apps: statuses})
```

In `internal/app/web/handler/app/refresh_apps.go`, replace:

```go
	webutil.Accepted(w, "apps refresh dispatched", struct {
		RequestID string `json:"request_id"`
	}{RequestID: requestID})
```

with:

```go
	accepted(w, "apps refresh dispatched", requestID)
```

In `internal/app/web/handler/app/check_domain.go`, replace:

```go
// CheckDomain answers
```

with:

```go
// CheckDomainResponse is the data of GET /api/v1/domains/check. Claims lists
// the apps already using the hostname (null when there are none).
type CheckDomainResponse struct {
	Available bool                `json:"available"`
	Claims    []model.DomainClaim `json:"claims"`
}

// CheckDomain answers
```

and replace:

```go
	webutil.Success(w, "", struct {
		Available bool                `json:"available"`
		Claims    []model.DomainClaim `json:"claims"`
	}{Available: len(claims) == 0, Claims: claims})
```

with:

```go
	webutil.Success(w, "", CheckDomainResponse{Available: len(claims) == 0, Claims: claims})
```

In `internal/app/web/handler/app/save_app.go`, make these replacements (each `old` → `new`):

1. `// contentItemRequest is the browser's form` → `// ContentItemRequest is the browser's form`
2. `type contentItemRequest struct` → `type ContentItemRequest struct`
3. `// sourceRequest configures a git-sourced app.` → `// SourceRequest configures a git-sourced app.`
4. `type sourceRequest struct` → `type SourceRequest struct`
5. `type saveAppRequest struct` →
   ```go
   // SaveAppRequest is the body of POST /api/v1/app/save-app.
   type SaveAppRequest struct
   ```
6. The three typed fields:
   ```go
   	Files     []contentItemRequest `json:"files"`
   	Variables []contentItemRequest `json:"variables"`
   	Source    *sourceRequest       `json:"source,omitempty"`
   ```
   →
   ```go
   	Files     []ContentItemRequest `json:"files"`
   	Variables []ContentItemRequest `json:"variables"`
   	Source    *SourceRequest       `json:"source,omitempty"`
   ```
7. `func toContentItems(items []contentItemRequest)` → `func toContentItems(items []ContentItemRequest)`
8. `webutil.DecodeBody[saveAppRequest]` → `webutil.DecodeBody[SaveAppRequest]`
9. ```go
   	webutil.Accepted(w, "app save dispatched", struct {
   		RequestID string `json:"request_id"`
   	}{RequestID: requestID})
   ```
   →
   ```go
   	accepted(w, "app save dispatched", requestID)
   ```

In `internal/app/web/handler/app/lifecycle.go`, make these replacements:

1. `type controlAppRequest struct` →
   ```go
   // ControlAppRequest is the body of POST /api/v1/app/control-app.
   type ControlAppRequest struct
   ```
2. `DecodeBody[controlAppRequest]` → `DecodeBody[ControlAppRequest]`
3. `type deleteAppRequest struct` →
   ```go
   // DeleteAppRequest is the body of POST /api/v1/app/delete-app.
   type DeleteAppRequest struct
   ```
4. `DecodeBody[deleteAppRequest]` → `DecodeBody[DeleteAppRequest]`
5. `type renameAppRequest struct` →
   ```go
   // RenameAppRequest is the body of POST /api/v1/app/rename-app.
   type RenameAppRequest struct
   ```
6. `DecodeBody[renameAppRequest]` → `DecodeBody[RenameAppRequest]`
7. `type rollbackAppRequest struct` →
   ```go
   // RollbackAppRequest is the body of POST /api/v1/app/rollback-app.
   type RollbackAppRequest struct
   ```
8. `DecodeBody[rollbackAppRequest]` → `DecodeBody[RollbackAppRequest]`

- [ ] **Step 4: Export the server handler types**

Replace `internal/app/web/handler/server/get_servers.go` with:

```go
package server

import (
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// GetServersResponse is the data of GET /api/v1/server/get-servers.
type GetServersResponse struct {
	Servers []model.Server `json:"servers"`
}

func (h *Handler) GetServers(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}

	servers, err := h.servers.GetServers(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	util.Success(w, "ok", GetServersResponse{Servers: servers})
}
```

Replace `internal/app/web/handler/server/get_servers_status.go` with:

```go
package server

import (
	"net/http"
	"time"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/service/status"
)

// GetServersStatusResponse is the data of GET /api/v1/server/get-servers-status.
type GetServersStatusResponse struct {
	Servers []status.ServerStatus `json:"servers"`
}

// GetServersStatus returns live liveness (from the in-memory TTL cache) for the
// user's servers. Status is never persisted; absence/expiry reads as "unknown".
func (h *Handler) GetServersStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}

	servers, err := h.servers.GetServers(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}

	now := time.Now()
	out := make([]status.ServerStatus, 0, len(servers))
	for _, s := range servers {
		out = append(out, status.ServerStatus{
			ServerID: s.ID,
			Liveness: h.status.ServerLiveness(s.ID, now),
		})
	}
	util.Success(w, "ok", GetServersStatusResponse{Servers: out})
}
```

(`status.ServerStatus` is `{ServerID string json:"server_id"; Liveness Liveness json:"liveness"}`, the same fields and tags as the deleted `serverStatusDTO`.)

In `internal/app/web/handler/server/get_public_key.go`:

1. Both `util.Success(w, "ok", publicKeyResponse{PublicKey: value})` and `util.Success(w, "ok", publicKeyResponse{PublicKey: point})` → `PublicKeyResponse{...}` (same arguments).
2. `type publicKeyResponse struct` →
   ```go
   // PublicKeyResponse is the data of GET /api/v1/server/get-public-key: the
   // agent's P-256 public point, base64.
   type PublicKeyResponse struct
   ```

In `internal/app/web/handler/server/register.go`:

1. `type registerServerRequest struct` →
   ```go
   // RegisterServerRequest is the body of POST /api/v1/server/register.
   type RegisterServerRequest struct
   ```
2. `var req registerServerRequest` → `var req RegisterServerRequest`
3. `// Register claims a pending server registration` →
   ```go
   // RegisterServerResponse is the data of POST /api/v1/server/register.
   type RegisterServerResponse struct {
   	Server model.Server `json:"server"`
   }

   // Register claims a pending server registration
   ```
4. ```go
   	util.Success(w, "server added", struct {
   		Server model.Server `json:"server"`
   	}{Server: srv})
   ```
   →
   ```go
   	util.Success(w, "server added", RegisterServerResponse{Server: srv})
   ```

- [ ] **Step 5: Export the user, org and auth handler types**

In `internal/app/web/handler/user/handler.go`:

1. `// GetProfile returns the caller's identity` →
   ```go
   // Profile is the data of GET /api/v1/user/get-profile. Email and
   // must_change_password are empty/false for accounts without local credentials.
   type Profile struct {
   	UserID             string `json:"user_id"`
   	Name               string `json:"name"`
   	Email              string `json:"email"`
   	Role               string `json:"role"`
   	MustChangePassword bool   `json:"must_change_password"`
   }

   // GetProfile returns the caller's identity
   ```
2. ```go
   	profile := struct {
   		UserID             string `json:"user_id"`
   		Name               string `json:"name"`
   		Email              string `json:"email"`
   		Role               string `json:"role"`
   		MustChangePassword bool   `json:"must_change_password"`
   	}{UserID: u.ID, Name: u.Name, Role: role}
   ```
   →
   ```go
   	profile := Profile{UserID: u.ID, Name: u.Name, Role: role}
   ```
3. `type changePasswordRequest struct` →
   ```go
   // ChangePasswordRequest is the body of POST /api/v1/user/change-password.
   type ChangePasswordRequest struct
   ```
4. `DecodeBody[changePasswordRequest]` → `DecodeBody[ChangePasswordRequest]`
5. `type createTokenRequest struct` →
   ```go
   // CreateTokenRequest is the body of POST /api/v1/user/create-token.
   // expires_in_days <= 0 means the token never expires.
   type CreateTokenRequest struct
   ```
6. `DecodeBody[createTokenRequest]` → `DecodeBody[CreateTokenRequest]`
7. `// CreateToken mints a PAT.` →
   ```go
   // CreateTokenResponse is the data of POST /api/v1/user/create-token. Token is
   // the plaintext PAT, returned only here.
   type CreateTokenResponse struct {
   	TokenID   string     `json:"token_id"`
   	Token     string     `json:"token"`
   	Prefix    string     `json:"prefix"`
   	Name      string     `json:"name"`
   	ExpiresAt *time.Time `json:"expires_at"`
   }

   // CreateToken mints a PAT.
   ```
8. ```go
   	webutil.Success(w, "token created", struct {
   		TokenID   string     `json:"token_id"`
   		Token     string     `json:"token"`
   		Prefix    string     `json:"prefix"`
   		Name      string     `json:"name"`
   		ExpiresAt *time.Time `json:"expires_at"`
   	}{rec.ID, plaintext, rec.Prefix, rec.Name, rec.ExpiresAt})
   ```
   →
   ```go
   	webutil.Success(w, "token created", CreateTokenResponse{
   		TokenID:   rec.ID,
   		Token:     plaintext,
   		Prefix:    rec.Prefix,
   		Name:      rec.Name,
   		ExpiresAt: rec.ExpiresAt,
   	})
   ```
9. `type deleteTokenRequest struct` →
   ```go
   // DeleteTokenRequest is the body of POST /api/v1/user/delete-token.
   type DeleteTokenRequest struct
   ```
10. `DecodeBody[deleteTokenRequest]` → `DecodeBody[DeleteTokenRequest]`

In `internal/app/web/handler/org/handler.go`:

1. `type createUserRequest struct` →
   ```go
   // CreateUserRequest is the body of POST /api/v1/org/create-user. Role is
   // "admin" or "member".
   type CreateUserRequest struct
   ```
2. `DecodeBody[createUserRequest]` → `DecodeBody[CreateUserRequest]`
3. `// CreateUser provisions an account` →
   ```go
   // CreateUserResponse is the data of POST /api/v1/org/create-user.
   // TempPassword is shown only here.
   type CreateUserResponse struct {
   	UserID       string `json:"user_id"`
   	Name         string `json:"name"`
   	Email        string `json:"email"`
   	Role         string `json:"role"`
   	TempPassword string `json:"temp_password"`
   }

   // CreateUser provisions an account
   ```
4. ```go
   	webutil.Success(w, "user created", struct {
   		UserID       string `json:"user_id"`
   		Name         string `json:"name"`
   		Email        string `json:"email"`
   		Role         string `json:"role"`
   		TempPassword string `json:"temp_password"`
   	}{user.ID, user.Name, email, req.Role, tempPassword})
   ```
   →
   ```go
   	webutil.Success(w, "user created", CreateUserResponse{
   		UserID:       user.ID,
   		Name:         user.Name,
   		Email:        email,
   		Role:         req.Role,
   		TempPassword: tempPassword,
   	})
   ```
5. `type updateMemberRequest struct` →
   ```go
   // UpdateMemberRequest is the body of POST /api/v1/org/update-member.
   type UpdateMemberRequest struct
   ```
6. `DecodeBody[updateMemberRequest]` → `DecodeBody[UpdateMemberRequest]`
7. `type memberRequest struct` →
   ```go
   // MemberRequest names one member: the body of remove-member and
   // reset-member-password.
   type MemberRequest struct
   ```
8. Both occurrences of `webutil.DecodeBody[memberRequest](w, r)` → `webutil.DecodeBody[MemberRequest](w, r)` (Edit with `replace_all: true`).
9. `// ResetMemberPassword issues a new temp password` →
   ```go
   // ResetMemberPasswordResponse is the data of POST
   // /api/v1/org/reset-member-password. TempPassword is shown only here.
   type ResetMemberPasswordResponse struct {
   	UserID       string `json:"user_id"`
   	TempPassword string `json:"temp_password"`
   }

   // ResetMemberPassword issues a new temp password
   ```
10. ```go
    	webutil.Success(w, "password reset", struct {
    		UserID       string `json:"user_id"`
    		TempPassword string `json:"temp_password"`
    	}{req.UserID, tempPassword})
    ```
    →
    ```go
    	webutil.Success(w, "password reset", ResetMemberPasswordResponse{UserID: req.UserID, TempPassword: tempPassword})
    ```
11. `type updateOrganizationRequest struct` →
    ```go
    // UpdateOrganizationRequest is the body of POST /api/v1/org/update-organization.
    type UpdateOrganizationRequest struct
    ```
12. `DecodeBody[updateOrganizationRequest]` → `DecodeBody[UpdateOrganizationRequest]`

In `internal/app/web/handler/auth/handler.go`:

1. `type registerRequest struct` →
   ```go
   // RegisterRequest is the body of POST /api/v1/auth/register.
   type RegisterRequest struct
   ```
2. `DecodeBody[registerRequest]` → `DecodeBody[RegisterRequest]`
3. `// Register creates an account.` →
   ```go
   // RegisterResponse is the data of POST /api/v1/auth/register.
   type RegisterResponse struct {
   	UserID string `json:"user_id"`
   	Email  string `json:"email"`
   }

   // Register creates an account.
   ```
4. ```go
   	webutil.Success(w, "registered", struct {
   		UserID string `json:"user_id"`
   		Email  string `json:"email"`
   	}{user.ID, email})
   ```
   →
   ```go
   	webutil.Success(w, "registered", RegisterResponse{UserID: user.ID, Email: email})
   ```
5. `// State reports what the login/register pages need` →
   ```go
   // StateResponse is the data of GET /api/v1/auth/state: bootstrap is true on
   // a fresh (unclaimed) instance.
   type StateResponse struct {
   	Bootstrap           bool `json:"bootstrap"`
   	RegistrationEnabled bool `json:"registration_enabled"`
   }

   // State reports what the login/register pages need
   ```
6. ```go
   	webutil.Success(w, "auth state", struct {
   		Bootstrap           bool `json:"bootstrap"`
   		RegistrationEnabled bool `json:"registration_enabled"`
   	}{Bootstrap: fresh, RegistrationEnabled: open})
   ```
   →
   ```go
   	webutil.Success(w, "auth state", StateResponse{Bootstrap: fresh, RegistrationEnabled: open})
   ```

- [ ] **Step 6: Verify: no unexported leftovers, everything green**

```bash
grep -rnE '\b(saveAppRequest|contentItemRequest|sourceRequest|controlAppRequest|deleteAppRequest|renameAppRequest|rollbackAppRequest|appWithDomains|registerServerRequest|publicKeyResponse|serverStatusDTO|changePasswordRequest|createTokenRequest|deleteTokenRequest|createUserRequest|updateMemberRequest|memberRequest|updateOrganizationRequest|registerRequest)\b' internal/app/web/handler || echo "no leftovers"
gofmt -l internal/app/web
go vet ./internal/app/web/...
go test ./internal/app/web/...
```

Expected: `no leftovers`, gofmt silent, vet silent, every package `ok` (including the unchanged `TestRoutesGolden`).

- [ ] **Step 7: Commit**

```bash
git add internal/app/web/handler
git commit -m "$(printf 'refactor(web): export handler request and response types\n\nNamed types replace the anonymous response structs so the OpenAPI\ngenerator can reflect them. Field order and JSON tags are unchanged, so\nresponses are byte-identical.\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 4: Per-package route tables

**Files:**
- Create: `internal/app/web/handler/app/routes.go`, `routes_test.go`
- Create: `internal/app/web/handler/docker/routes.go`, `routes_test.go`
- Create: `internal/app/web/handler/server/routes.go`, `routes_test.go`
- Create: `internal/app/web/handler/user/routes.go`, `routes_test.go`
- Create: `internal/app/web/handler/org/routes.go`, `routes_test.go`
- Create: `internal/app/web/handler/auth/routes.go`, `routes_test.go`
- Create: `internal/app/web/handler/notification/routes.go`, `routes_test.go`
- Create: `internal/app/web/auth/routes.go`, `routes_test.go`

**Interfaces:**
- Consumes: `api.Route`, `api.Guards`, `api.Middleware`, `api.User`, `api.Public`, `api.ServerQuery` (Task 2); exported types (Task 3).
- Produces:
  - `func (h *Handler) Routes(g api.Guards) []api.Route` in `handler/app` (13 routes), `handler/docker` (10), `handler/server` (4), `handler/user` (5), `handler/org` (7), `handler/auth` (2) and `handler/notification` (1).
  - `handler/app`: `type AppQuery struct{ServerID, AppID}`, `type LogsQuery struct{ServerID, AppID string; Since int64; Tail int32}`, `type ImageTagsQuery struct{ServerID, Image}`, `type CheckDomainQuery struct{Domain, AppID}` (query docs; handlers keep parsing `r.URL.Query()` as before).
  - `internal/app/web/auth`: `func Routes() []api.Route` (6 Mounted routes), plus `LoginRequest`, `LoginForm`, `LoginQuery`, `SessionUser` (`type SessionUser token.User`), `AuthStatus` and `AvatarPath`.
  - Admin placement (asserted by the tests; PR 4 deletes it): docker mutations (`registry/create|delete`, `network/create|delete`, `volume/create|delete`, `agent/update`), `server/register`, and every `org/*` except `get-organization`. `g.AuthLimit` sits on `POST /api/v1/auth/register` only (the `/auth/*` mount keeps its limiter in `routes.go`).

- [ ] **Step 1: Write the failing tests**

Create `internal/app/web/handler/docker/routes_test.go`:

```go
package docker

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

// adminGuard stands in for rbac.RequireAdmin; a named func so its code
// pointer identifies it.
func adminGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{Admin: adminGuard})
	// "METHOD path" -> behind the admin guard.
	want := map[string]bool{
		"GET /api/v1/registry/list":    false,
		"POST /api/v1/registry/create": true,
		"POST /api/v1/registry/delete": true,
		"GET /api/v1/network/list":     false,
		"POST /api/v1/network/create":  true,
		"POST /api/v1/network/delete":  true,
		"GET /api/v1/volume/list":      false,
		"POST /api/v1/volume/create":   true,
		"POST /api/v1/volume/delete":   true,
		"POST /api/v1/agent/update":    true,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || !rt.Accepted || rt.Result == nil {
			t.Errorf("%s: want a User, Accepted route with a handler and a Result", key)
		}
		if _, ok := rt.Query.(api.ServerQuery); !ok {
			t.Errorf("%s: Query = %T, want api.ServerQuery", key, rt.Query)
		}
		admin := false
		for _, mw := range rt.Middleware {
			admin = admin || reflect.ValueOf(mw).Pointer() == reflect.ValueOf(adminGuard).Pointer()
		}
		got[key] = admin
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> admin) = %v\nwant %v", got, want)
	}
}
```

Create `internal/app/web/handler/app/routes_test.go`:

```go
package server

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func adminGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{Admin: adminGuard})
	validate := reflect.ValueOf(GetAppsValidationMiddleware).Pointer()
	// "METHOD path" -> runs GetAppsValidationMiddleware. No app route is
	// admin-only: members keep the full app lifecycle.
	want := map[string]bool{
		"GET /api/v1/app/get-apps":        true,
		"GET /api/v1/app/get-apps-status": true,
		"POST /api/v1/app/refresh-apps":   true,
		"POST /api/v1/app/save-app":       false,
		"GET /api/v1/app/get-app":         false,
		"GET /api/v1/app/get-logs":        false,
		"GET /api/v1/app/get-revisions":   false,
		"POST /api/v1/app/rollback-app":   false,
		"GET /api/v1/image/get-tags":      false,
		"POST /api/v1/app/control-app":    false,
		"POST /api/v1/app/delete-app":     false,
		"POST /api/v1/app/rename-app":     false,
		"GET /api/v1/domains/check":       false,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User {
			t.Errorf("%s: want a User route with a handler", key)
		}
		if rt.Accepted != (rt.Result != nil) {
			t.Errorf("%s: Accepted=%v but Result=%T", key, rt.Accepted, rt.Result)
		}
		validated := false
		for _, mw := range rt.Middleware {
			p := reflect.ValueOf(mw).Pointer()
			if p == reflect.ValueOf(adminGuard).Pointer() {
				t.Errorf("%s: app routes must not be admin-only", key)
			}
			validated = validated || p == validate
		}
		got[key] = validated
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> validated) = %v\nwant %v", got, want)
	}
}
```

Create `internal/app/web/handler/server/routes_test.go`:

```go
package server

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func adminGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{Admin: adminGuard})
	want := map[string]bool{ // "METHOD path" -> behind the admin guard
		"GET /api/v1/server/get-servers":        false,
		"GET /api/v1/server/get-servers-status": false,
		"GET /api/v1/server/get-public-key":     false,
		"POST /api/v1/server/register":          true,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted {
			t.Errorf("%s: want a synchronous User route with a handler", key)
		}
		admin := false
		for _, mw := range rt.Middleware {
			admin = admin || reflect.ValueOf(mw).Pointer() == reflect.ValueOf(adminGuard).Pointer()
		}
		got[key] = admin
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> admin) = %v\nwant %v", got, want)
	}
}
```

Create `internal/app/web/handler/org/routes_test.go`:

```go
package org

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func adminGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{Admin: adminGuard})
	want := map[string]bool{ // "METHOD path" -> behind the admin guard
		"POST /api/v1/org/create-user":           true,
		"GET /api/v1/org/get-members":            true,
		"POST /api/v1/org/update-member":         true,
		"POST /api/v1/org/remove-member":         true,
		"POST /api/v1/org/reset-member-password": true,
		"GET /api/v1/org/get-organization":       false,
		"POST /api/v1/org/update-organization":   true,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted {
			t.Errorf("%s: want a synchronous User route with a handler", key)
		}
		admin := false
		for _, mw := range rt.Middleware {
			admin = admin || reflect.ValueOf(mw).Pointer() == reflect.ValueOf(adminGuard).Pointer()
		}
		got[key] = admin
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> admin) = %v\nwant %v", got, want)
	}
}
```

Create `internal/app/web/handler/user/routes_test.go`:

```go
package user

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func adminGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{Admin: adminGuard})
	want := []string{
		"POST /api/v1/user/create-token",
		"GET /api/v1/user/get-tokens",
		"POST /api/v1/user/delete-token",
		"GET /api/v1/user/get-profile",
		"POST /api/v1/user/change-password",
	}
	var got []string
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted || len(rt.Middleware) != 0 {
			t.Errorf("%s: want a synchronous User route with a handler and no extra middleware", key)
		}
		got = append(got, key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}
```

Create `internal/app/web/handler/auth/routes_test.go`:

```go
package auth

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func limitGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{AuthLimit: limitGuard})
	want := map[string]bool{ // "METHOD path" -> behind the auth rate limiter
		"POST /api/v1/auth/register": true,
		"GET /api/v1/auth/state":     false,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.Public {
			t.Errorf("%s: want a Public route with a handler", key)
		}
		limited := false
		for _, mw := range rt.Middleware {
			limited = limited || reflect.ValueOf(mw).Pointer() == reflect.ValueOf(limitGuard).Pointer()
		}
		got[key] = limited
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> limited) = %v\nwant %v", got, want)
	}
}
```

Create `internal/app/web/handler/notification/routes_test.go`:

```go
package notification

import (
	"net/http"
	"testing"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	rt := routes[0]
	if rt.Method != http.MethodGet || rt.Path != "/api/v1/sse" || rt.Handler == nil || rt.Auth != api.User {
		t.Errorf("sse route = %+v", rt)
	}
	if _, ok := rt.Events.(model.Notification); !ok {
		t.Errorf("Events = %T, want model.Notification", rt.Events)
	}
}
```

Create `internal/app/web/auth/routes_test.go`:

```go
package auth

import (
	"strings"
	"testing"
)

func TestRoutesAreMountedLibraryRoutes(t *testing.T) {
	routes := Routes()
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if !rt.Mounted || !rt.Raw || rt.Handler != nil {
			t.Errorf("%s: want Mounted, Raw and no handler (go-pkgz serves it)", key)
		}
		if !strings.HasPrefix(rt.Path, "/auth/") && !strings.HasPrefix(rt.Path, "/avatar/") {
			t.Errorf("%s: outside the /auth and /avatar mounts", key)
		}
	}
}
```

Why named sentinel funcs: `reflect.Value.Pointer()` on a func returns its code pointer. Two closures created from the same literal share that pointer, so they cannot be told apart. Named top-level functions are unique.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/web/handler/... ./internal/app/web/auth/`
Expected: FAIL to build with `h.Routes undefined (type *Handler has no field or method Routes)` in each handler package, and `undefined: Routes` in `internal/app/web/auth`.

- [ ] **Step 3: Write the tables**

Create `internal/app/web/handler/app/routes.go`:

```go
package server

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
)

// AppQuery addresses one app on one server.
type AppQuery struct {
	ServerID string `query:"server_id" required:"true"`
	AppID    string `query:"app_id" required:"true"`
}

// LogsQuery is the query of GET /api/v1/app/get-logs.
type LogsQuery struct {
	ServerID string `query:"server_id" required:"true"`
	AppID    string `query:"app_id" required:"true"`
	Since    int64  `query:"since" description:"Unix seconds; 0 or absent = no lower bound."`
	Tail     int32  `query:"tail" description:"Last N lines; default 200."`
}

// ImageTagsQuery is the query of GET /api/v1/image/get-tags.
type ImageTagsQuery struct {
	ServerID string `query:"server_id" required:"true"`
	Image    string `query:"image" required:"true" description:"Image reference, e.g. nginx or ghcr.io/org/app."`
}

// CheckDomainQuery is the query of GET /api/v1/domains/check.
type CheckDomainQuery struct {
	Domain string `query:"domain" description:"Hostname to check."`
	AppID  string `query:"app_id" description:"App being edited; its own claims don't count."`
}

// Routes is the app handler's slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	// Every server-addressed route: 400 on bad input or a failed dispatch,
	// 403 when the caller's org doesn't own the server.
	server := []int{http.StatusBadRequest, http.StatusForbidden}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-apps", Handler: h.GetApps,
			Auth: api.User, Middleware: []api.Middleware{GetAppsValidationMiddleware},
			Summary: "List a server's apps (DB cache) with their domains", Tag: "apps",
			Query: api.ServerQuery{}, Response: GetAppsResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-apps-status", Handler: h.GetAppsStatus,
			Auth: api.User, Middleware: []api.Middleware{GetAppsValidationMiddleware},
			Summary: "Latest cached container status of a server's apps", Tag: "apps",
			Query: api.ServerQuery{}, Response: command.GetAppsStatusResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Handler: h.RefreshApps,
			Auth: api.User, Middleware: []api.Middleware{GetAppsValidationMiddleware},
			Summary: "Re-list apps from the agent and reconcile the DB cache", Tag: "apps",
			Query: api.ServerQuery{}, Accepted: true, Result: command.ListAppsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/save-app", Handler: h.SaveApp, Auth: api.User,
			Summary: "Create or edit an app (new revision, redeploy unless draft)", Tag: "apps",
			Request: SaveAppRequest{}, Accepted: true, Result: command.SaveAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-app", Handler: h.GetApp, Auth: api.User,
			Summary: "Fetch an app's config, variables and files", Tag: "apps",
			Query: AppQuery{}, Accepted: true, Result: command.GetAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-logs", Handler: h.GetLogs, Auth: api.User,
			Summary: "Fetch an app's container logs", Tag: "apps",
			Query: LogsQuery{}, Accepted: true, Result: command.GetLogsResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-revisions", Handler: h.GetRevisions, Auth: api.User,
			Summary: "List an app's revision history", Tag: "apps",
			Query: AppQuery{}, Accepted: true, Result: command.GetRevisionsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/rollback-app", Handler: h.RollbackApp, Auth: api.User,
			Summary: "Restore a revision as a new commit and redeploy", Tag: "apps",
			Request: RollbackAppRequest{}, Accepted: true, Result: command.RollbackAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/image/get-tags", Handler: h.GetImageTags, Auth: api.User,
			Summary: "List registry tags for an image (agent credentials)", Tag: "apps",
			Query: ImageTagsQuery{}, Accepted: true, Result: command.ImageTagsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/control-app", Handler: h.ControlApp, Auth: api.User,
			Summary: "Start, stop, restart or update an app", Tag: "apps",
			Request: ControlAppRequest{}, Accepted: true, Result: command.ControlAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/delete-app", Handler: h.DeleteApp, Auth: api.User,
			Summary: "Delete an app (optionally its volumes)", Tag: "apps",
			Request: DeleteAppRequest{}, Accepted: true, Result: command.DeleteAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/rename-app", Handler: h.RenameApp, Auth: api.User,
			Summary: "Rename an app", Tag: "apps",
			Request: RenameAppRequest{}, Accepted: true, Result: command.RenameAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domains/check", Handler: h.CheckDomain, Auth: api.User,
			Summary: "Check whether a hostname is free", Tag: "apps",
			Query: CheckDomainQuery{}, Response: CheckDomainResponse{}, Errors: []int{http.StatusBadRequest},
		},
	}
}
```

Create `internal/app/web/handler/docker/routes.go`:

```go
package docker

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
)

// Routes is the docker handler's slice of the route table. Every route is
// server-addressed (server_id query) and agent-bound (202 + request_id).
// Mutations are admin-only via g.Admin.
func (h *Handler) Routes(g api.Guards) []api.Route {
	read := []int{http.StatusBadRequest, http.StatusForbidden}
	admin := []api.Middleware{g.Admin}
	route := func(method, path string, hf http.HandlerFunc, mw []api.Middleware, summary string, req, result any) api.Route {
		return api.Route{
			Method: method, Path: path, Handler: hf, Auth: api.User, Middleware: mw,
			Summary: summary, Tag: "docker", Query: api.ServerQuery{}, Request: req,
			Accepted: true, Result: result, Errors: read,
		}
	}
	return []api.Route{
		route(http.MethodGet, "/api/v1/registry/list", h.ListRegistries, nil,
			"List the agent's Docker registry logins", nil, command.ListRegistriesResponse{}),
		route(http.MethodPost, "/api/v1/registry/create", h.CreateRegistry, admin,
			"Log the agent in to a registry", command.CreateRegistryRequest{}, command.CreateRegistryResponse{}),
		route(http.MethodPost, "/api/v1/registry/delete", h.DeleteRegistry, admin,
			"Log the agent out of a registry", command.DeleteRegistryRequest{}, command.DeleteRegistryResponse{}),
		route(http.MethodGet, "/api/v1/network/list", h.ListNetworks, nil,
			"List Docker networks", nil, command.ListNetworksResponse{}),
		route(http.MethodPost, "/api/v1/network/create", h.CreateNetwork, admin,
			"Create a Docker network", command.CreateNetworkRequest{}, command.CreateNetworkResponse{}),
		route(http.MethodPost, "/api/v1/network/delete", h.DeleteNetwork, admin,
			"Delete a Docker network", command.DeleteNetworkRequest{}, command.DeleteNetworkResponse{}),
		route(http.MethodGet, "/api/v1/volume/list", h.ListVolumes, nil,
			"List Docker volumes", nil, command.ListVolumesResponse{}),
		route(http.MethodPost, "/api/v1/volume/create", h.CreateVolume, admin,
			"Create a Docker volume", command.CreateVolumeRequest{}, command.CreateVolumeResponse{}),
		route(http.MethodPost, "/api/v1/volume/delete", h.DeleteVolume, admin,
			"Delete a Docker volume", command.DeleteVolumeRequest{}, command.DeleteVolumeResponse{}),
		route(http.MethodPost, "/api/v1/agent/update", h.UpdateAgent, admin,
			"Self-update the agent binary", command.UpdateAgentRequest{}, command.UpdateAgentResponse{}),
	}
}
```

Create `internal/app/web/handler/server/routes.go`:

```go
package server

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the server handler's slice of the route table. Registering a
// server is admin-only via g.Admin.
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/server/get-servers", Handler: h.GetServers, Auth: api.User,
			Summary: "List the caller's servers", Tag: "server",
			Response: GetServersResponse{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/server/get-servers-status", Handler: h.GetServersStatus, Auth: api.User,
			Summary: "Live liveness of the caller's servers", Tag: "server",
			Response: GetServersStatusResponse{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/server/get-public-key", Handler: h.GetPublicKey, Auth: api.User,
			Summary: "The agent's public key for encrypting secrets", Tag: "server",
			Query: api.ServerQuery{}, Response: PublicKeyResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/server/register", Handler: h.Register, Auth: api.User,
			Middleware: []api.Middleware{g.Admin},
			Summary:    "Claim a pending server by its pairing code", Tag: "server",
			Request: RegisterServerRequest{}, Response: RegisterServerResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden},
		},
	}
}
```

Create `internal/app/web/handler/user/routes.go`:

```go
package user

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the user handler's slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	bad := []int{http.StatusBadRequest}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/user/create-token", Handler: h.CreateToken, Auth: api.User,
			Summary: "Mint a personal access token", Tag: "user",
			Request: CreateTokenRequest{}, Response: CreateTokenResponse{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/user/get-tokens", Handler: h.GetTokens, Auth: api.User,
			Summary: "List the caller's personal access tokens", Tag: "user",
			Response: []model.UserToken{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/delete-token", Handler: h.DeleteToken, Auth: api.User,
			Summary: "Revoke a personal access token", Tag: "user",
			Request: DeleteTokenRequest{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/user/get-profile", Handler: h.GetProfile, Auth: api.User,
			Summary: "The caller's profile and role", Tag: "user",
			Response: Profile{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/change-password", Handler: h.ChangePassword, Auth: api.User,
			Summary: "Change the caller's local password", Tag: "user",
			Request: ChangePasswordRequest{}, Errors: bad,
		},
	}
}
```

Create `internal/app/web/handler/org/routes.go`:

```go
package org

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the org handler's slice of the route table. Everything except
// get-organization is admin-only via g.Admin.
func (h *Handler) Routes(g api.Guards) []api.Route {
	admin := []api.Middleware{g.Admin}
	// 403 comes from the admin guard.
	adminErrs := []int{http.StatusBadRequest, http.StatusForbidden}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/org/create-user", Handler: h.CreateUser, Auth: api.User, Middleware: admin,
			Summary: "Create an account in the caller's org (temp password)", Tag: "org",
			Request: CreateUserRequest{}, Response: CreateUserResponse{}, Errors: adminErrs,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/get-members", Handler: h.GetMembers, Auth: api.User, Middleware: admin,
			Summary: "List the caller's org members", Tag: "org",
			Response: []model.Member{}, Errors: adminErrs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/update-member", Handler: h.UpdateMember, Auth: api.User, Middleware: admin,
			Summary: "Change a member's role", Tag: "org",
			Request: UpdateMemberRequest{}, Errors: adminErrs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/remove-member", Handler: h.RemoveMember, Auth: api.User, Middleware: admin,
			Summary: "Remove a member and their account", Tag: "org",
			Request: MemberRequest{}, Errors: adminErrs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/reset-member-password", Handler: h.ResetMemberPassword, Auth: api.User, Middleware: admin,
			Summary: "Issue a member a new temp password", Tag: "org",
			Request: MemberRequest{}, Response: ResetMemberPasswordResponse{}, Errors: adminErrs,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/get-organization", Handler: h.GetOrganization, Auth: api.User,
			Summary: "The caller's org name, icon and color", Tag: "org",
			Response: model.Organization{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/update-organization", Handler: h.UpdateOrganization, Auth: api.User, Middleware: admin,
			Summary: "Rename or restyle the caller's org", Tag: "org",
			Request: UpdateOrganizationRequest{}, Errors: adminErrs,
		},
	}
}
```

Create `internal/app/web/handler/auth/routes.go`:

```go
package auth

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the public auth handler's slice of the route table. Register is
// bcrypt-priced, so it sits behind g.AuthLimit.
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/auth/register", Handler: h.Register, Auth: api.Public,
			Middleware: []api.Middleware{g.AuthLimit},
			Summary:    "Create an account (first call claims a fresh instance)", Tag: "auth",
			Request: RegisterRequest{}, Response: RegisterResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/auth/state", Handler: h.State, Auth: api.Public,
			Summary: "Whether the instance is fresh and registration is open", Tag: "auth",
			Response: StateResponse{}, Errors: []int{http.StatusBadRequest},
		},
	}
}
```

Create `internal/app/web/handler/notification/routes.go`:

```go
package notification

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the SSE stream's slice of the route table. Each event is
// "data: <Notification JSON>\n\n"; operation_result events carry the
// x-operation-result payload of the Accepted route that dispatched them.
func (h *Handler) Routes(api.Guards) []api.Route {
	return []api.Route{{
		Method: http.MethodGet, Path: "/api/v1/sse", Handler: h.Stream, Auth: api.User,
		Summary: "Notification stream (operation results, status changes)", Tag: "system",
		Events: model.Notification{},
	}}
}
```

Create `internal/app/web/auth/routes.go`:

```go
package auth

import (
	"net/http"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/app/web/api"
)

// LoginRequest is the JSON body of POST /auth/local/login (go-pkgz direct
// provider). user is the account email; aud is the audience (the UI's
// hostname).
type LoginRequest struct {
	User   string `json:"user"`
	Passwd string `json:"passwd"`
	Aud    string `json:"aud"`
}

// LoginForm is the form-encoded alternative the web UI sends.
type LoginForm struct {
	User   string `formData:"user"`
	Passwd string `formData:"passwd"`
	Aud    string `formData:"aud"`
}

// LoginQuery: sess=1 issues a session-only cookie.
type LoginQuery struct {
	Sess string `query:"sess" enum:"0,1"`
}

// SessionUser is the go-pkgz user record (login and /auth/user responses).
type SessionUser token.User

// AuthStatus is the body of GET /auth/status.
type AuthStatus struct {
	Status string `json:"status" enum:"logged in,not logged in"`
	User   string `json:"user,omitempty"`
}

// AvatarPath names a stored avatar file.
type AvatarPath struct {
	Avatar string `path:"avatar"`
}

// Routes documents the go-pkgz endpoints mounted at /auth (behind the auth
// rate limiter) and /avatar. They are Mounted: served by the library, so
// api.Register skips them. Responses are the library's own JSON, not the
// webutil envelope (Raw); its errors are {"error": "..."}. Google OAuth
// (/auth/google/*) exists only when configured and is not listed.
func Routes() []api.Route {
	limited := []int{http.StatusTooManyRequests}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/auth/local/login", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "Email+password login; sets the JWT cookie", Tag: "auth",
			Query: LoginQuery{}, Request: LoginRequest{}, Form: LoginForm{}, Response: SessionUser{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		{
			Method: http.MethodGet, Path: "/auth/logout", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "Clear the JWT cookie", Tag: "auth", Errors: limited,
		},
		{
			Method: http.MethodGet, Path: "/auth/status", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "Whether the request carries a valid session", Tag: "auth",
			Response: AuthStatus{}, Errors: limited,
		},
		{
			Method: http.MethodGet, Path: "/auth/user", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "The session's user record", Tag: "auth",
			Response: SessionUser{}, Errors: []int{http.StatusUnauthorized, http.StatusTooManyRequests},
		},
		{
			Method: http.MethodGet, Path: "/auth/list", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "Names of the enabled login providers", Tag: "auth",
			Response: []string{}, Errors: limited,
		},
		{
			Method: http.MethodGet, Path: "/avatar/{avatar}", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "A stored avatar image", Tag: "auth",
			Query: AvatarPath{}, ContentType: "image/*",
			Errors: []int{http.StatusBadRequest, http.StatusForbidden},
		},
	}
}
```

- [ ] **Step 4: Run the tests**

```bash
gofmt -l internal/app/web
go vet ./internal/app/web/...
go test ./internal/app/web/...
```

Expected: gofmt silent (if it lists a file, run `gofmt -w` on it; gofmt aligns a run of single-key composite-literal lines), vet silent, all packages `ok`. `TestRoutesGolden` is still green because nothing registers the tables yet.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web/handler internal/app/web/auth
git commit -m "$(printf 'feat(web): describe every endpoint in per-package route tables\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 5: Register the router from the table

**Files:**
- Modify: `internal/app/web/routes.go` (whole file)
- Create: `internal/app/web/routes_table_test.go`

**Interfaces:**
- Consumes: `api.Register`, `api.Guards` (Task 2); every `Routes(g)` and `authprvd.Routes()` (Task 4); `testServer`, `health` (Task 1).
- Produces (package `web`):
  - `func APIRoutes() []api.Route` returns the full table over zero deps and nil guards, for documentation (Task 7's `cmd/openapi` and tests). **Never pass it to `api.Register`**; that panics by design.
  - `func routeTable(d *bootstrap.Deps, g api.Guards) []api.Route` (unexported) builds each handler from `d` exactly as `registerRoutes` does today: the same `Deps` fields and the same shared `NotificationManager`.

- [ ] **Step 1: Write the failing tests**

Create `internal/app/web/routes_table_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestRouteTableMatchesRouter proves the router serves exactly the table:
// every non-Mounted route is registered, nothing else is (besides the two
// library mounts), and every Mounted route lives under one of those mounts.
func TestRouteTableMatchesRouter(t *testing.T) {
	var walked []string
	err := chi.Walk(testServer(t), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if route == "/auth/*" || route == "/avatar/*" {
			return nil
		}
		walked = append(walked, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var table []string
	for _, rt := range APIRoutes() {
		if rt.Mounted {
			if !strings.HasPrefix(rt.Path, "/auth/") && !strings.HasPrefix(rt.Path, "/avatar/") {
				t.Errorf("Mounted %s %s is outside the /auth and /avatar mounts", rt.Method, rt.Path)
			}
			continue
		}
		table = append(table, rt.Method+" "+rt.Path)
	}
	sort.Strings(walked)
	sort.Strings(table)
	if strings.Join(walked, "\n") != strings.Join(table, "\n") {
		t.Errorf("router and table differ.\nrouter:\n%s\ntable:\n%s", strings.Join(walked, "\n"), strings.Join(table, "\n"))
	}
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_/health", nil))
	want := `{"success":true,"message":"healthy","data":null}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("GET /_/health = %d %q, want 200 %q", rec.Code, rec.Body.String(), want)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/web/ -run 'TestRouteTableMatchesRouter|TestHealth'`
Expected: FAIL to build with `undefined: APIRoutes`.

- [ ] **Step 3: Rewrite `routes.go` over the table**

Replace `internal/app/web/routes.go` with:

```go
package web

import (
	"net/http"
	"winterflow/internal/app/web/spa"
	webdist "winterflow/web"

	"winterflow/internal/app/web/api"
	authprvd "winterflow/internal/app/web/auth"
	happ "winterflow/internal/app/web/handler/app"
	hauth "winterflow/internal/app/web/handler/auth"
	hdocker "winterflow/internal/app/web/handler/docker"
	"winterflow/internal/app/web/handler/notification"
	horg "winterflow/internal/app/web/handler/org"
	"winterflow/internal/app/web/handler/server"
	huser "winterflow/internal/app/web/handler/user"
	"winterflow/internal/app/web/middleware/patauth"
	"winterflow/internal/app/web/middleware/ratelimit"
	"winterflow/internal/app/web/middleware/rbac"
	"winterflow/internal/app/web/util"
	"winterflow/internal/infra/bootstrap"
)

func (s *Server) registerRoutes() {
	// Auth endpoints are the API's only bcrypt-priced requests — throttle them
	// per client IP so credential floods answer 429 instead of burning CPU.
	// One limiter is shared by the go-pkgz /auth mount and /api/v1/auth/register.
	authLimiter := ratelimit.New(5, 10)

	authRoutes, avaRoutes := s.Auth.Handlers()
	s.Router.With(authLimiter.Middleware).Mount("/auth", authRoutes) // add auth handlers
	s.Router.Mount("/avatar", avaRoutes)                             // add avatar handler

	amw := s.Auth.Middleware()
	// authMW = PAT bearer first, JWT session otherwise. Every User route
	// uses this so tokens work everywhere a browser session does.
	authMW := patauth.Middleware(s.Deps.UserService, amw.Auth)
	guards := api.Guards{
		// Admin gates administration (member management, server
		// registration, infrastructure mutations) to owners/admins. Members
		// keep the full app lifecycle.
		Admin:     rbac.RequireAdmin(s.Deps.UserService),
		AuthLimit: authLimiter.Middleware,
	}
	api.Register(s.Router, routeTable(s.Deps, guards), authMW)

	// Serve the embedded SPA for everything the API doesn't claim (history-mode
	// fallback). A source build without `make web-build` embeds only the
	// committed placeholder; the server then stays API-only.
	if distFS, ok := webdist.DistFS(); ok {
		s.Router.NotFound(spa.Handler(distFS).ServeHTTP)
		s.Logger.Info("serving embedded web UI")
	} else {
		s.Logger.Info("no embedded web UI build; serving API only (run `make web-build` before building the binary)")
	}
}

// APIRoutes is the full route table for documentation (cmd/openapi): the
// handlers are built over zero dependencies and the guards are nil, so it
// must never be passed to api.Register.
func APIRoutes() []api.Route {
	return routeTable(&bootstrap.Deps{}, api.Guards{})
}

// routeTable collects every handler package's routes, plus the health probe
// and the go-pkgz mounts (documented only). The NotificationManager is shared
// across handlers: the SSE stream subscribes to it and the usecases publish
// to it, so every handler MUST receive the same instance from d.
func routeTable(d *bootstrap.Deps, g api.Guards) []api.Route {
	authAPI := hauth.NewHandler(&hauth.Deps{Logger: d.Log, Cfg: d.Cfg, Users: d.UserService})
	notificationAPI := notification.NewHandler(&notification.Deps{
		Logger:              d.Log,
		NotificationManager: d.NotificationManager,
	})
	appsAPI := happ.NewHandler(&happ.Deps{
		Logger:              d.Log,
		CommandDispatcher:   d.CommandDispatcher,
		AppRepository:       d.AppRepository,
		AppDomainRepository: d.AppDomainRepository,
		StatusCache:         d.StatusCache,
		Servers:             d.ServerRepository,
	})
	dockerAPI := hdocker.NewHandler(&hdocker.Deps{
		Logger:            d.Log,
		CommandDispatcher: d.CommandDispatcher,
		Servers:           d.ServerRepository,
	})
	serversAPI := server.NewHandler(&server.Deps{
		Logger:           d.Log,
		ServerRepository: d.ServerRepository,
		UserService:      d.UserService,
		StatusCache:      d.StatusCache,
		Cfg:              d.Cfg,
	})
	usersAPI := huser.NewHandler(&huser.Deps{Logger: d.Log, Tokens: d.UserService, Users: d.UserService})
	orgAPI := horg.NewHandler(&horg.Deps{Logger: d.Log, Users: d.UserService})

	routes := []api.Route{{
		Method: http.MethodGet, Path: "/_/health", Handler: health, Auth: api.Public,
		Summary: "Liveness probe", Tag: "system",
	}}
	for _, table := range [][]api.Route{
		authAPI.Routes(g),
		notificationAPI.Routes(g),
		appsAPI.Routes(g),
		dockerAPI.Routes(g),
		serversAPI.Routes(g),
		usersAPI.Routes(g),
		orgAPI.Routes(g),
		authprvd.Routes(),
	} {
		routes = append(routes, table...)
	}
	return routes
}

// health is the liveness probe: always 200 {"success":true,"message":"healthy"}.
func health(w http.ResponseWriter, _ *http.Request) {
	util.Success(w, "healthy", nil)
}
```

Notes for the reviewer: `s.Logger` was `deps.Log` and `s.Cfg` was `deps.Cfg` (see `NewServer`), so the handlers receive the same values as before. The rate limiter is still one instance shared by the `/auth` mount and the register route.

- [ ] **Step 4: Run the tests (the golden must be unchanged)**

```bash
go test ./internal/app/web/
git diff --exit-code internal/app/web/testdata/routes.golden
go vet ./internal/app/web/...
```

Expected: `ok`. `TestRoutesGolden`, `TestRouteTableMatchesRouter` and `TestHealth` all pass, and `git diff --exit-code` exits 0. If `TestRoutesGolden` fails, the refactor changed a path, handler or middleware chain. Fix the code; **do not** re-run `-update`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web/routes.go internal/app/web/routes_table_test.go
git commit -m "$(printf 'refactor(web): register routes from the typed route table\n\nRegistered paths and middleware chains are unchanged (routes.golden).\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 6: The OpenAPI 3.1 generator

**Files:**
- Create: `internal/app/web/api/openapi/openapi.go`
- Test: `internal/app/web/api/openapi/openapi_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `api.Route`, `api.User` (Task 2).
- Produces (package `openapi`, used by Task 7):
  - `func Build(routes []api.Route) (*openapi31.Spec, error)` fails on: an Accepted route without `Result`, an unnamed `Result` type, an unsupported field type (chan/func), two Go types sharing a component name, or a duplicate method+path.
  - `func YAML(routes []api.Route) ([]byte, error)` returns deterministic bytes.
  - `const SchemeBearer = "bearer"`, `SchemeCookie = "cookie"`, `ResultExtension = "x-operation-result"`.
  - Schema types: `Null` (exposes `{"type":"null"}`), `Error {success:false const, message, data:null}` (all required), `LibraryError {error}`, `AcceptedData {request_id}`.
  - Unexported: `envelope(data any) any` (an unnamed struct `{success:true const, message omitempty, data:T}` built with `reflect.StructOf`), `errorCodes(api.Route) []int`, `addError`, `(*builder).component(v) (ref string, err error)`, `operationID(path) string`.

- [ ] **Step 1: Write the failing tests**

Create `internal/app/web/api/openapi/openapi_test.go`:

```go
package openapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"winterflow/internal/app/web/api"
)

type thing struct {
	Name string `json:"name"`
}

type thingResult struct {
	ID string `json:"id"`
}

type thingForm struct {
	Name string `formData:"name"`
}

type avatarPath struct {
	Avatar string `path:"avatar"`
}

type thingQuery struct {
	ServerID string `query:"server_id" required:"true"`
}

func noop(http.ResponseWriter, *http.Request) {}

// sample covers every response shape the generator knows.
func sample() []api.Route {
	return []api.Route{
		{Method: http.MethodGet, Path: "/_/health", Handler: noop, Summary: "health", Tag: "system"},
		{Method: http.MethodGet, Path: "/api/v1/thing/get-thing", Handler: noop, Auth: api.User,
			Summary: "get", Tag: "things", Query: thingQuery{}, Response: thing{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusBadRequest}},
		{Method: http.MethodPost, Path: "/api/v1/thing/save-thing", Handler: noop, Auth: api.User,
			Summary: "save", Tag: "things", Request: thing{}, Accepted: true, Result: thingResult{},
			Errors: []int{http.StatusBadRequest}},
		// A second operation with the same Result reuses the component.
		{Method: http.MethodPost, Path: "/api/v1/thing/copy-thing", Handler: noop, Auth: api.User,
			Summary: "copy", Tag: "things", Accepted: true, Result: thingResult{}},
		{Method: http.MethodGet, Path: "/api/v1/sse", Handler: noop, Auth: api.User,
			Summary: "events", Tag: "system", Events: thing{}},
		{Method: http.MethodPost, Path: "/auth/local/login", Mounted: true, Raw: true,
			Summary: "login", Tag: "auth", Request: thing{}, Form: thingForm{}, Response: thingResult{},
			Errors: []int{http.StatusForbidden, http.StatusTooManyRequests}},
		{Method: http.MethodGet, Path: "/avatar/{avatar}", Mounted: true, Raw: true,
			Summary: "avatar", Tag: "auth", Query: avatarPath{}, ContentType: "image/*"},
	}
}

// load builds routes and parses the YAML back with kin-openapi, the
// validator PR 3's contract suite uses.
func load(t *testing.T, routes []api.Route) *openapi3.T {
	t.Helper()
	out, err := YAML(routes)
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(out)
	if err != nil {
		t.Fatalf("load: %v\n%s", err, out)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	return doc
}

func schemaFor(t *testing.T, doc *openapi3.T, method, path string, status int, contentType string) *openapi3.Schema {
	t.Helper()
	item := doc.Paths.Find(path)
	if item == nil {
		t.Fatalf("no path %s", path)
	}
	op := item.GetOperation(method)
	if op == nil {
		t.Fatalf("no %s %s", method, path)
	}
	resp := op.Responses.Status(status)
	if resp == nil || resp.Value == nil {
		t.Fatalf("%s %s: no %d response", method, path, status)
	}
	mt := resp.Value.Content.Get(contentType)
	if mt == nil || mt.Schema == nil {
		t.Fatalf("%s %s %d: no %s content", method, path, status, contentType)
	}
	return mt.Schema.Value
}

func visit(s *openapi3.Schema, body string) error {
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return err
	}
	return s.VisitJSON(v, openapi3.EnableJSONSchema2020())
}

func TestBuildValidDocument(t *testing.T) {
	doc := load(t, sample())
	if doc.OpenAPI != "3.1.0" {
		t.Errorf("openapi = %q, want 3.1.0", doc.OpenAPI)
	}
	for _, name := range []string{SchemeBearer, SchemeCookie} {
		if doc.Components.SecuritySchemes[name] == nil {
			t.Errorf("missing security scheme %q", name)
		}
	}
	if got := len(doc.Paths.Map()); got != 7 {
		t.Errorf("paths = %d, want 7", got)
	}
}

func TestEnvelopeSchemas(t *testing.T) {
	doc := load(t, sample())
	cases := []struct {
		name, method, path string
		status             int
		contentType, body  string
		ok                 bool
	}{
		{"null data", "GET", "/_/health", 200, "application/json", `{"success":true,"message":"healthy","data":null}`, true},
		{"null data, no message", "GET", "/_/health", 200, "application/json", `{"success":true,"data":null}`, true},
		{"null data rejects payload", "GET", "/_/health", 200, "application/json", `{"success":true,"data":{"x":1}}`, false},
		{"typed data", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":true,"data":{"name":"n"}}`, true},
		{"typed data wrong type", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":true,"data":{"name":1}}`, false},
		{"success must be true", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":false,"data":{"name":"n"}}`, false},
		{"data required", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":true}`, false},
		{"accepted", "POST", "/api/v1/thing/save-thing", 202, "application/json", `{"success":true,"message":"m","data":{"request_id":"r"}}`, true},
		{"accepted needs request_id", "POST", "/api/v1/thing/save-thing", 202, "application/json", `{"success":true,"data":{}}`, false},
		{"error", "POST", "/api/v1/thing/save-thing", 400, "application/json", `{"success":false,"message":"bad","data":null}`, true},
		{"error must be unsuccessful", "POST", "/api/v1/thing/save-thing", 400, "application/json", `{"success":true,"message":"bad","data":null}`, false},
		{"401 json", "GET", "/api/v1/thing/get-thing", 401, "application/json", `{"success":false,"message":"unauthorized","data":null}`, true},
		{"401 text", "GET", "/api/v1/thing/get-thing", 401, "text/plain", `"Unauthorized\n"`, true},
		{"403", "GET", "/api/v1/thing/get-thing", 403, "application/json", `{"success":false,"message":"forbidden","data":null}`, true},
		{"raw body", "POST", "/auth/local/login", 200, "application/json", `{"id":"u1"}`, true},
		{"library error", "POST", "/auth/local/login", 403, "application/json", `{"error":"incorrect user or password"}`, true},
		{"429 text", "POST", "/auth/local/login", 429, "text/plain", `"too many requests\n"`, true},
		{"sse event", "GET", "/api/v1/sse", 200, "text/event-stream", `{"name":"n"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := visit(schemaFor(t, doc, tc.method, tc.path, tc.status, tc.contentType), tc.body)
			if tc.ok && err != nil {
				t.Errorf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("want invalid, got valid")
			}
		})
	}
}

func TestErrorStatuses(t *testing.T) {
	doc := load(t, sample())
	get := doc.Paths.Find("/api/v1/thing/get-thing").Get
	for _, code := range []int{400, 401, 403} {
		if get.Responses.Status(code) == nil {
			t.Errorf("get-thing: missing %d", code)
		}
	}
	if n := get.Responses.Len(); n != 4 { // 200, 400, 401, 403: duplicates collapse
		t.Errorf("get-thing responses = %d, want 4", n)
	}
	health := doc.Paths.Find("/_/health").Get
	if health.Responses.Status(401) != nil {
		t.Error("public route documents 401")
	}
	if health.Security != nil && len(*health.Security) > 0 {
		t.Error("public route has security requirements")
	}
	if sec := get.Security; sec == nil || len(*sec) != 2 {
		t.Errorf("user route security = %v, want bearer or cookie", sec)
	}
}

func TestRequestBodies(t *testing.T) {
	doc := load(t, sample())
	body := doc.Paths.Find("/auth/local/login").Post.RequestBody.Value.Content
	if mt := body.Get("application/json"); mt == nil || mt.Schema.Ref != "#/components/schemas/thing" {
		t.Errorf("login JSON body = %+v, want $ref thing", mt)
	}
	if mt := body.Get("application/x-www-form-urlencoded"); mt == nil || mt.Schema.Ref != "#/components/schemas/FormDatathingForm" {
		t.Errorf("login form body = %+v, want $ref FormDatathingForm", mt)
	}
	avatar := doc.Paths.Find("/avatar/{avatar}").Get
	if p := avatar.Parameters.GetByInAndName("path", "avatar"); p == nil || !p.Required {
		t.Errorf("avatar path param = %+v", p)
	}
	if avatar.Responses.Status(http.StatusOK).Value.Content.Get("image/*") == nil {
		t.Error("avatar must document an image/* body")
	}
}

func TestQueryParameters(t *testing.T) {
	doc := load(t, sample())
	p := doc.Paths.Find("/api/v1/thing/get-thing").Get.Parameters.GetByInAndName("query", "server_id")
	if p == nil || !p.Required {
		t.Fatalf("server_id query param = %+v, want required", p)
	}
}

func TestOperationResultExtension(t *testing.T) {
	doc := load(t, sample())
	op := doc.Paths.Find("/api/v1/thing/save-thing").Post
	ext, ok := op.Extensions[ResultExtension].(map[string]any)
	if !ok {
		t.Fatalf("x-operation-result = %#v", op.Extensions[ResultExtension])
	}
	ref, _ := ext["$ref"].(string)
	if ref != "#/components/schemas/thingResult" {
		t.Fatalf("$ref = %q", ref)
	}
	s := doc.Components.Schemas[strings.TrimPrefix(ref, componentsSchemas)]
	if s == nil || s.Value.Properties["id"] == nil {
		t.Fatalf("result component missing or wrong: %+v", s)
	}
	if _, ok := doc.Paths.Find("/api/v1/thing/get-thing").Get.Extensions[ResultExtension]; ok {
		t.Error("synchronous route carries x-operation-result")
	}
}

func TestBuildErrors(t *testing.T) {
	type other struct {
		Name int `json:"name"`
	}
	type unsupported struct {
		C chan int `json:"c"`
	}
	collide := func() any {
		// A second type that is also named "thing".
		type thing struct {
			X int `json:"x"`
		}
		return thing{}
	}
	cases := []struct {
		name   string
		routes []api.Route
		want   string
	}{
		{"accepted without result", []api.Route{
			{Method: "POST", Path: "/a", Accepted: true},
		}, "no Result"},
		{"unnamed result", []api.Route{
			{Method: "POST", Path: "/a", Accepted: true, Result: struct{ A int }{}},
		}, "not a named type"},
		{"name clash", []api.Route{
			{Method: "GET", Path: "/a", Response: thing{}},
			{Method: "GET", Path: "/b", Response: collide()},
		}, `schema name "thing"`},
		{"unsupported result field", []api.Route{
			{Method: "POST", Path: "/a", Accepted: true, Result: unsupported{}},
		}, "type is not supported"},
		{"duplicate operation", []api.Route{
			{Method: "GET", Path: "/a", Response: other{}},
			{Method: "GET", Path: "/a", Response: other{}},
		}, "already exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.routes)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestOperationID(t *testing.T) {
	for path, want := range map[string]string{
		"/api/v1/app/save-app":    "appSaveApp",
		"/api/v1/sse":             "sse",
		"/_/health":               "health",
		"/auth/local/login":       "authLocalLogin",
		"/avatar/{avatar}":        "avatar",
		"/api/v1/app/get-apps--x": "appGetAppsX",
	} {
		if got := operationID(path); got != want {
			t.Errorf("operationID(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestYAMLReportsBuildErrors(t *testing.T) {
	if _, err := YAML([]api.Route{{Method: "POST", Path: "/a", Accepted: true}}); err == nil {
		t.Fatal("YAML over an invalid table returned no error")
	}
}

func TestYAMLDeterministic(t *testing.T) {
	a, err := YAML(sample())
	if err != nil {
		t.Fatal(err)
	}
	b, err := YAML(sample())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("two runs over the same routes differ")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/web/api/openapi/`
Expected: FAIL with `no required module provides package github.com/getkin/kin-openapi/openapi3`. The package has no non-test files yet, so the missing module is the first error.

- [ ] **Step 3: Implement the generator**

Create `internal/app/web/api/openapi/openapi.go`:

```go
// Package openapi renders the api route table into an OpenAPI 3.1 document
// (api/openapi.yaml, written by cmd/openapi). It is the one place that knows
// the wire envelope: every JSON response is wrapped exactly the way
// internal/app/web/util writes it.
package openapi

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/swaggest/jsonschema-go"
	oapi "github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"

	"winterflow/internal/app/web/api"
)

// Security scheme names. Every User route accepts either.
const (
	SchemeBearer = "bearer"
	SchemeCookie = "cookie"
)

// ResultExtension is the operation extension that names, as a $ref, the
// payload of the SSE operation_result an Accepted route eventually produces.
const ResultExtension = "x-operation-result"

const componentsSchemas = "#/components/schemas/"

// Null is the schema of `data` when a route answers without a payload:
// webutil writes "data":null.
type Null struct{}

// JSONSchema implements jsonschema.Exposer.
func (Null) JSONSchema() (jsonschema.Schema, error) {
	var s jsonschema.Schema
	s.AddType(jsonschema.Null)
	return s, nil
}

// Error is the body of every webutil error: util.Error (400),
// util.Unauthorized (401), util.Forbidden and rbac.RequireAdmin (403).
type Error struct {
	Success bool   `json:"success" required:"true" const:"false"`
	Message string `json:"message" required:"true"`
	Data    Null   `json:"data" required:"true"`
}

// LibraryError is the error body of the go-pkgz /auth and /avatar routes.
type LibraryError struct {
	Error string `json:"error" required:"true"`
}

// AcceptedData is the `data` of every 202: the request id that the SSE
// operation_result will carry as its ref.
type AcceptedData struct {
	RequestID string `json:"request_id" required:"true"`
}

// envelope returns a value of an unnamed struct type shaped like
// util.APIResponse, with Data typed as data's type (Null when data is nil).
// Unnamed, so the schema is inlined per operation while Data's type stays a
// shared component.
func envelope(data any) any {
	dt := reflect.TypeOf(Null{})
	if data != nil {
		dt = reflect.TypeOf(data)
	}
	t := reflect.StructOf([]reflect.StructField{
		{Name: "Success", Type: reflect.TypeOf(true), Tag: `json:"success" required:"true" const:"true"`},
		{Name: "Message", Type: reflect.TypeOf(""), Tag: `json:"message,omitempty"`},
		{Name: "Data", Type: dt, Tag: `json:"data" required:"true"`},
	})
	return reflect.New(t).Elem().Interface()
}

// builder holds one generation run.
type builder struct {
	r *openapi31.Reflector
	// names maps each component name to the Go type that claimed it, so two
	// types with the same name fail loudly instead of one silently
	// shadowing the other.
	names map[string]reflect.Type
	err   error
}

// Build renders routes into an OpenAPI 3.1 document.
func Build(routes []api.Route) (*openapi31.Spec, error) {
	b := &builder{r: openapi31.NewReflector(), names: map[string]reflect.Type{}}
	b.r.DefaultOptions = append(b.r.DefaultOptions, jsonschema.InterceptDefName(b.defName))

	s := b.r.SpecEns()
	s.SetTitle("Winterflow API")
	s.SetVersion("1")
	s.SetDescription("Generated from the route tables in internal/app/web by `make openapi`; do not edit.\n\n" +
		"JSON responses use the envelope {success, message, data}. 202 responses carry data.request_id; " +
		"the outcome arrives on GET /api/v1/sse as an operation_result Notification whose ref is that " +
		"request_id and whose payload matches the operation's x-operation-result schema.")
	s.SetHTTPBearerTokenSecurity(SchemeBearer, "wfp_ personal access token",
		"Personal access token (wfp_…) as `Authorization: Bearer`. It is also accepted as the Basic-auth password.")
	s.SetAPIKeySecurity(SchemeCookie, "JWT", oapi.InCookie,
		"JWT session cookie set by POST /auth/local/login (the X-JWT header is accepted too).")

	for _, rt := range routes {
		if err := b.addRoute(rt); err != nil {
			return nil, fmt.Errorf("%s %s: %w", rt.Method, rt.Path, err)
		}
	}
	if b.err != nil {
		return nil, b.err
	}
	return s, nil
}

// defName names components after the bare Go type ("SaveAppRequest", not
// the package-prefixed "ServerSaveAppRequest") and records clashes. The
// reflector adds its own "FormData" prefix for form bodies afterwards.
func (b *builder) defName(t reflect.Type, _ string) string {
	name := t.Name()
	if prev, ok := b.names[name]; ok && prev != t && b.err == nil {
		b.err = fmt.Errorf("schema name %q used by both %s and %s", name, prev, t)
	}
	b.names[name] = t
	return name
}

func (b *builder) addRoute(rt api.Route) error {
	oc, err := b.r.NewOperationContext(rt.Method, rt.Path)
	if err != nil {
		return err
	}
	oc.SetID(operationID(rt.Path))
	oc.SetSummary(rt.Summary)
	if rt.Tag != "" {
		oc.SetTags(rt.Tag)
	}
	if rt.Auth == api.User {
		oc.AddSecurity(SchemeBearer)
		oc.AddSecurity(SchemeCookie)
	}
	if rt.Query != nil {
		oc.AddReqStructure(rt.Query)
	}
	if rt.Request != nil {
		oc.AddReqStructure(rt.Request)
	}
	if rt.Form != nil {
		oc.AddReqStructure(rt.Form, oapi.WithContentType("application/x-www-form-urlencoded"))
	}

	switch {
	case rt.Events != nil:
		oc.AddRespStructure(rt.Events, oapi.WithContentType("text/event-stream"), oapi.WithHTTPStatus(http.StatusOK))
	case rt.Raw:
		oc.AddRespStructure(rt.Response, oapi.WithContentType(rt.ContentType), oapi.WithHTTPStatus(http.StatusOK))
	case rt.Accepted:
		oc.AddRespStructure(envelope(AcceptedData{}), oapi.WithHTTPStatus(http.StatusAccepted))
	default:
		oc.AddRespStructure(envelope(rt.Response), oapi.WithHTTPStatus(http.StatusOK))
	}
	for _, code := range errorCodes(rt) {
		addError(oc, code, rt.Raw)
	}

	if rt.Accepted {
		if rt.Result == nil {
			return fmt.Errorf("accepted route has no Result")
		}
		ref, err := b.component(rt.Result)
		if err != nil {
			return err
		}
		oc.(openapi31.OperationExposer).Operation().
			WithMapOfAnythingItem(ResultExtension, map[string]any{"$ref": ref})
	}
	return b.r.AddOperation(oc)
}

// errorCodes is rt.Errors plus 401 on User routes, sorted and de-duplicated.
func errorCodes(rt api.Route) []int {
	codes := append([]int(nil), rt.Errors...)
	if rt.Auth == api.User {
		codes = append(codes, http.StatusUnauthorized)
	}
	sort.Ints(codes)
	out := codes[:0]
	for i, c := range codes {
		if i == 0 || codes[i-1] != c {
			out = append(out, c)
		}
	}
	return out
}

// addError documents one error status. 401 comes from webutil (JSON) or
// from the go-pkgz JWT middleware (text/plain "Unauthorized"); 429 is the
// rate limiter's text/plain body; library routes answer {"error": "..."}.
func addError(oc oapi.OperationContext, code int, raw bool) {
	switch {
	case code == http.StatusTooManyRequests:
		oc.AddRespStructure("", oapi.WithContentType("text/plain"), oapi.WithHTTPStatus(code))
	case raw:
		oc.AddRespStructure(LibraryError{}, oapi.WithHTTPStatus(code))
	case code == http.StatusUnauthorized:
		oc.AddRespStructure(Error{}, oapi.WithHTTPStatus(code))
		oc.AddRespStructure("", oapi.WithContentType("text/plain"), oapi.WithHTTPStatus(code))
	default:
		oc.AddRespStructure(Error{}, oapi.WithHTTPStatus(code))
	}
}

// component reflects v into components/schemas and returns its $ref.
func (b *builder) component(v any) (string, error) {
	s, err := b.r.Reflect(v,
		jsonschema.RootRef,
		jsonschema.DefinitionsPrefix(componentsSchemas),
		jsonschema.CollectDefinitions(func(name string, schema jsonschema.Schema) {
			comps := b.r.SpecEns().ComponentsEns()
			if _, ok := comps.Schemas[name]; ok {
				return
			}
			sm, err := schema.ToSchemaOrBool().ToSimpleMap()
			if err != nil && b.err == nil {
				b.err = err
			}
			comps.WithSchemasItem(name, sm)
		}),
	)
	if err != nil {
		return "", err
	}
	if s.Ref == nil {
		return "", fmt.Errorf("result type %T is not a named type", v)
	}
	return *s.Ref, nil
}

// operationID derives a stable id from the path: /api/v1/app/save-app ->
// appSaveApp, /_/health -> health, /avatar/{avatar} -> avatar.
func operationID(path string) string {
	path = strings.TrimPrefix(path, "/api/v1")
	var out strings.Builder
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "_" || strings.HasPrefix(seg, "{") {
			continue
		}
		for _, word := range strings.Split(seg, "-") {
			if word == "" {
				continue
			}
			if out.Len() == 0 {
				out.WriteString(word)
				continue
			}
			out.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}
	return out.String()
}

// YAML renders routes as the committed api/openapi.yaml bytes.
func YAML(routes []api.Route) ([]byte, error) {
	s, err := Build(routes)
	if err != nil {
		return nil, err
	}
	return s.MarshalYAML()
}
```

Notes for the implementer, verified against v0.2.61:
- The extension is set on `oc.(openapi31.OperationExposer).Operation()` **before** `AddOperation`. `AddOperation` copies the operation into the path item, so setting it afterwards is lost.
- Calling `AddRespStructure` twice with the same status merges both content types into one response (the 401 JSON + text/plain pair).
- `jsonschema-go` already makes slices and pointers nullable unless they are `omitempty` (`"claims":null` validates). Do **not** enable `EnvelopNullability`: v0.3.79 then emits `anyOf:[null,$ref]` next to `type: object`, which rejects `null`.

- [ ] **Step 4: Add the modules, then run the tests**

```bash
go get github.com/swaggest/openapi-go@v0.2.61 github.com/swaggest/jsonschema-go@v0.3.79 github.com/getkin/kin-openapi@v0.149.0
go mod tidy
git diff go.mod
go test ./internal/app/web/api/openapi/
go vet ./internal/app/web/api/openapi/
```

Expected: `go.mod` gains the three direct `require` lines and the indirect ones listed in Global Constraints, and nothing else. Tests `ok`, vet silent. Run `go mod tidy` only after the imports exist; otherwise it drops the modules again.

- [ ] **Step 5: Check licenses and binary isolation**

```bash
for m in github.com/swaggest/openapi-go@v0.2.61 github.com/swaggest/jsonschema-go@v0.3.79 github.com/swaggest/refl@v1.4.0 github.com/getkin/kin-openapi@v0.149.0 github.com/oasdiff/yaml@v0.1.1 github.com/oasdiff/yaml3@v0.0.14 github.com/santhosh-tekuri/jsonschema/v6@v6.0.3 github.com/go-openapi/jsonpointer@v0.22.5 github.com/go-openapi/swag/jsonname@v0.25.5 gopkg.in/yaml.v2@v2.4.0; do
  printf '%s: ' "$m"; head -c 120 "$(go env GOMODCACHE)/$m/LICENSE" | tr '\n' ' '; echo
done
go list -deps ./cmd/standalone ./cmd/api | grep -E 'swaggest|kin-openapi' || echo "server binaries clean"
```

Expected: every license line reads MIT or Apache (`oasdiff/yaml3` says "MIT and Apache"). The last line prints `server binaries clean`. Any GPL/AGPL hit is a blocker: stop and report it.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/app/web/api/openapi
git commit -m "$(printf 'feat(web): generate OpenAPI 3.1 from the route table\n\nswaggest/openapi-go (MIT) reflects the table; kin-openapi (MIT) validates\nit in tests. Responses are wrapped in the webutil envelope, errors share one\nError schema, and 202 routes name their SSE result in x-operation-result.\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 7: `cmd/openapi`, the committed `api/openapi.yaml` and the document tests

**Files:**
- Create: `cmd/openapi/main.go`
- Create: `api/openapi.yaml` (generated)
- Test: `internal/app/web/openapi_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `web.APIRoutes()` (Task 5); `openapi.YAML`, `openapi.ResultExtension` (Task 6); `api.User` (Task 2).
- Produces:
  - `go run ./cmd/openapi` prints the YAML to stdout and exits 1 with `openapi: <err>` on failure.
  - `make openapi` writes `api/openapi.yaml` atomically.
  - `api/openapi.yaml` is the contract PR 3 validates against.

- [ ] **Step 1: Write the failing tests**

Create `internal/app/web/openapi_test.go`:

```go
package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"winterflow/internal/app/web/api"
	"winterflow/internal/app/web/api/openapi"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// specPath is the committed document, relative to this package.
var specPath = filepath.Join("..", "..", "..", "api", "openapi.yaml")

func TestOpenAPIUpToDate(t *testing.T) {
	want, err := openapi.YAML(APIRoutes())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v (run make openapi)", specPath, err)
	}
	if string(got) != string(want) {
		t.Fatal("api/openapi.yaml is stale: run make openapi and commit the result")
	}
}

// loadSpec parses and validates the committed document with kin-openapi.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load %s: %v", specPath, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate %s: %v", specPath, err)
	}
	return doc
}

func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	doc := loadSpec(t)
	for _, rt := range APIRoutes() {
		item := doc.Paths.Find(rt.Path)
		if item == nil || item.GetOperation(rt.Method) == nil {
			t.Errorf("%s %s is in the route table but not in the spec", rt.Method, rt.Path)
			continue
		}
		op := item.GetOperation(rt.Method)
		_, hasResult := op.Extensions[openapi.ResultExtension]
		if rt.Accepted != hasResult {
			t.Errorf("%s %s: accepted=%v but x-operation-result present=%v", rt.Method, rt.Path, rt.Accepted, hasResult)
		}
		if rt.Accepted && op.Responses.Status(http.StatusAccepted) == nil {
			t.Errorf("%s %s: accepted route without a 202 response", rt.Method, rt.Path)
		}
		if secured := op.Security != nil && len(*op.Security) > 0; secured != (rt.Auth == api.User) {
			t.Errorf("%s %s: security present=%v, auth=%v", rt.Method, rt.Path, secured, rt.Auth)
		}
	}
	sse := doc.Paths.Find("/api/v1/sse").Get.Responses.Status(http.StatusOK).Value.Content
	if mt := sse.Get("text/event-stream"); mt == nil || mt.Schema.Ref != "#/components/schemas/Notification" {
		t.Errorf("SSE route must document text/event-stream with the Notification schema, got %+v", sse)
	}
}

// TestOpenAPIMatchesWire writes real webutil responses and validates the
// bytes against the committed schemas, so the envelope in the document is
// the envelope on the wire.
func TestOpenAPIMatchesWire(t *testing.T) {
	doc := loadSpec(t)
	cases := []struct {
		name, method, path string
		status             int
		contentType        string
		write              func(w http.ResponseWriter)
	}{
		{"success without data", http.MethodGet, "/_/health", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "healthy", nil)
		}},
		{"success with data", http.MethodGet, "/api/v1/auth/state", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "auth state", struct {
				Bootstrap           bool `json:"bootstrap"`
				RegistrationEnabled bool `json:"registration_enabled"`
			}{true, true})
		}},
		{"success with empty message", http.MethodGet, "/api/v1/domains/check", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "", struct {
				Available bool                `json:"available"`
				Claims    []model.DomainClaim `json:"claims"`
			}{Available: true})
		}},
		{"nil list", http.MethodGet, "/api/v1/user/get-tokens", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "tokens", []model.UserToken(nil))
		}},
		{"accepted", http.MethodPost, "/api/v1/app/save-app", 202, "application/json", func(w http.ResponseWriter) {
			util.Accepted(w, "app save dispatched", struct {
				RequestID string `json:"request_id"`
			}{"req-1"})
		}},
		{"bad request", http.MethodPost, "/api/v1/app/save-app", 400, "application/json", func(w http.ResponseWriter) {
			util.Error(w, "server_id is required", nil)
		}},
		{"unauthorized", http.MethodGet, "/api/v1/server/get-servers", 401, "application/json", func(w http.ResponseWriter) {
			util.Unauthorized(w)
		}},
		{"go-pkgz unauthorized", http.MethodGet, "/api/v1/server/get-servers", 401, "text/plain", func(w http.ResponseWriter) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}},
		{"forbidden", http.MethodGet, "/api/v1/registry/list", 403, "application/json", func(w http.ResponseWriter) {
			util.Forbidden(w)
		}},
		{"rate limited", http.MethodPost, "/api/v1/auth/register", 429, "text/plain", func(w http.ResponseWriter) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.write(rec)
			if rec.Code != tc.status {
				t.Fatalf("writer status = %d, want %d", rec.Code, tc.status)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.contentType) {
				t.Fatalf("writer Content-Type = %q, want %s", ct, tc.contentType)
			}
			resp := doc.Paths.Find(tc.path).GetOperation(tc.method).Responses.Status(tc.status)
			if resp == nil {
				t.Fatalf("%s %s documents no %d", tc.method, tc.path, tc.status)
			}
			mt := resp.Value.Content.Get(tc.contentType)
			if mt == nil {
				t.Fatalf("%s %s %d documents no %s body", tc.method, tc.path, tc.status, tc.contentType)
			}
			var body any = rec.Body.String()
			if tc.contentType == "application/json" {
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body is not JSON: %v", err)
				}
			}
			if err := mt.Schema.Value.VisitJSON(body, openapi3.EnableJSONSchema2020()); err != nil {
				t.Fatalf("wire bytes %s do not match the schema: %v", rec.Body.String(), err)
			}
		})
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/web/ -run TestOpenAPI`
Expected: FAIL. `TestOpenAPIUpToDate` reports `read ../../../api/openapi.yaml: open ...: no such file or directory (run make openapi)`, and the other two report `load ../../../api/openapi.yaml: ...`.

- [ ] **Step 3: Add the command and the Makefile target**

Create `cmd/openapi/main.go`:

```go
// Command openapi prints the OpenAPI 3.1 document generated from the route
// tables. `make openapi` redirects it into api/openapi.yaml.
package main

import (
	"fmt"
	"os"

	"winterflow/internal/app/web"
	"winterflow/internal/app/web/api/openapi"
)

func main() {
	out, err := openapi.YAML(web.APIRoutes())
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}
```

In `Makefile`, change the `.PHONY` line from:

```make
.PHONY: build lint standalone api web web-build third-party-licenses test web-test cover diffcov
```

to:

```make
.PHONY: build lint standalone api web web-build third-party-licenses test web-test cover diffcov openapi
```

Then add this target after the `diffcov` target (tab-indented recipe):

```make
# Regenerate api/openapi.yaml from the route tables in internal/app/web.
# TestOpenAPIUpToDate fails until the committed file matches.
openapi:
	$(GO) run ./cmd/openapi > api/openapi.yaml.tmp || { rm -f api/openapi.yaml.tmp; exit 1; }
	mv api/openapi.yaml.tmp api/openapi.yaml
```

- [ ] **Step 4: Generate the document and run the tests**

```bash
mkdir -p api
make openapi
grep -c 'operationId:' api/openapi.yaml
grep -c 'x-operation-result:' api/openapi.yaml
head -3 api/openapi.yaml
go test ./internal/app/web/...
```

Expected: `49` operations (43 registered + 6 Mounted), `20` `x-operation-result` (10 app + 10 docker), and the first line is `openapi: 3.1.0`. All tests pass: `TestOpenAPIUpToDate`, `TestOpenAPIDocumentsEveryRoute`, all 10 `TestOpenAPIMatchesWire` subtests, and the unchanged `TestRoutesGolden`.

Spot-check one operation by eye: `sed -n '/^  \/api\/v1\/app\/save-app:/,/^  \/api\/v1\/auth/p' api/openapi.yaml`. It must show `requestBody` `$ref SaveAppRequest`, a `202` envelope with `data: $ref AcceptedData`, `400`/`403` `$ref Error`, `401` with both `application/json` and `text/plain`, `security: [bearer, cookie]`, and `x-operation-result: $ref '#/components/schemas/SaveAppResponse'`.

- [ ] **Step 5: Prove the up-to-date check bites**

```bash
printf '\n# drift\n' >> api/openapi.yaml
go test ./internal/app/web/ -run TestOpenAPIUpToDate; echo "exit=$?"
make openapi
go test ./internal/app/web/ -run TestOpenAPIUpToDate
```

Expected: the first run FAILs with `api/openapi.yaml is stale: run make openapi and commit the result` and `exit=1`. After `make openapi` it passes again.

- [ ] **Step 6: Commit**

```bash
git add cmd/openapi api/openapi.yaml internal/app/web/openapi_test.go Makefile
git commit -m "$(printf 'feat(api): commit the generated OpenAPI document and keep it current\n\nmake openapi writes api/openapi.yaml; TestOpenAPIUpToDate fails on drift,\nthe document validates with kin-openapi, and real webutil output is\nvalidated against it.\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 8: Docs, coverage baseline and full verification

**Files:**
- Modify: `AGENTS.md`, `CLAUDE.md`
- Modify: `coverage-baseline.json` (only if the ratchet asks)

**Interfaces:**
- Consumes: everything above.
- Produces: contributor docs that point new routes at the table and `make openapi`.

- [ ] **Step 1: Update `AGENTS.md`**

In the Architecture paragraph, replace:

```
Add a feature by: defining a command type + payload struct, adding an agent handler in `internal/app/agent`, and adding a usecase + route on the API side.
```

with:

```
Add a feature by: defining a command type + payload struct, adding an agent handler in `internal/app/agent`, and adding a usecase + route on the API side. A route is one `api.Route` entry in its handler package's `Routes()` (`internal/app/web/api`): the same table registers it with chi and generates `api/openapi.yaml`, so run `make openapi` and commit the result.
```

In "Build, Test, and Development Commands", after the `make cover` / `make diffcov` bullet, add:

```
- `make openapi` – regenerates `api/openapi.yaml` (OpenAPI 3.1) from the route tables; `TestOpenAPIUpToDate` fails until the committed file matches.
```

(If another branch has reworded these exact sentences, keep their wording and add the same information next to it.)

- [ ] **Step 2: Update `CLAUDE.md`**

In "Commands", after the bullet that starts ``- `make grpc` — regenerate Go from``, add:

```
- `make openapi` — regenerate `api/openapi.yaml` from the route tables (`go run ./cmd/openapi`); `TestOpenAPIUpToDate` fails until it is committed.
```

In "Architecture", replace:

```
and add a usecase/handler + route on the API side.
```

with:

```
and add a usecase/handler + route on the API side. Routes live in typed tables: each handler package's `Routes(g api.Guards) []api.Route` (`internal/app/web/api`) is both what `routes.go` registers with chi (`api.Register`, fail-closed on nil guards) and what `internal/app/web/api/openapi` renders into `api/openapi.yaml`; admin-only routes carry `g.Admin` in their `Middleware`. Run `make openapi` after any route or response-type change.
```

- [ ] **Step 3: Full verification**

```bash
unformatted="$(gofmt -l $(git ls-files '*.go'))"; [ -z "$unformatted" ] && echo "gofmt clean" || echo "$unformatted"
go vet ./...
cp go.mod /tmp/go.mod.before && go mod tidy && diff /tmp/go.mod.before go.mod && echo "tidy stable"
go list -deps ./cmd/standalone ./cmd/api | grep -E 'swaggest|kin-openapi' || echo "server binaries clean"
make test
make web-test
git diff --exit-code internal/app/web/testdata/routes.golden && echo "golden unchanged since Task 1"
git log --oneline ci/tests-and-coverage..HEAD -- internal/app/web/testdata/routes.golden
make diffcov DIFFCOV_BASE=ci/tests-and-coverage
go run ./tools/diffcov ratchet --baseline coverage-baseline.json --go coverage.out --lcov web/coverage/lcov.info
```

Expected: `gofmt clean`, vet silent, `tidy stable`, `server binaries clean`, `make test` green (it runs with `-race`), and `make web-test` green (web is untouched). The golden is unchanged, and the `git log` lists exactly one commit, Task 1's. diffcov prints `go ... PASS` (≈98–99%; the only expected uncovered lines are the moved SPA branch in `routes.go` and the `ToSimpleMap` error branch in `openapi.go`) and `web  no executable changed lines  PASS`. The ratchet passes.

- [ ] **Step 4: Raise the baseline if asked**

If the ratchet printed `::notice::raise coverage-baseline.json go to <value>` (expected: the Go total rises from 61.5 to about 63.8), set `"go"` in `coverage-baseline.json` to exactly `<value>`, leave `"web"` alone, and re-run the ratchet command from Step 3 to confirm it passes without a notice. If no notice was printed, leave the file unchanged.

- [ ] **Step 5: Commit**

```bash
git add AGENTS.md CLAUDE.md coverage-baseline.json
git commit -m "$(printf 'docs: document the route table and make openapi\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

- [ ] **Step 6: Note for the PR description (do not push from this plan)**

The PR body must state:
- There is no behaviour change. Registered routes and middleware are pinned by `internal/app/web/testdata/routes.golden`, captured before the refactor and unchanged since. Responses are byte-identical; only Go type names changed.
- The new dependencies and their licenses (all MIT/Apache-2.0). The generator and kin-openapi are not linked into the server binaries.
- What PR 3 consumes: `api/openapi.yaml`, the `x-operation-result` extension and `api.Route.Mounted`.
- PR 4 (remove roles) touches the `g.Admin` entries in `handler/{docker,org,server}/routes.go`, those packages' `TestRoutes`, and `api.Guards.Admin`.
- The suites that were run: `go test -race ./...`, `pnpm --dir web test`, `make diffcov`, and the ratchet.

---

## Self-Review

**Spec coverage (PR 2 section):**
- Package `internal/app/web/api` with `Auth` (`Public`/`User`) and `Route` (all spec fields: Method, Path, Handler, Auth, Middleware, Summary, Tag, Query, Request, Response, Accepted, Result, Errors) → Task 2. The extra fields and why they exist → "Resolved spec ambiguities".
- Each handler package exports `Routes()` → Task 4 (signature takes `api.Guards`, see ambiguities).
- Request/response structs exported, renamed minimally → Task 3 (full rename map in its Interfaces block).
- `routes.go` keeps building authMW, adminMW, rate limiter and CORS (CORS stays global in `registerMiddleware`, untouched), then registers each route as `With(chain...).Method(...)` → Task 5.
- Registered paths and middleware identical, checked by a `chi.Walk` golden captured before the refactor → Task 1 (capture) and Task 5 (unchanged), plus `TestRouteTableMatchesRouter`.
- Non-JSON/library routes: health and SSE are table routes with handlers. The go-pkgz `/auth/*` and avatar endpoints are `Handler: nil, Mounted: true`, documented and not re-registered → Task 4 (`auth.Routes`), Task 2 (`Register` skips them). The SPA is not a route (ambiguities).
- swaggest `openapi31` reflector (MIT) → Task 6. Envelope matches `util.APIResponse` bytes (`success`, `message` omitempty, `data` always present) → Task 6 (`envelope`) + Task 7 (`TestOpenAPIMatchesWire`).
- One shared `Error` schema matching `webutil.Error`/`Unauthorized`/`Forbidden` → Task 6, validated against real bytes in Task 7.
- Accepted routes: 202 with `data: {request_id}` and `x-operation-result: {$ref: '#/components/schemas/<Result>'}` → Task 6, checked per route in Task 7.
- SSE documents `text/event-stream` with the `Notification` component → Tasks 4, 6 and 7.
- Security schemes `bearer` + `cookie`; User routes require either → Task 6, checked per route in Task 7.
- `go run ./cmd/openapi > api/openapi.yaml` and `make openapi` → Task 7.
- `TestOpenAPIUpToDate` fails with "run make openapi" on mismatch → Task 7 (Step 5 proves it).
- Document validates with kin-openapi `openapi3` loader `Validate` (test-only, MIT) → Tasks 6 and 7.
- Every new Go line covered (PR 1's 80% diff gate) → per-package tests in Tasks 2–7. Measured at 98.9%; the remaining lines are named in Task 8 Step 3.

**Placeholder scan:** every code step gives the full file or an exact old → new replacement. The only values left to the implementer are measured: the Task 1 golden (captured, by design) and the coverage baseline (Task 8 Step 4, as PR 1 requires).

**Type consistency:** `api.Route`/`api.Guards`/`api.Middleware`/`api.Register`/`api.ServerQuery` (Task 2) are used with the same names in Tasks 4–7. `Routes(g api.Guards) []api.Route` is the same in all seven handler packages. `auth.Routes() []api.Route` is used in Task 5. `APIRoutes()`/`routeTable(d, g)` (Task 5) are used by Task 7. `openapi.Build`/`YAML`/`ResultExtension`/`SchemeBearer`/`SchemeCookie` (Task 6) are used by Task 7. The exported handler type names in Task 3's Interfaces block match the tables in Task 4 and the tests in Tasks 3 and 7. All of this was compiled and tested together in a scratch copy of the base branch (`go test -race ./...`, `go vet ./...`, gofmt, `go mod tidy`, diffcov).

**Review Focus:** each of the five lines has a pinning test in its owning task: `TestRegisterFailsClosed` + per-package `TestRoutes` (Tasks 2, 4); the null/nil cases in `TestEnvelopeSchemas` and `TestOpenAPIMatchesWire` (Tasks 6, 7); the text/plain 401/429 cases (Tasks 6, 7); `TestBuildErrors/name clash` (Task 6); `TestOpenAPIUpToDate` + `TestYAMLDeterministic` (Tasks 7, 6).
