# CI PR 3 — API Contract Suite on SQLite and Postgres Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every route in the typed route table is exercised end to end — real router, real middleware, real database, real `InProcessBridge` — against the committed `api/openapi.yaml`, on SQLite and on Postgres, with a golden transcript proving both databases answer the same.

**Architecture:** A test-only package `internal/app/web/contract` (every file is `_test.go`, package `contract_test`) builds one isolated stack per top-level test: a fresh database (SQLite temp file, or a new Postgres schema selected with `search_path` when `WF_TEST_DATABASE_URL` is set), `bootstrap.WireCore` over an in-memory bus, the real `appagent.InProcessBridge` in front of a scripted fake agent (answers through the real codec), and `web.NewServer(...).Handler` wrapped in a hit recorder behind `httptest`. Clients (`claim()`, `pat()`, `member()`, `anon()`) validate every request and response with `kin-openapi` `openapi3filter` against `api/openapi.yaml`; agent-bound routes are followed through SSE to their `operation_result`, whose payload is validated against the route's `x-operation-result` component. `TestMain` fails the run when any table route was never called. `TestParity` compares a normalized transcript with one golden file that both databases must reproduce. CI gains a `contract` matrix job (`db: [sqlite, postgres]`, `postgres:16` service) whose Postgres coverage profile is merged into the diff gate. Four small production changes make the stack testable and fix what the suite found on Postgres.

**Tech Stack:** Go 1.25 (module `winterflow`), chi v5.2.5, go-pkgz/auth v2.0.0, bun v1.2.15 (`sqliteshim`, `pgdriver`), `github.com/getkin/kin-openapi` v0.149.0 (`openapi3`, `openapi3filter`, `routers` — already required by PR 2, test-only), Docker `postgres:16` (CI service and `make test-pg`).

**Spec:** `docs/superpowers/specs/2026-10-10-ci-and-api-contracts-design.md`, section "PR 3 — API contract suite on SQLite and Postgres" only. Its base, PR 2 (`ci/openapi-from-code`: route table, generator, committed `api/openapi.yaml` with `x-operation-result`, required response fields, `*Input` request components, `ServerStatusPayload`/`AppsStatusPayload` SSE payload rules), is assumed merged into the branch point.

**Branch:** create `ci/contract-suite` from the tip of `ci/openapi-from-code` (`git switch -c ci/contract-suite ci/openapi-from-code`). The local diff gate for this stacked branch is `make diffcov DIFFCOV_BASE=ci/openapi-from-code`; note that locally it sees SQLite coverage only (see Task 12).

**Verified at plan time:** every file and edit in this plan was applied to a scratch copy of `ci/openapi-from-code` at `4de22f4` (after PR 2's fix wave). Results: `go build ./...`, `go vet ./...` and `gofmt -l` clean; `go test -race -coverpkg=./... ./...` green (85 s wall, same as without `-coverpkg`); the contract package alone takes 12–25 s with `-race` and 1–3 s without, on SQLite and on Postgres 16 (`docker run postgres:16`), five consecutive `-race` runs green after the go-pkgz race workaround; `TestParity` produced the same golden on both databases (five repeated Postgres runs). The coverage guard was proven by deleting the `/_/health` call (it fails listing `GET /_/health`) and found a real recorder bug (handlers that never write were not recorded — fixed in Task 3's `record`). `tools/diffcov` on the change: 24/31 changed Go lines (77.4%) with the SQLite profile alone, **29/31 (93.5%) with the Postgres contract profile merged** — the merge in Task 12 is required, not optional. Go total coverage moved from 64.9% to 74.3% (merged, `-coverpkg=./...`).

**Postgres findings (each fixed here; the parity allowlist is empty):**
1. `app_domains.app_id`/`server_id` are `CHAR(36)` while `apps.app_id`/`servers.server_id` are `UUID`. `FindClaims` joins them, so on Postgres every `save-app` whose config has an ingress domain failed: `ERROR: operator does not exist: uuid = character (SQLSTATE=42883)` → 400 "failed to save app". `GET /domains/check` with a claimed domain failed the same way. Fix: a Postgres-only migration retyping both columns to `UUID` (Task 7).
2. `types.DateTime` writes naive UTC wall-clock strings (`"2006-01-02 15:04:05"`) into `TIMESTAMPTZ` columns, which Postgres reads in the **session** time zone. With `timezone=America/New_York` every stored timestamp came back 4 h early (`created_at 18:33:07 -0400`, skew −3h59m59s). The official image defaults to UTC, which hid it. Fix: `db.utcDSN` pins `timezone=UTC` on every Postgres connection; the harness deliberately connects with a non-UTC zone so the fix stays pinned (Task 6).
3. List queries order by `created_at`, which has one-second precision; rows created in the same second come back in storage order, which differs between engines (SQLite: rowid; Postgres: heap order, which an `UPDATE` can change). Fix: primary-key tie-breaks in `GetApps`, `GetServers`, `ListMembers`, `ListTokens` (Task 10). Pinned on SQLite by `TestGetAppsOrdersTiesByID`, which fails without the fix.

**Other findings the suite surfaced (fixed here):**
- go-pkgz writes `WriteHeader` before `rest.RenderJSON` sets `Content-Type` on some paths (`/auth/user` 401, unknown provider 400), so `{"error": …}` arrives as sniffed `text/plain; charset=utf-8`. The PR 2 document only allowed `application/json` there. Fix: the generator documents library errors under both media types (Task 4).
- `go-pkgz/auth/v2/token.NewService` writes a package global through a function-local `sync.Once`, so two `web.NewServer` calls in parallel tests are a data race (`-race` failed 1 run in 4). Production builds one server, so the harness serializes `NewServer` with a mutex instead of patching the library (Task 3).
- The SSE handler only sent headers with the first event, so a client could not know when it was subscribed; a command dispatched right after opening the stream could finish before `AddChannel` and its result was dropped (results are not buffered). Fix: flush headers right after `AddChannel` (Task 2) — also gives browsers an immediate `EventSource.onopen`.
- The contract package is test-only, so without `-coverpkg` it contributes **nothing** to coverage (CI runs `go test -coverprofile ./...` per package). The spec's "Coverage interplay" requires it to count; Task 12 adds `-coverpkg=./...`.

**Resolved spec ambiguities (decided here):**
- `web.NewServer(deps).Router` in the spec is `web.NewServer(ctx, deps).Handler` (it returns `*http.Server`; the handler is the chi router). The harness wraps it in its recorder and serves it with `httptest.NewUnstartedServer` + `BaseContext` = the test context, so cancelling ends SSE streams before `Close`.
- "The same constructors BootstrapStandalone uses": `bootstrap.wireCore` is exported as `WireCore` (no wrapper, no factory) and the harness calls it, then `appagent.NewInProcessBridge`. Certs, Docker, ingress, the status reporter and the source poller are not started; the pending embedded-server registration `BootstrapStandalone` creates is seeded directly with `ServerRepository.RegisterServer` (no cert generation), so the first login auto-claims it exactly as in production.
- "The fake agent implements the transport dispatcher interface": `InProcessBridge` now takes `appagent.CommandRunner` (same method set as `grpc/agent.Dispatcher`); the fake asserts both at compile time.
- Topology is `standalone` (as `BootstrapStandalone`). A second organization for the 403 probes cannot be created over HTTP in standalone (registration closes after the claim), so `env.foreignServer` seeds it through the same repository calls the handlers use (`RegisterLocalUser`, `RegisterServer`, `ClaimServer`).
- Fresh DB "per test" = per top-level `Test*` function; its subtests share the stack sequentially. Top-level tests run `t.Parallel()`. Postgres pools are capped at 4 connections per test.
- Process-wide env (`JWT_SECRET`, `WEB_URL`, `AVATARS_STORAGE_PATH`, …) is set once in `TestMain` with `os.Setenv` (not `t.Setenv`, which forbids `t.Parallel`). The database URL never goes through env: the harness passes it to `db.NewBunConnection` directly.
- Request validation looks the operation up from the route table (`routeFor`) and builds `routers.Route` itself — no gorilla/legacy router dependency. `openapi3filter.NoopAuthenticationFunc` (auth is asserted by status codes); `IncludeResponseStatus: true` (an undocumented status such as 500 fails). Deliberately malformed requests set `call.Invalid` to skip pre-send validation; their responses are still validated. `text/event-stream` bodies are excluded from `ValidateResponse`; instead every `data:` event of every stream is validated against the `Notification` component at cleanup, and each awaited success payload against the component named by `x-operation-result` (looked up in `components.schemas` by stripping `#/components/schemas/`, since kin-openapi does not resolve refs inside extensions).
- `awaitResult(t, client, requestID)` takes the client whose stream was opened **before** dispatch (`client.accepted` fails fast without one); matches `type == operation_result && ref == requestID`; 5 s timeout; other event types are kept and validated, not matched.
- Fake agent defaults are realistic, schema-valid payloads mirroring `internal/app/agent.Dispatcher`'s echo shapes (non-nil slices, `config: {}`), never zero values. Overrides: `agent.on(type, script)` and `agent.fail(type, msg)`; `agent.seen(type)` and `agent.count()` expose what reached the agent.
- Coverage guard: a route counts as covered when any recorded request matched its table path (any status). Mounted go-pkgz routes are exercised too (`/auth/local/login`, `/auth/logout`, `/auth/status`, `/auth/user`, `/auth/list`, and `/avatar/{avatar}` via its 403 for a missing file); Google OAuth is not in the table. So `unexercised` is empty. The guard is skipped for `-run`/`-skip`/`-short` runs.
- "One error per command family": app family (`app.save`, `app.rename`, `app.delete`, `app.get`) and docker family (`registry.create`, `network.list`, `volume.delete`, `agent.update`), plus the harness self-test's `network.list`.
- Beyond the spec's minimum, admin-only routes are also probed with a member (403), because PR 4 removes roles and should see these assertions change deliberately.
- `TestParity` records responses and the SSE result payloads (agent-bound steps). Normalization: n-th distinct UUID → `<id-n>`, RFC 3339 timestamps → `<time>`, `wfp_…` → `<token>` (PAT and its display prefix), then re-encoding sorts keys; `SetEscapeHTML(false)` keeps placeholders readable. Golden: `internal/app/web/contract/testdata/parity.golden.json`, rewritten with `-update`.
- `WF_TEST_DATABASE_URL` is read only by tests, not by any binary, so `.env.example` does not change; it is documented in `AGENTS.md`.
- `make test-pg` is `scripts/test-pg.sh` (random host port, `pg_isready -h 127.0.0.1` so the image's init-time socket-only server is not mistaken for ready, `trap` removes the container). The CI `contract` job runs both legs with `-race`.
- Repository unit tests use non-UUID ids (`"app-1"`) and therefore stay SQLite-only. Out of scope; the contract suite is the Postgres coverage.

## Global Constraints

- Go module `winterflow`, `go 1.25.3` in `go.mod`, no `toolchain` line. **No new modules**: `go mod tidy` must leave `go.mod`/`go.sum` unchanged (kin-openapi, pgdriver and chi are already required).
- `internal/app/web/contract` contains only `_test.go` files (package `contract_test`) plus `testdata/`.
- Every top-level contract test calls `t.Parallel()` and builds its own `newEnv(t)`; nothing in the package uses `t.Setenv`.
- Production changes are limited to: `WireCore` export, `CommandRunner` interface, SSE header flush, generator library-error media types (+ regenerated `api/openapi.yaml`), `db.utcDSN`, the `app_domains` UUID migration, list-order tie-breaks, CI/Makefile/scripts/docs/baseline. No route, status code or response body changes otherwise.
- Diff coverage: changed non-test Go lines ≥80% (PR 1's gate) **with the Postgres contract profile merged** (Task 12). Postgres-only branches are covered only by that profile.
- `gofmt -l $(git ls-files '*.go')` prints nothing; `go vet ./...` passes.
- No env var is added to any binary, so `.env.example` / `web/.env.example` stay unchanged.
- Never touch `web/src/components/ui/**`.
- Commits: Conventional Commits, imperative mood. Each message ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.

## Review Focus

1. **A result produced before the client is listening.** Results are not buffered: a 202 whose `operation_result` lands before `AddChannel` is silently lost and the test (or a real UI) waits forever. Expected: once `GET /api/v1/sse` has answered 200, every later result reaches it. Pinned by `TestStreamIsSubscribedWhenHeadersArrive` (Task 2, fails without the flush) and by `client.accepted` refusing to dispatch without an open stream (Task 3).
2. **A Postgres whose session time zone is not UTC** (a managed database, a `ALTER ROLE … SET timezone`). Expected: timestamps read back equal to what was written. Pinned by the harness connecting with `timezone=America/New_York` and `TestServerRoutes` asserting `created_at` is within a minute of now (Task 6; −4 h without the fix), plus `TestUTCDSNPinsTimeZone`.
3. **Two rows created in the same second listed in different orders by the two databases** (two apps saved back to back, a member created right after the owner). Expected: the same order everywhere, so UIs and the golden transcript are stable. Pinned by `TestGetAppsOrdersTiesByID` (Task 10, fails without the tie-break) and by `TestParity` saving two apps in the same second.
4. **A new route or input added without a contract test.** Expected: CI fails, naming the route. Pinned by the `TestMain` coverage guard (Task 11, proven by removing a call) and `TestProbesCoverTable` (Task 9: every route with a query or body has a malformed probe; every probe names a real route).
5. **A library or middleware answering with a status or media type the document does not list** (go-pkgz `text/plain` JSON, a 500 from a failed query). Expected: the contract test fails rather than passing silently. Pinned by `IncludeResponseStatus: true` in every response validation (Task 3), the `/auth/user` anonymous 401 in `TestAuthClaimFlow` and the generator's `library error as text` case (Task 4).

---

## File Map

| Path | Action | Responsibility |
|---|---|---|
| `internal/infra/bootstrap/{core,api,standalone}.go`, `core_test.go` | modify (Task 1) | `wireCore` → exported `WireCore` |
| `internal/app/agent/inprocess.go` | modify (Task 1) | `CommandRunner` interface; bridge takes it |
| `internal/app/web/handler/notification/handler.go` | modify (Task 2) | flush headers right after subscribing |
| `internal/app/web/handler/notification/handler_test.go` | create (Task 2) | subscribed-on-headers, 401 |
| `internal/app/web/contract/db_test.go` | create (Task 3), modify (Task 6) | `freshDB`: SQLite temp file or Postgres schema; `dialect()` |
| `internal/app/web/contract/fakeagent_test.go` | create (Task 3) | scripted fake agent through the real codec |
| `internal/app/web/contract/spec_test.go` | create (Task 3) | load spec, `routeFor`, request/response/schema validation, `resultSchema` |
| `internal/app/web/contract/recorder_test.go` | create (Task 3) | hit recorder middleware, `TestMatchPath` |
| `internal/app/web/contract/harness_test.go` | create (Task 3) | `env`, `newEnv`, seeding, `foreignServer`, `publishEvent`, `eventually` |
| `internal/app/web/contract/client_test.go` | create (Task 3) | `client`, `call`, `claim/pat/bearer/member/anon`, `accepted` |
| `internal/app/web/contract/sse_test.go` | create (Task 3) | `subscribe`, `awaitResult`, `succeed`, `failWith` |
| `internal/app/web/contract/main_test.go` | create (Task 3), modify (Task 11) | `TestMain` env; coverage guard |
| `internal/app/web/contract/selftest_test.go` | create (Task 3) | `TestHealth`, `TestHarnessRoundTrip` |
| `scripts/test-pg.sh`, `Makefile` (`test-pg`) | create/modify (Task 3) | throwaway Postgres run |
| `internal/app/web/contract/auth_test.go` | create (Task 4) | register/claim/login/session routes |
| `internal/app/web/api/openapi/openapi.go`, `openapi_test.go`, `api/openapi.yaml` | modify (Task 4) | library errors also `text/plain` |
| `internal/app/web/contract/{user,org}_test.go` | create (Task 5) | user and org routes |
| `internal/app/web/contract/server_test.go` | create (Task 6) | server routes, UTC timestamps |
| `internal/infra/db/bun.go`, `bun_test.go` | modify/create (Task 6) | `utcDSN` |
| `internal/app/web/contract/apps_test.go` | create (Task 7) | app lifecycle, domains, agent errors |
| `internal/infra/db/migrations/20261010000001_app_domains_uuid.go` | create (Task 7) | Postgres `app_domains` UUID columns |
| `internal/app/web/contract/docker_test.go` | create (Task 8) | registry/network/volume/agent routes |
| `internal/app/web/contract/matrix_test.go` | create (Task 9) | probes; 401 / 403 / 400 across the table |
| `internal/app/web/contract/{parity,diff}_test.go`, `testdata/parity.golden.json` | create (Task 10) | parity transcript |
| `internal/infra/db/repository/{app,server,user}.go`, `app_test.go` | modify (Task 10) | ordering tie-breaks |
| `internal/app/web/contract/guard_test.go` | create (Task 11) | `missingRoutes`, `unexercised`, `coverageReport` |
| `.github/workflows/ci.yml` | modify (Task 12) | `-coverpkg`, `contract` matrix, merge Postgres profile |
| `Makefile` (`test`), `AGENTS.md`, `CLAUDE.md`, `coverage-baseline.json` | modify (Task 12) | docs and ratchet |

---

### Task 1: Harness seams — export `WireCore`, bridge takes a `CommandRunner`

**Files:**
- Modify: `internal/infra/bootstrap/core.go:16-23`, `internal/infra/bootstrap/api.go:33`, `internal/infra/bootstrap/standalone.go:41`, `internal/infra/bootstrap/core_test.go:18,32`
- Modify: `internal/app/agent/inprocess.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `func bootstrap.WireCore(ctx context.Context, b bus.Bus, dbconn *db.BunConnection, cfg *config.ServerConfig, log *logger.Logger) (*bootstrap.Deps, *repository.DbServerRepository)`; `type agent.CommandRunner interface { Dispatch(ctx context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope }`; `func agent.NewInProcessBridge(b bus.Bus, cfg *config.ServerConfig, d agent.CommandRunner, log *logger.Logger) *agent.InProcessBridge`.

- [ ] **Step 1: Point the existing test at the exported name (failing build)**

In `internal/infra/bootstrap/core_test.go`, replace `wireCore` with `WireCore` in the comment on line 18 and the call on line 32:

```go
// WireCore is the shared wiring both topologies boot through; prove it
```

```go
	deps, serverRepo := WireCore(ctx, b, conn, cfg, log)
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/infra/bootstrap/ -run TestWireCoreWiresResponsePipeline`
Expected: FAIL, `undefined: WireCore`.

- [ ] **Step 3: Rename the function and its two callers**

`internal/infra/bootstrap/core.go`:

```go
// WireCore builds the wiring both topologies share: DB-backed repositories and
// services, the notification manager, the command dispatcher, the status
// cache, and the response/events subscribers. The topologies differ only in
// the bus they pass (Redis vs in-process) and the extras they add afterwards
// (standalone: certs, in-process bridge, self-registration; api: nothing).
// The concrete server repository is returned alongside Deps for standalone's
// registration flow, which needs methods beyond the port.
func WireCore(ctx context.Context, b bus.Bus, dbconn *db.BunConnection, cfg *config.ServerConfig, log *logger.Logger) (*Deps, *repository.DbServerRepository) {
```

`internal/infra/bootstrap/api.go` line 33: `deps, _ := WireCore(ctx, b, dbconn, cfg, log)`
`internal/infra/bootstrap/standalone.go` line 41: `deps, serverRepo := WireCore(ctx, b, dbconn, cfg, log)`

Check nothing else uses the old name: `grep -rn wireCore --include=*.go .` prints nothing.

- [ ] **Step 4: Let the bridge take an interface**

In `internal/app/agent/inprocess.go` add the import `"winterflow/internal/infra/transport/grpc/proto"` and replace the struct field and constructor:

```go
type InProcessBridge struct {
	bus        bus.Bus
	cfg        *config.ServerConfig
	dispatcher CommandRunner
	log        *logger.Logger
}

// CommandRunner executes one command envelope and returns its correlated
// reply. *Dispatcher is the production implementation; the API contract
// suite plugs in a scripted fake agent. Same method set as the gRPC agent's
// transport Dispatcher interface.
type CommandRunner interface {
	Dispatch(ctx context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope
}

var _ CommandRunner = (*Dispatcher)(nil)

func NewInProcessBridge(b bus.Bus, cfg *config.ServerConfig, d CommandRunner, log *logger.Logger) *InProcessBridge {
	return &InProcessBridge{bus: b, cfg: cfg, dispatcher: d, log: log}
}
```

(`internal/app/agent` must not import `internal/infra/transport/grpc/agent`; the local interface avoids the dependency. The existing `TestInProcessBridgeRoundTrip` passes a `*Dispatcher` and keeps covering the constructor.)

- [ ] **Step 5: Run the affected packages**

Run: `go build ./... && go test ./internal/infra/bootstrap/ ./internal/app/agent/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/infra/bootstrap internal/app/agent/inprocess.go
git commit -m "refactor: export WireCore and let the in-process bridge take a CommandRunner

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: SSE stream is subscribed when its headers arrive

**Files:**
- Modify: `internal/app/web/handler/notification/handler.go` (after `defer h.nm.RemoveChannel(userID, ch)`)
- Test: `internal/app/web/handler/notification/handler_test.go` (create)

**Interfaces:**
- Consumes: `notificationsvc.NewNotificationManager()`, `token.SetUserInfo`.
- Produces: the guarantee Task 3's `client.subscribe` relies on — `GET /api/v1/sse` returns its 200 only after `AddChannel`.

- [ ] **Step 1: Write the failing test**

Create `internal/app/web/handler/notification/handler_test.go`:

```go
package notification

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/domain/model"
	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/pkg/logger"
)

func asUser(id string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := token.User{ID: id}
		u.SetStrAttr("user_id", id)
		next.ServeHTTP(w, token.SetUserInfo(r, u))
	})
}

// TestStreamIsSubscribedWhenHeadersArrive: the handler flushes the headers
// right after subscribing, so a client that has its 200 cannot miss a
// notification published afterwards.
func TestStreamIsSubscribedWhenHeadersArrive(t *testing.T) {
	nm := notificationsvc.NewNotificationManager()
	h := NewHandler(&Deps{Logger: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"}), NotificationManager: nm})
	srv := httptest.NewServer(asUser("u1", http.HandlerFunc(h.Stream)))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req) // returns only once headers are flushed
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	nm.Publish("u1", model.Notification{Type: model.NotificationOperationResult, Ref: "r1"})
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "data: ") || !strings.Contains(line, `"ref":"r1"`) {
		t.Fatalf("event = %q", line)
	}
}

func TestStreamRequiresUser(t *testing.T) {
	h := NewHandler(&Deps{Logger: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"}), NotificationManager: notificationsvc.NewNotificationManager()})
	rec := httptest.NewRecorder()
	h.Stream(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sse", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/app/web/handler/notification/ -run TestStreamIsSubscribedWhenHeadersArrive`
Expected: FAIL after ~5 s (`context deadline exceeded`): `Do` blocks because no header is sent before the first event, so the publish never happens.

- [ ] **Step 3: Flush right after subscribing**

In `Stream`, directly after `defer h.nm.RemoveChannel(userID, ch)`:

```go
	// Send the headers now: once the client sees 200 it is subscribed, so
	// a command it dispatches next cannot finish before its result has a
	// channel to land in.
	flusher.Flush()
```

- [ ] **Step 4: Run the package**

Run: `go test -race ./internal/app/web/handler/notification/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web/handler/notification
git commit -m "fix(sse): send headers once the stream is subscribed

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Contract harness — stack, fake agent, validated clients, SSE

**Files:**
- Create: `internal/app/web/contract/db_test.go`, `fakeagent_test.go`, `spec_test.go`, `recorder_test.go`, `harness_test.go`, `client_test.go`, `sse_test.go`, `main_test.go`, `selftest_test.go`
- Create: `scripts/test-pg.sh`
- Modify: `Makefile` (`.PHONY`, new `test-pg` target)

**Interfaces:**
- Consumes: `bootstrap.WireCore`, `appagent.NewInProcessBridge`/`CommandRunner` (Task 1); SSE flush (Task 2); `web.NewServer`, `web.APIRoutes()`, `openapi.ResultExtension`, `api/openapi.yaml` (PR 2).
- Produces (all in package `contract_test`, used by Tasks 4–11):
  - `func freshDB(t *testing.T) string`; `func dialect() string`; `const dbEnv = "WF_TEST_DATABASE_URL"`
  - `type fakeAgent`; `func newFakeAgent() *fakeAgent`; `(a *fakeAgent) on(typ command.Type, s script)`; `fail(typ command.Type, msg string)`; `seen(typ command.Type) []received`; `count() int`; `type reply struct{ Payload any; Err string }`; `type script func(req json.RawMessage) reply`; `func decode[T any](raw json.RawMessage) T`
  - `var table = web.APIRoutes()`; `func loadSpec(t) *openapi3.T`; `func matchPath(tpl, path string) (map[string]string, bool)`; `func routeFor(method, path string) (api.Route, map[string]string, bool)`; `func validateRequest(t, *http.Request)`; `func validateResponse(t, *http.Request, status int, header http.Header, body []byte)`; `func validateSchema(t, component string, raw []byte)`; `func checkSchema(component string, raw []byte) error`; `func resultSchema(t, api.Route) string`
  - `var hits` (`set map[hit]int`, `type hit struct{ Method, Path string; Status int }`); `func record(http.Handler) http.Handler`
  - `type env struct{ ctx; deps *bootstrap.Deps; agent *fakeAgent; srv *httptest.Server; bus *membus.Bus; cfg *config.ServerConfig; … }`; `func newEnv(t) *env`; `(e *env) seedPendingServer(t) string` (returns the code); `foreignServer(t) string` (server id); `publishEvent(t, bus.EventMessage)`; `func eventually(t, cond func() bool)`
  - `type client`; `type call struct{ Method, Path string; Query url.Values; Body any; Invalid bool }`; `type response struct{ Status int; Header http.Header; Body []byte }`; `(c *client) do/get/post`; `(r *response) expect(t, int) *response`; `(r *response) data(t, v any) envelope`; `(c *client) accepted(t, call) string`; `(e *env) claim(t) *client`; `anon() *client`; `newClient() *client`; `bearer(t, token string, owner *client) *client`; `(c *client) pat(t) *client`; `member(t) *client`; `login(t, email, password string)`; `serverID(t) string`; `const ownerPassword`
  - `(c *client) subscribe(t)`; `func awaitResult(t, c *client, requestID string) event`; `(c *client) succeed(t, call, v any)`; `(c *client) failWith(t, call, msg string)`; `const resultTimeout = 5 * time.Second`

- [ ] **Step 1: Write the self-test that drives the harness**

Create `internal/app/web/contract/selftest_test.go`:

```go
package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/domain/command"
)

func TestHealth(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.anon().get(t, "/_/health", nil).expect(t, http.StatusOK)
}

// TestHarnessRoundTrip proves the harness end to end: claim + auto-claimed
// server, an agent-bound call answered by the fake agent over SSE, a
// scripted agent error, PAT auth, 401 and the foreign-org 403.
func TestHarnessRoundTrip(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	q := url.Values{"server_id": {sid}}

	var list command.ListNetworksResponse
	owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/network/list", Query: q}, &list)
	if len(list.Networks) != 1 || len(e.agent.seen(command.TypeNetworkList)) != 1 {
		t.Fatalf("networks = %+v", list)
	}
	e.agent.fail(command.TypeNetworkList, "docker down")
	owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/network/list", Query: q}, "docker down")

	owner.pat(t).get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusOK)
	e.anon().get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusUnauthorized)
	owner.get(t, "/api/v1/server/get-public-key", url.Values{"server_id": {e.foreignServer(t)}}).
		expect(t, http.StatusForbidden)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/app/web/contract/`
Expected: build failure, `undefined: newEnv` (and the other harness names).

- [ ] **Step 3: Database per test**

Create `internal/app/web/contract/db_test.go`:

```go
package contract_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uptrace/bun/driver/pgdriver"
)

// dbEnv names the Postgres server the suite runs against. Unset means
// SQLite: one temp file per test.
const dbEnv = "WF_TEST_DATABASE_URL"

// dialect reports which database this run exercises ("sqlite" or "postgres").
func dialect() string {
	if strings.HasPrefix(os.Getenv(dbEnv), "postgres://") {
		return "postgres"
	}
	return "sqlite"
}

// freshDB returns a connection string for an empty database owned by t.
// SQLite: a temp file. Postgres: a new schema t_<hex> on WF_TEST_DATABASE_URL,
// selected with search_path, dropped on cleanup.
func freshDB(t *testing.T) string {
	t.Helper()
	base := os.Getenv(dbEnv)
	if dialect() == "sqlite" {
		return "sqlite://" + filepath.Join(t.TempDir(), "wf.sqlite")
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(b[:])
	admin := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(base)))
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema on %s: %v", dbEnv, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", dbEnv, err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return fmt.Sprint(u)
}
```

- [ ] **Step 4: The fake agent**

Create `internal/app/web/contract/fakeagent_test.go`. Default answers echo request fields the way `internal/app/agent/dispatcher.go` does and never contain nil slices (PR 2's response schemas mark them required, and `null` would fail):

```go
package contract_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	appagent "winterflow/internal/app/agent"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/codec"
	grpcagent "winterflow/internal/infra/transport/grpc/agent"
	"winterflow/internal/infra/transport/grpc/proto"
)

// reply is a scripted agent answer: Err != "" makes it an error response
// (status 1 on the SSE result), otherwise Payload is the success body.
type reply struct {
	Payload any
	Err     string
}

// script answers one command; req is the raw request payload.
type script func(req json.RawMessage) reply

// received is one command the fake agent saw.
type received struct {
	Type    command.Type
	AgentID string
	Payload json.RawMessage
}

// fakeAgent stands in for the docker-compose Dispatcher behind the real
// InProcessBridge. Every command type answers success by default with the
// same echo shape the real Dispatcher produces; tests override per type.
type fakeAgent struct {
	mu        sync.Mutex
	overrides map[command.Type]script
	calls     []received
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{overrides: map[command.Type]script{}}
}

// on overrides the answer for typ for the rest of the test.
func (a *fakeAgent) on(typ command.Type, s script) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.overrides[typ] = s
}

// fail makes typ answer with an error.
func (a *fakeAgent) fail(typ command.Type, msg string) {
	a.on(typ, func(json.RawMessage) reply { return reply{Err: msg} })
}

// seen returns the commands of type typ received so far.
func (a *fakeAgent) seen(typ command.Type) []received {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []received
	for _, c := range a.calls {
		if c.Type == typ {
			out = append(out, c)
		}
	}
	return out
}

// count is the number of commands received so far, of any type.
func (a *fakeAgent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

// Dispatch implements agent.CommandRunner (and the gRPC transport's
// Dispatcher): decode nothing itself, answer through the real codec.
func (a *fakeAgent) Dispatch(_ context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	typ := command.Type(req.Type)
	agentID := ""
	if req.Base != nil {
		agentID = req.Base.AgentId
	}
	a.mu.Lock()
	a.calls = append(a.calls, received{Type: typ, AgentID: agentID, Payload: append(json.RawMessage(nil), req.Payload...)})
	s, ok := a.overrides[typ]
	a.mu.Unlock()
	if !ok {
		s = defaultScript(typ)
	}
	r := s(req.Payload)
	code, detail, body := proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", r.Payload
	if r.Err != "" {
		code, detail, body = proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR, r.Err, nil
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, typ, code, detail, body)
	if err != nil {
		resp, _ = codec.EncodeResponse(agentID, req.RequestId, typ, proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR, err.Error(), nil)
	}
	return resp
}

// decode is the scripts' request decoder (the real codec).
func decode[T any](raw json.RawMessage) T {
	var v T
	_ = codec.DecodePayload(raw, &v)
	return v
}

// defaultScript is the success answer for typ, echoing request fields the
// way internal/app/agent's Dispatcher does.
func defaultScript(typ command.Type) script {
	switch typ {
	case command.TypeAppSave:
		return func(raw json.RawMessage) reply {
			in := decode[command.SaveAppRequest](raw)
			return reply{Payload: command.SaveAppResponse{AppID: in.App.AppID, Revision: "rev-1"}}
		}
	case command.TypeAppsList:
		return func(json.RawMessage) reply { return reply{Payload: command.ListAppsResponse{Apps: []model.App{}}} }
	case command.TypeAppsStatus:
		return func(json.RawMessage) reply {
			return reply{Payload: command.GetAppsStatusResponse{Apps: []command.AppStatus{}}}
		}
	case command.TypeAppGet:
		return func(raw json.RawMessage) reply {
			in := decode[command.GetAppRequest](raw)
			return reply{Payload: command.GetAppResponse{App: command.AppPayload{AppID: in.AppID, Config: []byte(`{}`), Variables: []command.ContentItem{}, Files: []command.ContentItem{}}}}
		}
	case command.TypeAppControl:
		return func(raw json.RawMessage) reply {
			in := decode[command.ControlAppRequest](raw)
			return reply{Payload: command.ControlAppResponse{AppID: in.AppID, Action: in.Action}}
		}
	case command.TypeAppDelete:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.DeleteAppResponse{AppID: decode[command.DeleteAppRequest](raw).AppID}}
		}
	case command.TypeAppRename:
		return func(raw json.RawMessage) reply {
			in := decode[command.RenameAppRequest](raw)
			return reply{Payload: command.RenameAppResponse{AppID: in.AppID, Name: in.Name}}
		}
	case command.TypeAppLogs:
		return func(raw json.RawMessage) reply {
			in := decode[command.GetLogsRequest](raw)
			return reply{Payload: command.GetLogsResponse{AppID: in.AppID, Logs: []command.LogEntry{{Timestamp: 1, Level: command.LogLevelInfo, Message: "hello"}}}}
		}
	case command.TypeAppRevisions:
		return func(raw json.RawMessage) reply {
			in := decode[command.GetRevisionsRequest](raw)
			return reply{Payload: command.GetRevisionsResponse{AppID: in.AppID, Current: "rev-1", Deployed: "rev-1",
				Revisions: []command.RevisionInfo{{Hash: "rev-1", Subject: "save", Timestamp: 1}}}}
		}
	case command.TypeAppRollback:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.RollbackAppResponse{AppID: decode[command.RollbackAppRequest](raw).AppID, Revision: "rev-2"}}
		}
	case command.TypeRegistryList:
		return func(json.RawMessage) reply {
			return reply{Payload: command.ListRegistriesResponse{Registries: []command.Registry{{Address: "ghcr.io"}}}}
		}
	case command.TypeRegistryCreate:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.CreateRegistryResponse{Address: decode[command.CreateRegistryRequest](raw).Address}}
		}
	case command.TypeRegistryDelete:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.DeleteRegistryResponse{Address: decode[command.DeleteRegistryRequest](raw).Address}}
		}
	case command.TypeNetworkList:
		return func(json.RawMessage) reply {
			return reply{Payload: command.ListNetworksResponse{Networks: []command.Network{{Name: "bridge", Driver: "bridge"}}}}
		}
	case command.TypeNetworkCreate:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.CreateNetworkResponse{Name: decode[command.CreateNetworkRequest](raw).Name}}
		}
	case command.TypeNetworkDelete:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.DeleteNetworkResponse{Name: decode[command.DeleteNetworkRequest](raw).Name}}
		}
	case command.TypeVolumeList:
		return func(json.RawMessage) reply {
			return reply{Payload: command.ListVolumesResponse{Volumes: []command.Volume{{Name: "data", Driver: "local"}}}}
		}
	case command.TypeVolumeCreate:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.CreateVolumeResponse{Name: decode[command.CreateVolumeRequest](raw).Name}}
		}
	case command.TypeVolumeDelete:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.DeleteVolumeResponse{Name: decode[command.DeleteVolumeRequest](raw).Name}}
		}
	case command.TypeImageTags:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.ImageTagsResponse{Image: decode[command.ImageTagsRequest](raw).Image, Tags: []string{"latest"}}}
		}
	case command.TypeAgentUpdate:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.UpdateAgentResponse{FromVersion: "v1", ToVersion: decode[command.UpdateAgentRequest](raw).Version, Scheduled: true}}
		}
	}
	return func(json.RawMessage) reply { return reply{Err: "fake agent: unsupported command type " + string(typ)} }
}

// TestFakeAgentAnswersEveryCommandType pins that every command type the
// API can dispatch has a default success script.
func TestFakeAgentAnswersEveryCommandType(t *testing.T) {
	t.Parallel()
	a := newFakeAgent()
	for _, typ := range []command.Type{
		command.TypeAppSave, command.TypeAppGet, command.TypeAppsList, command.TypeAppsStatus,
		command.TypeAppDelete, command.TypeAppRename, command.TypeAppControl, command.TypeAppLogs,
		command.TypeAppRevisions, command.TypeAppRollback, command.TypeRegistryList,
		command.TypeRegistryCreate, command.TypeRegistryDelete, command.TypeNetworkList,
		command.TypeNetworkCreate, command.TypeNetworkDelete, command.TypeVolumeList,
		command.TypeVolumeCreate, command.TypeVolumeDelete, command.TypeImageTags, command.TypeAgentUpdate,
	} {
		resp := a.Dispatch(context.Background(), &proto.RequestEnvelope{Type: string(typ), RequestId: "r", Payload: []byte(`{}`)})
		if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
			t.Errorf("%s: default answer is %v %q", typ, resp.Base.ResponseCode, resp.Base.Detail)
		}
	}
	resp := a.Dispatch(context.Background(), &proto.RequestEnvelope{Type: "nope", RequestId: "r"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Error("unknown command type must answer an error")
	}
}

// The fake satisfies both seams the real Dispatcher fills.
var (
	_ appagent.CommandRunner = (*fakeAgent)(nil)
	_ grpcagent.Dispatcher   = (*fakeAgent)(nil)
)
```

- [ ] **Step 5: Spec loading and validation**

Create `internal/app/web/contract/spec_test.go`:

```go
package contract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"winterflow/internal/app/web"
	"winterflow/internal/app/web/api"
	"winterflow/internal/app/web/api/openapi"
)

// specPath is the committed, generated document (PR 2), relative to this
// package.
var specPath = filepath.Join("..", "..", "..", "..", "api", "openapi.yaml")

var (
	specOnce sync.Once
	specDoc  *openapi3.T
	specErr  error
	table    = web.APIRoutes()
)

// loadSpec parses api/openapi.yaml once per test binary.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	specOnce.Do(func() {
		specDoc, specErr = openapi3.NewLoader().LoadFromFile(specPath)
		if specErr == nil {
			specErr = specDoc.Validate(context.Background())
		}
	})
	if specErr != nil {
		t.Fatalf("load %s: %v", specPath, specErr)
	}
	return specDoc
}

// matchPath reports whether a concrete URL path matches a table path, where
// a {param} segment matches any one non-empty segment.
func matchPath(tpl, path string) (map[string]string, bool) {
	ts, ps := strings.Split(tpl, "/"), strings.Split(path, "/")
	if len(ts) != len(ps) {
		return nil, false
	}
	params := map[string]string{}
	for i := range ts {
		if strings.HasPrefix(ts[i], "{") && strings.HasSuffix(ts[i], "}") {
			if ps[i] == "" {
				return nil, false
			}
			params[strings.Trim(ts[i], "{}")] = ps[i]
			continue
		}
		if ts[i] != ps[i] {
			return nil, false
		}
	}
	return params, true
}

// routeFor finds the table route serving method + path.
func routeFor(method, path string) (api.Route, map[string]string, bool) {
	for _, rt := range table {
		if rt.Method != method {
			continue
		}
		if params, ok := matchPath(rt.Path, path); ok {
			return rt, params, true
		}
	}
	return api.Route{}, nil, false
}

// validationInput builds the kin-openapi input for req; the operation is
// looked up from the table route, so no router dependency is needed.
func validationInput(t *testing.T, req *http.Request) *openapi3filter.RequestValidationInput {
	t.Helper()
	doc := loadSpec(t)
	rt, params, ok := routeFor(req.Method, req.URL.Path)
	if !ok {
		t.Fatalf("%s %s is not in the route table", req.Method, req.URL.Path)
	}
	item := doc.Paths.Find(rt.Path)
	if item == nil || item.GetOperation(rt.Method) == nil {
		t.Fatalf("%s %s is not in %s", rt.Method, rt.Path, specPath)
	}
	return &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: params,
		Route: &routers.Route{
			Spec: doc, Path: rt.Path, PathItem: item, Method: rt.Method,
			Operation: item.GetOperation(rt.Method),
		},
		Options: &openapi3filter.Options{
			// Auth is asserted by status codes, not by the validator.
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			MultiError:         true,
		},
	}
}

// validateRequest checks req against the document (it restores the body).
func validateRequest(t *testing.T, req *http.Request) {
	t.Helper()
	in := validationInput(t, req)
	if err := openapi3filter.ValidateRequest(req.Context(), in); err != nil {
		t.Fatalf("request %s %s violates the spec: %v", req.Method, req.URL.Path, err)
	}
}

// validateResponse checks status, headers and body against the document; an
// undocumented status fails.
func validateResponse(t *testing.T, req *http.Request, status int, header http.Header, body []byte) {
	t.Helper()
	in := validationInput(t, req)
	in.Options.IncludeResponseStatus = true
	if strings.HasPrefix(header.Get("Content-Type"), "text/event-stream") {
		in.Options.ExcludeResponseBody = true
	}
	err := openapi3filter.ValidateResponse(req.Context(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 status,
		Header:                 header,
		Body:                   io.NopCloser(bytes.NewReader(body)),
		Options:                in.Options,
	})
	if err != nil {
		t.Fatalf("response %d of %s %s violates the spec: %v\nbody: %s", status, req.Method, req.URL.Path, err, body)
	}
}

// validateSchema checks raw JSON against a named component.
func validateSchema(t *testing.T, component string, raw []byte) {
	t.Helper()
	loadSpec(t)
	if err := checkSchema(component, raw); err != nil {
		t.Fatal(err)
	}
}

// checkSchema is validateSchema without a *testing.T (for cleanups); the
// spec must already be loaded.
func checkSchema(component string, raw []byte) error {
	ref := specDoc.Components.Schemas[component]
	if ref == nil {
		return fmt.Errorf("component %s is not in the spec", component)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("%s is not JSON: %v", raw, err)
	}
	if err := ref.Value.VisitJSON(v, openapi3.EnableJSONSchema2020()); err != nil {
		return fmt.Errorf("%s does not match %s: %v", raw, component, err)
	}
	return nil
}

// resultSchema is the x-operation-result component of an Accepted route.
func resultSchema(t *testing.T, rt api.Route) string {
	t.Helper()
	op := loadSpec(t).Paths.Find(rt.Path).GetOperation(rt.Method)
	ext, ok := op.Extensions[openapi.ResultExtension].(map[string]any)
	if !ok {
		t.Fatalf("%s %s has no %s", rt.Method, rt.Path, openapi.ResultExtension)
	}
	ref, _ := ext["$ref"].(string)
	return strings.TrimPrefix(ref, "#/components/schemas/")
}
```

- [ ] **Step 6: The hit recorder**

Create `internal/app/web/contract/recorder_test.go`. `record` must also mark requests whose handler never writes (go-pkgz logout answers an implicit 200 with no body):

```go
package contract_test

import (
	"net/http"
	"sync"
	"testing"
)

// hit is one request the suite made: the table route it reached and the
// status it got.
type hit struct {
	Method, Path string
	Status       int
}

// hits collects every routed request across all tests in the binary.
var hits = struct {
	sync.Mutex
	set map[hit]int
}{set: map[hit]int{}}

// statusWriter captures the status on the first header write. Flush is
// passed through: the SSE handler needs an http.Flusher.
type statusWriter struct {
	http.ResponseWriter
	method, path string
	recorded     bool
}

func (w *statusWriter) WriteHeader(code int) {
	w.mark(code)
	w.ResponseWriter.WriteHeader(code)
}

// mark records the request once, under its table path.
func (w *statusWriter) mark(code int) {
	if w.recorded {
		return
	}
	w.recorded = true
	path := w.path
	if rt, _, ok := routeFor(w.method, w.path); ok {
		path = rt.Path
	}
	hits.Lock()
	hits.set[hit{w.method, path, code}]++
	hits.Unlock()
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.recorded {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if !w.recorded {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// record wraps the real router; only the harness installs it.
func record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, method: r.Method, path: r.URL.Path}
		next.ServeHTTP(sw, r)
		sw.mark(http.StatusOK) // a handler that never writes answers 200
	})
}

func TestMatchPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tpl, path string
		ok        bool
	}{
		{"/api/v1/sse", "/api/v1/sse", true},
		{"/api/v1/sse", "/api/v1/sse/x", false},
		{"/avatar/{avatar}", "/avatar/a.png", true},
		{"/avatar/{avatar}", "/avatar/", false},
		{"/auth/local/login", "/auth/google/login", false},
	}
	for _, tc := range cases {
		if _, ok := matchPath(tc.tpl, tc.path); ok != tc.ok {
			t.Errorf("matchPath(%q, %q) = %v, want %v", tc.tpl, tc.path, ok, tc.ok)
		}
	}
}
```

- [ ] **Step 7: The stack**

Create `internal/app/web/contract/harness_test.go`:

```go
package contract_test

import (
	"context"
	"net"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	appagent "winterflow/internal/app/agent"
	"winterflow/internal/app/web"
	"winterflow/internal/domain/dto"
	"winterflow/internal/infra/bootstrap"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// env is one isolated stack: fresh DB, the standalone core wiring, the real
// InProcessBridge in front of a fake agent, and the real router behind an
// httptest server.
type env struct {
	ctx   context.Context
	deps  *bootstrap.Deps
	agent *fakeAgent
	srv   *httptest.Server
	bus   *membus.Bus
	cfg   *config.ServerConfig

	mu       sync.Mutex
	requests map[string]string // request_id -> "METHOD /path" that dispatched it
}

// newServerMu serializes web.NewServer across parallel tests (see newEnv).
var newServerMu sync.Mutex

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "contract"})

// newEnv builds the stack the way BootstrapStandalone does (WireCore over an
// in-memory bus + InProcessBridge), minus Docker, certs and ingress.
func newEnv(t *testing.T) *env {
	t.Helper()
	cfg := config.NewServerConfig("standalone")
	conn := db.NewBunConnection(testLog, freshDB(t))
	// Parallel tests share one Postgres; keep each test's pool small.
	conn.GetDB().SetMaxOpenConns(4)
	t.Cleanup(func() { _ = conn.Shutdown() })

	ctx, cancel := context.WithCancel(context.Background())
	b := membus.NewBus(testLog)
	deps, _ := bootstrap.WireCore(ctx, b, conn, cfg, testLog)
	agent := newFakeAgent()
	if err := appagent.NewInProcessBridge(b, cfg, agent, testLog).Start(ctx); err != nil {
		t.Fatal(err)
	}

	// go-pkgz token.NewService writes a package global on every call (a
	// function-local sync.Once), so parallel NewServer calls race.
	newServerMu.Lock()
	handler := web.NewServer(ctx, deps).Handler
	newServerMu.Unlock()
	srv := httptest.NewUnstartedServer(record(handler))
	// SSE handlers observe ctx through BaseContext: cancelling it ends
	// streams so Close does not wait on them.
	srv.Config.BaseContext = func(_ net.Listener) context.Context { return ctx }
	srv.Start()
	t.Cleanup(srv.Close)
	t.Cleanup(cancel) // runs before srv.Close (LIFO)

	e := &env{ctx: ctx, deps: deps, agent: agent, srv: srv, bus: b, cfg: cfg, requests: map[string]string{}}
	e.seedPendingServer(t)
	return e
}

// seedPendingServer leaves one claimable registration, as BootstrapStandalone
// does at boot; the first login auto-claims it.
func (e *env) seedPendingServer(t *testing.T) string {
	t.Helper()
	code := util.GenerateRandomCode(6)
	err := e.deps.ServerRepository.RegisterServer(e.ctx, dto.ServerRegistrationDTO{
		ServerID: util.GenerateID(), CertificateID: util.GenerateID(), Hostname: "contract",
		Code: code, ExpiresAt: time.Now().Add(time.Hour), Certificate: []byte("cert"),
		CertificateExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed pending server: %v", err)
	}
	return code
}

// foreignServer creates a second org (own user) with a claimed server and
// returns that server's id: the 403 probe for server-addressed routes.
func (e *env) foreignServer(t *testing.T) string {
	t.Helper()
	u, err := e.deps.UserService.RegisterLocalUser(e.ctx, "Mallory", "mallory@other.test", "password")
	if err != nil {
		t.Fatalf("foreign user: %v", err)
	}
	orgID, err := e.deps.UserService.PrimaryOrganizationID(e.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code := e.seedPendingServer(t)
	srv, err := e.deps.ServerRepository.ClaimServer(e.ctx, dto.ClaimServerDTO{Code: code, OrganizationID: orgID})
	if err != nil {
		t.Fatalf("foreign claim: %v", err)
	}
	return srv.ID
}

func (e *env) rememberRequest(id, route string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests[id] = route
}

func (e *env) routeOfRequest(id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.requests[id]
}

// publishEvent puts an agent-initiated event on the events queue, the way
// the Hub (or standalone's status reporter) does.
func (e *env) publishEvent(t *testing.T, ev bus.EventMessage) {
	t.Helper()
	if err := e.bus.Publish(e.ctx, e.cfg.GetBusEventsQueue(), ev); err != nil {
		t.Fatal(err)
	}
}

// eventually polls cond for up to resultTimeout (the events queue is async).
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(resultTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within " + resultTimeout.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
```

- [ ] **Step 8: Clients**

Create `internal/app/web/contract/client_test.go`:

```go
package contract_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

// client is one caller of the API: anonymous, a JWT-cookie session, or a
// PAT bearer. Every request and response it sends is validated against
// api/openapi.yaml.
type client struct {
	e      *env
	http   *http.Client
	bearer string
	userID string
	email  string
	stream *stream // SSE subscription, opened by subscribe
}

// call is one request. Body is JSON-encoded unless it is a string, which is
// sent verbatim (malformed-body probes). Invalid skips request validation
// for deliberately bad requests; the response is validated either way.
type call struct {
	Method  string
	Path    string
	Query   url.Values
	Body    any
	Invalid bool
}

// response is a received answer.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// envelope is webutil's body shape.
type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *env) newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{Jar: jar}}
}

// anon is an unauthenticated client.
func (e *env) anon() *client { return e.newClient() }

func (c *client) do(t *testing.T, k call) *response {
	t.Helper()
	u := c.e.srv.URL + k.Path
	if len(k.Query) > 0 {
		u += "?" + k.Query.Encode()
	}
	var body io.Reader
	switch b := k.Body.(type) {
	case nil:
	case string:
		body = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(c.e.ctx, k.Method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if !k.Invalid {
		validateRequest(t, req)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", k.Method, k.Path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	// Validation needs a fresh request: the original body was consumed.
	vreq, _ := http.NewRequestWithContext(c.e.ctx, k.Method, u, nil)
	validateResponse(t, vreq, resp.StatusCode, resp.Header, raw)
	return &response{Status: resp.StatusCode, Header: resp.Header, Body: raw}
}

func (c *client) get(t *testing.T, path string, q url.Values) *response {
	t.Helper()
	return c.do(t, call{Method: http.MethodGet, Path: path, Query: q})
}

func (c *client) post(t *testing.T, path string, body any) *response {
	t.Helper()
	return c.do(t, call{Method: http.MethodPost, Path: path, Body: body})
}

// expect fails unless the status is want.
func (r *response) expect(t *testing.T, want int) *response {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status = %d, want %d; body: %s", r.Status, want, r.Body)
	}
	return r
}

// data decodes the envelope's data into v (v may be nil).
func (r *response) data(t *testing.T, v any) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(r.Body, &env); err != nil {
		t.Fatalf("body is not the envelope: %v: %s", err, r.Body)
	}
	if v != nil {
		if err := json.Unmarshal(env.Data, v); err != nil {
			t.Fatalf("data: %v: %s", err, env.Data)
		}
	}
	return env
}

// accepted asserts a 202, remembers which route dispatched the request id
// (awaitResult validates against its x-operation-result) and returns it.
func (c *client) accepted(t *testing.T, k call) string {
	t.Helper()
	if c.stream == nil {
		t.Fatal("subscribe before dispatching: the result is only delivered to open streams")
	}
	r := c.do(t, k).expect(t, http.StatusAccepted)
	var d struct {
		RequestID string `json:"request_id"`
	}
	r.data(t, &d)
	if d.RequestID == "" {
		t.Fatalf("202 without request_id: %s", r.Body)
	}
	c.e.rememberRequest(d.RequestID, k.Method+" "+k.Path)
	return d.RequestID
}

const ownerPassword = "owner-password"

// claim runs the first-run flow: register (claims the fresh instance) and
// log in. The login auto-claims the pending embedded server. The client
// holds the JWT cookie and an open SSE stream.
func (e *env) claim(t *testing.T) *client {
	t.Helper()
	c := e.newClient()
	c.email = "owner@example.test"
	r := c.post(t, "/api/v1/auth/register", map[string]string{
		"name": "Owner", "email": c.email, "password": ownerPassword,
	}).expect(t, http.StatusOK)
	var reg struct {
		UserID string `json:"user_id"`
	}
	r.data(t, &reg)
	c.userID = reg.UserID
	c.login(t, c.email, ownerPassword)
	c.subscribe(t)
	return c
}

// login posts the go-pkgz local login; the cookie lands in the jar.
func (c *client) login(t *testing.T, email, password string) {
	t.Helper()
	c.do(t, call{Method: http.MethodPost, Path: "/auth/local/login", Body: map[string]string{
		"user": email, "passwd": password, "aud": "contract",
	}}).expect(t, http.StatusOK)
}

// pat mints a personal access token as c and returns a bearer-only client
// for the same user (no cookie), subscribed to SSE.
func (c *client) pat(t *testing.T) *client {
	t.Helper()
	var tok struct {
		Token string `json:"token"`
	}
	c.post(t, "/api/v1/user/create-token", map[string]any{"name": "contract"}).
		expect(t, http.StatusOK).data(t, &tok)
	return c.e.bearer(t, tok.Token, c)
}

// bearer is a cookie-less client authenticating with token as owner's user,
// subscribed to SSE.
func (e *env) bearer(t *testing.T, token string, owner *client) *client {
	t.Helper()
	p := e.newClient()
	p.bearer, p.userID, p.email = token, owner.userID, owner.email
	p.subscribe(t)
	return p
}

// member creates a member account in c's org and returns it logged in.
func (c *client) member(t *testing.T) *client {
	t.Helper()
	var created struct {
		UserID       string `json:"user_id"`
		TempPassword string `json:"temp_password"`
	}
	c.post(t, "/api/v1/org/create-user", map[string]string{
		"name": "Member", "email": "member@example.test", "role": "member",
	}).expect(t, http.StatusOK).data(t, &created)
	m := c.e.newClient()
	m.userID, m.email = created.UserID, "member@example.test"
	m.login(t, m.email, created.TempPassword)
	m.subscribe(t)
	return m
}

// serverID is the caller's (auto-claimed) server.
func (c *client) serverID(t *testing.T) string {
	t.Helper()
	var d struct {
		Servers []struct {
			ID string `json:"id"`
		} `json:"servers"`
	}
	c.get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusOK).data(t, &d)
	if len(d.Servers) == 0 {
		t.Fatal("caller has no server")
	}
	return d.Servers[0].ID
}
```

- [ ] **Step 9: SSE**

Create `internal/app/web/contract/sse_test.go`. `event` shadows `model.Notification.Payload` with a `json.RawMessage` (the shallower field wins in `encoding/json`), so payloads stay raw for schema validation:

```go
package contract_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
)

// resultTimeout bounds awaitResult (spec: 5 s).
const resultTimeout = 5 * time.Second

// event is one decoded SSE notification plus its raw JSON.
type event struct {
	model.Notification
	Payload json.RawMessage `json:"payload,omitempty"`
	Raw     []byte          `json:"-"`
}

// stream buffers every event of one SSE connection.
type stream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	events []event
	done   bool
}

// subscribe opens GET /api/v1/sse and returns once the server has
// subscribed the client (the handler flushes headers after AddChannel).
func (c *client) subscribe(t *testing.T) {
	t.Helper()
	if c.stream != nil {
		return
	}
	ctx, cancel := context.WithCancel(c.e.ctx)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.e.srv.URL+"/api/v1/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	validateRequest(t, req)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	validateResponse(t, req, resp.StatusCode, resp.Header, nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("SSE status = %d", resp.StatusCode)
	}
	s := &stream{}
	s.cond = sync.NewCond(&s.mu)
	c.stream = s
	// Every event the stream carried, of any type (server_status and
	// apps_status included), must match the Notification schema.
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, ev := range s.events {
			if err := checkSchema("Notification", ev.Raw); err != nil {
				t.Errorf("SSE event violates the spec: %v", err)
			}
		}
	})
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			raw := []byte(strings.TrimPrefix(line, "data: "))
			var ev event
			_ = json.Unmarshal(raw, &ev)
			ev.Raw = raw
			s.mu.Lock()
			s.events = append(s.events, ev)
			s.cond.Broadcast()
			s.mu.Unlock()
		}
		s.mu.Lock()
		s.done = true
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
}

// awaitResult waits for the operation_result correlated to requestID,
// validates the event against the Notification schema and, on success, its
// payload against the dispatching route's x-operation-result.
func awaitResult(t *testing.T, c *client, requestID string) event {
	t.Helper()
	s := c.stream
	if s == nil {
		t.Fatal("awaitResult on a client without an SSE stream")
	}
	deadline := time.Now().Add(resultTimeout)
	timer := time.AfterFunc(resultTimeout, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer timer.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		for _, ev := range s.events {
			if ev.Type == model.NotificationOperationResult && ev.Ref == requestID {
				validateSchema(t, "Notification", ev.Raw)
				if ev.Status == model.NotificationStatusSuccess {
					key := c.e.routeOfRequest(requestID)
					method, path, _ := strings.Cut(key, " ")
					rt, _, ok := routeFor(method, path)
					if !ok {
						t.Fatalf("request %s was not dispatched through client.accepted", requestID)
					}
					validateSchema(t, resultSchema(t, rt), ev.Payload)
				}
				return ev
			}
		}
		if s.done || time.Now().After(deadline) {
			t.Fatalf("no operation_result for %s within %s", requestID, resultTimeout)
		}
		s.cond.Wait()
	}
}

// succeed dispatches k, awaits its result and requires success; the result
// payload is decoded into v when non-nil.
func (c *client) succeed(t *testing.T, k call, v any) {
	t.Helper()
	ev := awaitResult(t, c, c.accepted(t, k))
	if ev.Status != model.NotificationStatusSuccess {
		t.Fatalf("%s %s: result status %d error %q", k.Method, k.Path, ev.Status, ev.Error)
	}
	if v != nil {
		if err := json.Unmarshal(ev.Payload, v); err != nil {
			t.Fatalf("result payload: %v", err)
		}
	}
}

// failWith dispatches k and requires an error result carrying msg.
func (c *client) failWith(t *testing.T, k call, msg string) {
	t.Helper()
	ev := awaitResult(t, c, c.accepted(t, k))
	if ev.Status != model.NotificationStatusError || ev.Error != msg || len(ev.Payload) != 0 {
		t.Fatalf("%s %s: want error %q, got status %d error %q payload %s", k.Method, k.Path, msg, ev.Status, ev.Error, ev.Payload)
	}
}
```

- [ ] **Step 10: TestMain (environment only; the guard comes in Task 11)**

Create `internal/app/web/contract/main_test.go`:

```go
package contract_test

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

// TestMain sets the process-wide environment the server reads (every test
// shares it, so tests can run in parallel), then runs the suite.
func TestMain(m *testing.M) {
	flag.Parse()
	tmp, err := os.MkdirTemp("", "wf-contract-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for k, v := range map[string]string{
		"JWT_SECRET":           "contract-secret",
		"WEB_URL":              "http://127.0.0.1",
		"REGION":               "contract",
		"AVATARS_STORAGE_PATH": tmp,
		"AGENT_DATA_DIR":       tmp,
		"INGRESS_ENABLED":      "false",
	} {
		os.Setenv(k, v)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
```

- [ ] **Step 11: Run the harness on SQLite, with the race detector**

Run: `go vet ./internal/app/web/contract/ && go test -race -count=1 ./internal/app/web/contract/`
Expected: PASS (`TestHealth`, `TestHarnessRoundTrip`, `TestFakeAgentAnswersEveryCommandType`, `TestMatchPath`). Run it three times; a `DATA RACE` in `token.NewService` means the `newServerMu` lock is missing.

- [ ] **Step 12: `make test-pg`**

Create `scripts/test-pg.sh` (and `chmod +x scripts/test-pg.sh`):

```bash
#!/usr/bin/env bash
# Run the API contract suite against a throwaway postgres:16 container on a
# random host port (make test-pg). The container is removed on exit, pass or
# fail. Extra arguments go to `go test` (e.g. -run TestParity -v).
set -euo pipefail

GO="${GO:-go}"
cid="$(docker run -d --rm -p 127.0.0.1:0:5432 -e POSTGRES_PASSWORD=postgres postgres:16 -c max_connections=200)"
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT

port="$(docker port "$cid" 5432/tcp | head -n1 | sed 's/.*://')"
for _ in $(seq 1 60); do
	if docker exec "$cid" pg_isready -U postgres -h 127.0.0.1 >/dev/null 2>&1; then
		break
	fi
	sleep 0.5
done
docker exec "$cid" pg_isready -U postgres -h 127.0.0.1 >/dev/null

export WF_TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:${port}/postgres?sslmode=disable"
"$GO" test -count=1 "$@" ./internal/app/web/contract/
```

In `Makefile`, add `test-pg` to `.PHONY` and this target after `test`:

```make
# The API contract suite against a throwaway postgres:16 container (Docker).
# Plain `go test ./...` stays on SQLite. Extra go test flags:
# make test-pg TEST_PG_ARGS='-run TestParity -v'
test-pg:
	GO=$(GO) bash scripts/test-pg.sh $(TEST_PG_ARGS)
```

Run: `make test-pg` then `docker ps --filter ancestor=postgres:16 -q | wc -l`
Expected: `ok  winterflow/internal/app/web/contract`, then `0` (the container is gone).

- [ ] **Step 13: Commit**

```bash
git add internal/app/web/contract scripts/test-pg.sh Makefile
git commit -m "test(contract): add the API contract harness with a fake agent and spec validation

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Auth routes, and library errors documented as they arrive

**Files:**
- Create: `internal/app/web/contract/auth_test.go`
- Modify: `internal/app/web/api/openapi/openapi.go` (`addError`, `case raw:`), `internal/app/web/api/openapi/openapi_test.go` (`TestEnvelopeSchemas` cases), `api/openapi.yaml` (regenerated)

**Interfaces:**
- Consumes: Task 3 harness (`newEnv`, `anon`, `claim`, `login`, `call`, `ownerPassword`).
- Produces: nothing new for later tasks.

- [ ] **Step 1: Write the auth contract test**

Create `internal/app/web/contract/auth_test.go`:

```go
package contract_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAuthClaimFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	anon := e.anon()

	var st struct {
		Bootstrap           bool `json:"bootstrap"`
		RegistrationEnabled bool `json:"registration_enabled"`
	}
	anon.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if !st.Bootstrap || !st.RegistrationEnabled {
		t.Fatalf("fresh instance state = %+v, want bootstrap + open", st)
	}

	t.Run("register rejects a short password", func(t *testing.T) {
		anon.post(t, "/api/v1/auth/register", map[string]string{
			"name": "X", "email": "x@example.test", "password": "1",
		}).expect(t, http.StatusBadRequest)
	})

	owner := e.claim(t)

	anon.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if st.Bootstrap || st.RegistrationEnabled {
		t.Fatalf("claimed standalone state = %+v, want closed", st)
	}
	t.Run("standalone registration closes after the claim", func(t *testing.T) {
		env := anon.post(t, "/api/v1/auth/register", map[string]string{
			"name": "Late", "email": "late@example.test", "password": "password",
		}).expect(t, http.StatusBadRequest).data(t, nil)
		if env.Message != "registration is disabled" {
			t.Fatalf("message = %q", env.Message)
		}
	})
	t.Run("login with a wrong password is 403", func(t *testing.T) {
		e.anon().do(t, call{Method: http.MethodPost, Path: "/auth/local/login", Body: map[string]string{
			"user": owner.email, "passwd": "wrong", "aud": "contract",
		}}).expect(t, http.StatusForbidden)
	})
	t.Run("session endpoints", func(t *testing.T) {
		var status struct {
			Status string `json:"status"`
		}
		r := owner.get(t, "/auth/status", nil).expect(t, http.StatusOK)
		if err := json.Unmarshal(r.Body, &status); err != nil || status.Status != "logged in" {
			t.Fatalf("status = %s", r.Body)
		}
		owner.get(t, "/auth/user", nil).expect(t, http.StatusOK)
		anon.get(t, "/auth/user", nil).expect(t, http.StatusUnauthorized)
		owner.get(t, "/auth/list", nil).expect(t, http.StatusOK)
		anon.get(t, "/avatar/missing.image", nil).expect(t, http.StatusForbidden)
	})
	t.Run("logout clears the session", func(t *testing.T) {
		owner.get(t, "/auth/logout", nil).expect(t, http.StatusOK)
		owner.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusUnauthorized)
	})
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -count=1 -run TestAuthClaimFlow ./internal/app/web/contract/`
Expected: FAIL in `session endpoints`: `response 401 of GET /auth/user violates the spec: response header Content-Type has unexpected value: "text/plain; charset=utf-8"`, body `{"error":"token cookie was not presented: …"}`.

- [ ] **Step 3: Pin the generator behaviour first**

In `internal/app/web/api/openapi/openapi_test.go`, `TestEnvelopeSchemas`, add after the `"library error"` case:

```go
		{"library error as text", "POST", "/auth/local/login", 403, "text/plain", `"{\"error\":\"user is nil\"}\n"`, true},
```

Run: `go test ./internal/app/web/api/openapi/ -run TestEnvelopeSchemas`
Expected: FAIL (no `text/plain` body documented for the 403).

- [ ] **Step 4: Document library errors under both media types**

In `addError` (`internal/app/web/api/openapi/openapi.go`):

```go
	case raw:
		// go-pkgz sometimes calls WriteHeader before rest.RenderJSON sets
		// the Content-Type (/auth/user 401, unknown provider 400), so the
		// same {"error": "..."} also arrives as sniffed text/plain.
		oc.AddRespStructure(LibraryError{}, oapi.WithHTTPStatus(code))
		oc.AddRespStructure("", oapi.WithContentType("text/plain"), oapi.WithHTTPStatus(code))
```

Run: `make openapi` (adds 18 lines: a `text/plain` body to each library error response).

- [ ] **Step 5: Run everything that reads the document**

Run: `go test -count=1 ./internal/app/web/api/... ./internal/app/web/ && go test -count=1 -run 'TestAuth|TestHealth' ./internal/app/web/contract/`
Expected: PASS (`TestOpenAPIUpToDate` included).

- [ ] **Step 6: Commit**

```bash
git add internal/app/web/contract/auth_test.go internal/app/web/api/openapi api/openapi.yaml
git commit -m "test(contract): cover the auth routes and document go-pkgz text/plain errors

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: User and org routes

**Files:**
- Create: `internal/app/web/contract/user_test.go`, `internal/app/web/contract/org_test.go`

**Interfaces:**
- Consumes: Task 3 harness; exported handler types `user.Profile`, `user.CreateTokenRequest/Response`, `user.DeleteTokenRequest`, `user.ChangePasswordRequest`, `org.UpdateOrganizationRequest`, `org.UpdateMemberRequest`, `org.MemberRequest`, `org.ResetMemberPasswordResponse`, `model.UserToken`, `model.Member`, `model.Organization`.
- Produces: nothing new.

- [ ] **Step 1: Write the tests**

`internal/app/web/contract/user_test.go`:

```go
package contract_test

import (
	"net/http"
	"testing"

	"winterflow/internal/app/web/handler/user"
	"winterflow/internal/domain/model"
)

func TestUserRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	t.Run("profile", func(t *testing.T) {
		var p user.Profile
		owner.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK).data(t, &p)
		if p.UserID != owner.userID || p.Email != owner.email || p.Role != "owner" || p.MustChangePassword {
			t.Fatalf("profile = %+v", p)
		}
	})

	t.Run("tokens: create, authenticate, list, delete", func(t *testing.T) {
		var created user.CreateTokenResponse
		owner.post(t, "/api/v1/user/create-token", user.CreateTokenRequest{Name: "ci", ExpiresInDays: 30}).
			expect(t, http.StatusOK).data(t, &created)
		if created.Token == "" || created.ExpiresAt == nil {
			t.Fatalf("created = %+v", created)
		}
		bearer := e.newClient()
		bearer.bearer = created.Token
		var p user.Profile
		bearer.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK).data(t, &p)
		if p.UserID != owner.userID {
			t.Fatalf("PAT resolves to %q, want %q", p.UserID, owner.userID)
		}

		var tokens []model.UserToken
		owner.get(t, "/api/v1/user/get-tokens", nil).expect(t, http.StatusOK).data(t, &tokens)
		if len(tokens) != 1 || tokens[0].ID != created.TokenID || tokens[0].LastUsedAt == nil {
			t.Fatalf("tokens = %+v", tokens)
		}

		owner.post(t, "/api/v1/user/delete-token", user.DeleteTokenRequest{TokenID: created.TokenID}).expect(t, http.StatusOK)
		bearer.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusUnauthorized)
		owner.get(t, "/api/v1/user/get-tokens", nil).expect(t, http.StatusOK).data(t, &tokens)
		if len(tokens) != 0 {
			t.Fatalf("tokens after delete = %+v", tokens)
		}
	})

	t.Run("change password", func(t *testing.T) {
		owner.post(t, "/api/v1/user/change-password", user.ChangePasswordRequest{
			CurrentPassword: "wrong", NewPassword: "new-password",
		}).expect(t, http.StatusBadRequest)
		owner.post(t, "/api/v1/user/change-password", user.ChangePasswordRequest{
			CurrentPassword: ownerPassword, NewPassword: "new-password",
		}).expect(t, http.StatusOK)
		e.anon().login(t, owner.email, "new-password")
	})
}
```

`internal/app/web/contract/org_test.go`:

```go
package contract_test

import (
	"net/http"
	"testing"

	"winterflow/internal/app/web/handler/org"
	"winterflow/internal/domain/model"
)

func TestOrgRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	var o model.Organization
	owner.get(t, "/api/v1/org/get-organization", nil).expect(t, http.StatusOK).data(t, &o)
	if o.ID == "" {
		t.Fatalf("organization = %+v", o)
	}
	owner.post(t, "/api/v1/org/update-organization", org.UpdateOrganizationRequest{Name: "Acme", Icon: "box", Color: "#123456"}).
		expect(t, http.StatusOK)
	owner.get(t, "/api/v1/org/get-organization", nil).expect(t, http.StatusOK).data(t, &o)
	if o.Name != "Acme" || o.Icon != "box" || o.Color != "#123456" {
		t.Fatalf("organization after update = %+v", o)
	}

	m := owner.member(t)
	var members []model.Member
	owner.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK).data(t, &members)
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}

	t.Run("members cannot administer", func(t *testing.T) {
		m.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusForbidden)
		m.get(t, "/api/v1/org/get-organization", nil).expect(t, http.StatusOK)
	})

	owner.post(t, "/api/v1/org/update-member", org.UpdateMemberRequest{UserID: m.userID, Role: "admin"}).expect(t, http.StatusOK)
	m.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK)
	t.Run("the last owner cannot be demoted", func(t *testing.T) {
		owner.post(t, "/api/v1/org/update-member", org.UpdateMemberRequest{UserID: owner.userID, Role: "member"}).
			expect(t, http.StatusBadRequest)
	})

	var reset org.ResetMemberPasswordResponse
	owner.post(t, "/api/v1/org/reset-member-password", org.MemberRequest{UserID: m.userID}).
		expect(t, http.StatusOK).data(t, &reset)
	e.anon().login(t, m.email, reset.TempPassword)

	owner.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: m.userID}).expect(t, http.StatusOK)
	owner.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK).data(t, &members)
	if len(members) != 1 {
		t.Fatalf("members after remove = %+v", members)
	}
	t.Run("cannot remove yourself", func(t *testing.T) {
		owner.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: owner.userID}).expect(t, http.StatusBadRequest)
	})
}
```

- [ ] **Step 2: Run on both databases**

Run: `go test -count=1 -run 'TestUser|TestOrg' ./internal/app/web/contract/ && make test-pg TEST_PG_ARGS="-run 'TestUser|TestOrg'"`
Expected: PASS on both. These pin existing behaviour; a failure here is a contract break in production code — stop and report it rather than editing the assertion.

- [ ] **Step 3: Commit**

```bash
git add internal/app/web/contract/user_test.go internal/app/web/contract/org_test.go
git commit -m "test(contract): cover the user and org routes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Server routes, and UTC timestamps on Postgres

**Files:**
- Create: `internal/app/web/contract/server_test.go`, `internal/infra/db/bun_test.go`
- Modify: `internal/app/web/contract/db_test.go` (`freshDB`), `internal/infra/db/bun.go` (`setupPostgreSQL`, new `utcDSN`)

**Interfaces:**
- Consumes: Task 3 harness (`publishEvent`, `eventually`, `seedPendingServer`, `deps.ServerRepository.SaveCapabilities`).
- Produces: `func db.utcDSN(connStr string) string` (unexported).

- [ ] **Step 1: Connect the Postgres harness with a non-UTC session zone**

In `freshDB` (`internal/app/web/contract/db_test.go`), after `q.Set("search_path", schema)`:

```go
	// A non-UTC session zone: the server must pin UTC itself (db.utcDSN).
	q.Set("timezone", "America/New_York")
```

- [ ] **Step 2: Write the server contract test**

Create `internal/app/web/contract/server_test.go`:

```go
package contract_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/transport/bus"
)

func TestServerRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t) // the login auto-claimed the embedded server

	t.Run("liveness follows the events queue", func(t *testing.T) {
		var st server.GetServersStatusResponse
		owner.get(t, "/api/v1/server/get-servers-status", nil).expect(t, http.StatusOK).data(t, &st)
		if len(st.Servers) != 1 || st.Servers[0].Liveness != status.LivenessUnknown {
			t.Fatalf("before heartbeat: %+v", st)
		}
		e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventServerOnline})
		eventually(t, func() bool {
			owner.get(t, "/api/v1/server/get-servers-status", nil).expect(t, http.StatusOK).data(t, &st)
			return st.Servers[0].Liveness == status.LivenessOnline
		})
	})

	t.Run("public key comes from the capability", func(t *testing.T) {
		q := url.Values{"server_id": {sid}}
		owner.get(t, "/api/v1/server/get-public-key", q).expect(t, http.StatusBadRequest)
		if err := e.deps.ServerRepository.SaveCapabilities(e.ctx, sid, map[string]string{"public_key": "BASE64POINT"}, nil); err != nil {
			t.Fatal(err)
		}
		var pk server.PublicKeyResponse
		owner.get(t, "/api/v1/server/get-public-key", q).expect(t, http.StatusOK).data(t, &pk)
		if pk.PublicKey != "BASE64POINT" {
			t.Fatalf("public key = %+v", pk)
		}
	})

	t.Run("register claims a pending server by code", func(t *testing.T) {
		code := e.seedPendingServer(t)
		owner.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: "ZZZZZZ"}).expect(t, http.StatusBadRequest)
		var reg server.RegisterServerResponse
		owner.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: code}).expect(t, http.StatusOK).data(t, &reg)
		var list server.GetServersResponse
		owner.get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Servers) != 2 {
			t.Fatalf("servers after register = %+v", list)
		}
		// Timestamps round-trip as UTC on both databases (Postgres reads
		// naive strings in the session zone).
		for _, s := range list.Servers {
			if skew := time.Since(s.CreatedAt); skew < -time.Minute || skew > time.Minute {
				t.Fatalf("created_at %s is %s away from now", s.CreatedAt, skew)
			}
		}
	})
}
```

- [ ] **Step 3: Run it on both databases to see Postgres fail**

Run: `go test -count=1 -run TestServerRoutes ./internal/app/web/contract/ && make test-pg TEST_PG_ARGS='-run TestServerRoutes'`
Expected: SQLite PASS; Postgres FAIL: `created_at 2026-… -0400 -0400 is -3h59m59s away from now`.

- [ ] **Step 4: Unit-test the DSN rewrite**

Create `internal/infra/db/bun_test.go`:

```go
package db

import (
	"net/url"
	"testing"
)

func TestUTCDSNPinsTimeZone(t *testing.T) {
	for _, in := range []string{
		"postgres://u:p@db:5432/wf",
		"postgres://u:p@db:5432/wf?sslmode=disable&timezone=America/New_York",
		"postgres://u:p@db/wf?search_path=t_1",
	} {
		out := utcDSN(in)
		u, _ := url.Parse(out)
		q := u.Query()
		if q.Get("timezone") != "UTC" {
			t.Errorf("%s -> %s: timezone not pinned", in, out)
		}
		orig, _ := url.Parse(in)
		for k := range orig.Query() {
			if k != "timezone" && q.Get(k) != orig.Query().Get(k) {
				t.Errorf("%s -> %s: lost %s", in, out, k)
			}
		}
	}
	if bad := "postgres://u:p@db:5432/wf\x7f"; utcDSN(bad) != bad {
		t.Error("an unparsable DSN must pass through unchanged")
	}
}
```

Run: `go test ./internal/infra/db/ -run TestUTCDSNPinsTimeZone`
Expected: FAIL, `undefined: utcDSN`.

- [ ] **Step 5: Pin UTC on every Postgres connection**

In `internal/infra/db/bun.go` add `"net/url"` to the imports, change the first line of `setupPostgreSQL`, and add `utcDSN` after it:

```go
func setupPostgreSQL(connStr string, log *logger.Logger) (*sql.DB, error) {
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(utcDSN(connStr))))
```

```go
// utcDSN pins the session time zone to UTC. types.DateTime writes naive
// UTC wall-clock strings ("2006-01-02 15:04:05"); a TIMESTAMPTZ column
// reads those in the session zone, so any other server/role time zone would
// shift every stored timestamp by its offset. An unparsable DSN is
// returned unchanged for pgdriver to reject with its own error.
func utcDSN(connStr string) string {
	u, err := url.Parse(connStr)
	if err != nil {
		return connStr
	}
	q := u.Query()
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	return u.String()
}
```

(`pgdriver.WithConnParams` would *replace* the DSN's other parameters such as `search_path`, so the DSN itself is rewritten. Unknown query parameters are sent as Postgres startup parameters, which is how `timezone` and `search_path` take effect.)

- [ ] **Step 6: Run both**

Run: `go test -count=1 ./internal/infra/db/ && go test -count=1 -run TestServerRoutes ./internal/app/web/contract/ && make test-pg TEST_PG_ARGS='-run TestServerRoutes'`
Expected: PASS everywhere.

- [ ] **Step 7: Commit**

```bash
git add internal/app/web/contract/db_test.go internal/app/web/contract/server_test.go internal/infra/db/bun.go internal/infra/db/bun_test.go
git commit -m "fix(db): pin the Postgres session time zone to UTC

Timestamps are written as naive UTC strings into TIMESTAMPTZ columns, so a
non-UTC session zone shifted every stored time. The contract suite now
connects with America/New_York and covers the server routes.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: App routes, and `app_domains` UUID columns on Postgres

**Files:**
- Create: `internal/app/web/contract/apps_test.go`, `internal/infra/db/migrations/20261010000001_app_domains_uuid.go`

**Interfaces:**
- Consumes: Task 3 harness; `happ.SaveAppRequest`, `happ.ContentItemRequest`, `happ.GetAppsResponse`, `happ.AppWithDomains`, `happ.CheckDomainResponse`, `happ.ControlAppRequest`, `happ.RollbackAppRequest`, `happ.RenameAppRequest`, `happ.DeleteAppRequest` (package `server` at `internal/app/web/handler/app`, imported as `happ`).
- Produces: `func saveApp(t, c *client, serverID, name string, config json.RawMessage) string` and `func listApps(t, c *client, serverID string) []happ.AppWithDomains` (package `contract_test`; Task 10 does not depend on them).

- [ ] **Step 1: Write the app contract tests**

Create `internal/app/web/contract/apps_test.go`:

```go
package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
)

// saveApp creates an app through the API and returns its id.
func saveApp(t *testing.T, c *client, serverID, name string, config json.RawMessage) string {
	t.Helper()
	if config == nil {
		config = json.RawMessage(`{}`)
	}
	var saved command.SaveAppResponse
	c.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: serverID,
		App:      model.App{Name: name, Icon: "box", Color: "#112233"},
		Config:   config,
		Files:    []happ.ContentItemRequest{{Name: "compose.yaml", Content: "services: {}"}},
	}}, &saved)
	if saved.AppID == "" || saved.Revision == "" {
		t.Fatalf("save result = %+v", saved)
	}
	return saved.AppID
}

// listApps is GET get-apps (the DB cache).
func listApps(t *testing.T, c *client, serverID string) []happ.AppWithDomains {
	t.Helper()
	var d happ.GetAppsResponse
	c.get(t, "/api/v1/app/get-apps", url.Values{"server_id": {serverID}}).expect(t, http.StatusOK).data(t, &d)
	return d.Apps
}

func TestAppLifecycle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	ingress := json.RawMessage(`{"ingress":{"domains":[{"domain":"web.example.test","upstream_port":8080}]}}`)

	appID := saveApp(t, owner, sid, "web", ingress)
	apps := listApps(t, owner, sid)
	if len(apps) != 1 || apps[0].ID != appID || apps[0].Name != "web" || len(apps[0].Domains) != 1 {
		t.Fatalf("after save: %+v", apps)
	}
	if got := e.agent.seen(command.TypeAppSave); len(got) != 1 || got[0].AgentID != sid {
		t.Fatalf("app.save reached the agent as %+v", got)
	}

	t.Run("domain check sees the claim", func(t *testing.T) {
		var d happ.CheckDomainResponse
		owner.get(t, "/api/v1/domains/check", url.Values{"domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
		if d.Available || len(d.Claims) != 1 || d.Claims[0].AppName != "web" {
			t.Fatalf("check = %+v", d)
		}
		owner.get(t, "/api/v1/domains/check", url.Values{"domain": {"free.example.test"}}).expect(t, http.StatusOK).data(t, &d)
		if !d.Available {
			t.Fatalf("free domain = %+v", d)
		}
	})
	t.Run("a second app cannot take the domain", func(t *testing.T) {
		owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: "dup", Icon: "box", Color: "#112233"}, Config: ingress,
		}}).expect(t, http.StatusBadRequest)
	})

	q := url.Values{"server_id": {sid}, "app_id": {appID}}
	t.Run("agent reads", func(t *testing.T) {
		var got command.GetAppResponse
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-app", Query: q}, &got)
		if got.App.AppID != appID {
			t.Fatalf("get-app = %+v", got)
		}
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-revisions", Query: q}, nil)
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-logs",
			Query: url.Values{"server_id": {sid}, "app_id": {appID}, "tail": {"50"}}}, nil)
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/image/get-tags",
			Query: url.Values{"server_id": {sid}, "image": {"nginx"}}}, nil)
	})
	t.Run("control and rollback", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/control-app",
			Body: happ.ControlAppRequest{ServerID: sid, AppID: appID, Action: command.ControlRestart}}, nil)
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/rollback-app",
			Body: happ.RollbackAppRequest{ServerID: sid, AppID: appID, Hash: "rev-1"}}, nil)
	})
	t.Run("rename updates the DB", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/rename-app",
			Body: happ.RenameAppRequest{ServerID: sid, AppID: appID, Name: "web-2"}}, nil)
		if apps := listApps(t, owner, sid); apps[0].Name != "web-2" {
			t.Fatalf("after rename: %+v", apps)
		}
	})
	t.Run("status comes from the events queue", func(t *testing.T) {
		body, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{{AppID: appID, StatusCode: 1, Containers: []command.ContainerStatus{}}}})
		e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventAppsStatus, Payload: body})
		var st command.GetAppsStatusResponse
		eventually(t, func() bool {
			owner.get(t, "/api/v1/app/get-apps-status", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &st)
			return len(st.Apps) == 1
		})
	})
	t.Run("refresh reconciles from the agent", func(t *testing.T) {
		e.agent.on(command.TypeAppsList, func(json.RawMessage) reply {
			return reply{Payload: command.ListAppsResponse{Apps: []model.App{{ID: appID, Name: "renamed-on-disk", Icon: "box", Color: "#112233"}}}}
		})
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Query: url.Values{"server_id": {sid}}}, nil)
		if apps := listApps(t, owner, sid); len(apps) != 1 || apps[0].Name != "renamed-on-disk" {
			t.Fatalf("after refresh: %+v", apps)
		}
	})
	t.Run("delete removes the app and its domains", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/delete-app",
			Body: happ.DeleteAppRequest{ServerID: sid, AppID: appID}}, nil)
		if apps := listApps(t, owner, sid); len(apps) != 0 {
			t.Fatalf("after delete: %+v", apps)
		}
		var d happ.CheckDomainResponse
		owner.get(t, "/api/v1/domains/check", url.Values{"domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
		if !d.Available {
			t.Fatalf("domain still claimed: %+v", d)
		}
	})
}

// TestAppAgentErrors: one error-scripted case per command family. An agent
// failure is status 1 with the message and leaves the DB untouched.
func TestAppAgentErrors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	appID := saveApp(t, owner, sid, "web", nil)

	e.agent.fail(command.TypeAppSave, "compose failed")
	owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: sid, App: model.App{Name: "broken", Icon: "box", Color: "#112233"}, Config: json.RawMessage(`{}`),
	}}, "compose failed")
	e.agent.fail(command.TypeAppRename, "rename failed")
	owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/app/rename-app",
		Body: happ.RenameAppRequest{ServerID: sid, AppID: appID, Name: "x"}}, "rename failed")
	e.agent.fail(command.TypeAppDelete, "delete failed")
	owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/app/delete-app",
		Body: happ.DeleteAppRequest{ServerID: sid, AppID: appID}}, "delete failed")
	e.agent.fail(command.TypeAppGet, "no such app")
	owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-app",
		Query: url.Values{"server_id": {sid}, "app_id": {appID}}}, "no such app")

	if apps := listApps(t, owner, sid); len(apps) != 1 || apps[0].Name != "web" {
		t.Fatalf("failed commands changed the DB: %+v", apps)
	}
}
```

- [ ] **Step 2: Run on both databases to see Postgres fail**

Run: `go test -count=1 -run TestApp ./internal/app/web/contract/ && make test-pg TEST_PG_ARGS='-run TestApp'`
Expected: SQLite PASS; Postgres FAIL in `TestAppLifecycle`: `status = 400, want 202; body: {"success":false,"message":"failed to save app","data":null}`. The JSON error log just above it names the cause: `"msg":"SaveApp: find domain claims" … "error":"ERROR: operator does not exist: uuid = character (SQLSTATE=42883)"`.

- [ ] **Step 3: Retype the columns on Postgres**

Create `internal/infra/db/migrations/20261010000001_app_domains_uuid.go`:

```go
package migrations

import (
	"context"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	// No down step: narrowing back to CHAR(36) would only restore the bug.
	Migrations.MustRegister(appDomainsUUIDColumns, nil)
}

// appDomainsUUIDColumns retypes app_domains.app_id/server_id to UUID on
// Postgres. They were CHAR(36) while apps.app_id and servers.server_id are
// UUID, so FindClaims' join (app_domain -> app, server) failed with
// "operator does not exist: uuid = character" and every save-app with an
// ingress domain answered 400. SQLite has no UUID type: nothing to do.
func appDomainsUUIDColumns(ctx context.Context, db *bun.DB) error {
	if db.Dialect().Name() != dialect.PG {
		return nil
	}
	_, err := db.ExecContext(ctx, `ALTER TABLE app_domains
		ALTER COLUMN app_id TYPE UUID USING app_id::uuid,
		ALTER COLUMN server_id TYPE UUID USING server_id::uuid`)
	return err
}
```

(A nil down step is allowed by bun's migrator. Statement-level error handling is deliberately absent so the only Postgres-only lines are the `ALTER`, which Task 12's merged profile covers. Ids are UUIDv7 everywhere they come from, and `apps.app_id` is already `UUID`, so the `USING …::uuid` cast cannot meet a non-UUID row on an existing Postgres install.)

- [ ] **Step 4: Run both**

Run: `go test -count=1 ./internal/infra/db/... && go test -count=1 -run TestApp ./internal/app/web/contract/ && make test-pg TEST_PG_ARGS='-run TestApp'`
Expected: PASS everywhere.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web/contract/apps_test.go internal/infra/db/migrations/20261010000001_app_domains_uuid.go
git commit -m "fix(db): make app_domains ids UUID on Postgres

FindClaims joins app_domains (CHAR(36)) to apps/servers (UUID), which
Postgres rejects, so every save-app with an ingress domain answered 400.
The contract suite now covers the app routes on both databases.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Docker routes

**Files:**
- Create: `internal/app/web/contract/docker_test.go`

**Interfaces:**
- Consumes: Task 3 harness; `command.CreateRegistryRequest`, `DeleteRegistryRequest`, `CreateNetworkRequest`, `DeleteNetworkRequest`, `CreateVolumeRequest`, `DeleteVolumeRequest`, `UpdateAgentRequest`.
- Produces: nothing new.

- [ ] **Step 1: Write the docker contract test**

Create `internal/app/web/contract/docker_test.go`. Overrides persist for the rest of the test, so the member check uses `volume/list`, which the error subtest left alone:

```go
package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/domain/command"
)

func TestDockerRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	q := url.Values{"server_id": {owner.serverID(t)}}

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/registry/list", nil},
		{http.MethodPost, "/api/v1/registry/create", command.CreateRegistryRequest{Address: "ghcr.io", Username: "u", Password: "p"}},
		{http.MethodPost, "/api/v1/registry/delete", command.DeleteRegistryRequest{Address: "ghcr.io"}},
		{http.MethodGet, "/api/v1/network/list", nil},
		{http.MethodPost, "/api/v1/network/create", command.CreateNetworkRequest{Name: "edge"}},
		{http.MethodPost, "/api/v1/network/delete", command.DeleteNetworkRequest{Name: "edge"}},
		{http.MethodGet, "/api/v1/volume/list", nil},
		{http.MethodPost, "/api/v1/volume/create", command.CreateVolumeRequest{Name: "data"}},
		{http.MethodPost, "/api/v1/volume/delete", command.DeleteVolumeRequest{Name: "data"}},
		{http.MethodPost, "/api/v1/agent/update", command.UpdateAgentRequest{Version: "v261010.1"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			owner.succeed(t, call{Method: tc.method, Path: tc.path, Query: q, Body: tc.body}, nil)
		})
	}

	t.Run("agent errors", func(t *testing.T) {
		e.agent.fail(command.TypeRegistryCreate, "login failed")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/registry/create", Query: q,
			Body: command.CreateRegistryRequest{Address: "ghcr.io", Username: "u", Password: "p"}}, "login failed")
		e.agent.fail(command.TypeNetworkList, "docker down")
		owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/network/list", Query: q}, "docker down")
		e.agent.fail(command.TypeVolumeDelete, "volume in use")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/volume/delete", Query: q,
			Body: command.DeleteVolumeRequest{Name: "data"}}, "volume in use")
		e.agent.fail(command.TypeAgentUpdate, "no such release")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/agent/update", Query: q,
			Body: command.UpdateAgentRequest{Version: "v0"}}, "no such release")
	})

	t.Run("members cannot mutate infrastructure", func(t *testing.T) {
		m := owner.member(t)
		m.do(t, call{Method: http.MethodPost, Path: "/api/v1/network/create", Query: q,
			Body: command.CreateNetworkRequest{Name: "x"}}).expect(t, http.StatusForbidden)
		m.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/volume/list", Query: q}, nil)
	})
}
```

- [ ] **Step 2: Run on both databases**

Run: `go test -count=1 -run TestDocker ./internal/app/web/contract/ && make test-pg TEST_PG_ARGS='-run TestDocker'`
Expected: PASS on both.

- [ ] **Step 3: Commit**

```bash
git add internal/app/web/contract/docker_test.go
git commit -m "test(contract): cover the registry, network, volume and agent routes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Cross-cutting 401 / 403 / 400 matrix

**Files:**
- Create: `internal/app/web/contract/matrix_test.go`

**Interfaces:**
- Consumes: Task 3 harness (`table`, `foreignServer`, `agent.count`).
- Produces: `type probe struct{ valid func(serverID string) call; malformed call }`; `var probes map[string]probe` keyed `"METHOD /path"`.

- [ ] **Step 1: Write the matrix**

Create `internal/app/web/contract/matrix_test.go`:

```go
package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/app/web/api"
)

// probe describes how to call one route for the cross-cutting checks.
type probe struct {
	// valid builds a well-formed call addressed at serverID; nil for
	// routes that are not server-addressed.
	valid func(serverID string) call
	// malformed is one call the route must answer 400 (bad body, missing
	// parameter); the zero value means the route takes no input to break.
	malformed call
}

func sq(id string) url.Values { return url.Values{"server_id": {id}} }
func aq(id string) url.Values {
	return url.Values{"server_id": {id}, "app_id": {"00000000-0000-0000-0000-000000000001"}}
}
func post(path string, body any) call    { return call{Method: http.MethodPost, Path: path, Body: body} }
func get(path string, q url.Values) call { return call{Method: http.MethodGet, Path: path, Query: q} }
func bad(method, path string) call {
	if method == http.MethodGet {
		return call{Method: method, Path: path, Invalid: true}
	}
	return call{Method: method, Path: path, Body: "{", Invalid: true}
}

const anApp = "00000000-0000-0000-0000-000000000001"

// probes covers every route that takes input or is server-addressed.
// TestProbesCoverTable keeps it in step with the route table.
var probes = map[string]probe{
	"GET /api/v1/app/get-apps":        {valid: func(s string) call { return get("/api/v1/app/get-apps", sq(s)) }, malformed: bad("GET", "/api/v1/app/get-apps")},
	"GET /api/v1/app/get-apps-status": {valid: func(s string) call { return get("/api/v1/app/get-apps-status", sq(s)) }, malformed: bad("GET", "/api/v1/app/get-apps-status")},
	"POST /api/v1/app/refresh-apps": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Query: sq(s)}
	}, malformed: call{Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Invalid: true}},
	"POST /api/v1/app/save-app": {valid: func(s string) call {
		return post("/api/v1/app/save-app", map[string]any{"server_id": s, "app": map[string]any{"name": "x"}, "config": map[string]any{}})
	}, malformed: bad("POST", "/api/v1/app/save-app")},
	"GET /api/v1/app/get-app":       {valid: func(s string) call { return get("/api/v1/app/get-app", aq(s)) }, malformed: bad("GET", "/api/v1/app/get-app")},
	"GET /api/v1/app/get-logs":      {valid: func(s string) call { return get("/api/v1/app/get-logs", aq(s)) }, malformed: bad("GET", "/api/v1/app/get-logs")},
	"GET /api/v1/app/get-revisions": {valid: func(s string) call { return get("/api/v1/app/get-revisions", aq(s)) }, malformed: bad("GET", "/api/v1/app/get-revisions")},
	"POST /api/v1/app/rollback-app": {valid: func(s string) call {
		return post("/api/v1/app/rollback-app", map[string]any{"server_id": s, "app_id": anApp, "hash": "rev-1"})
	}, malformed: bad("POST", "/api/v1/app/rollback-app")},
	"GET /api/v1/image/get-tags": {valid: func(s string) call {
		return get("/api/v1/image/get-tags", url.Values{"server_id": {s}, "image": {"nginx"}})
	}, malformed: bad("GET", "/api/v1/image/get-tags")},
	"POST /api/v1/app/control-app": {valid: func(s string) call {
		return post("/api/v1/app/control-app", map[string]any{"server_id": s, "app_id": anApp, "action": "start"})
	}, malformed: bad("POST", "/api/v1/app/control-app")},
	"POST /api/v1/app/delete-app": {valid: func(s string) call {
		return post("/api/v1/app/delete-app", map[string]any{"server_id": s, "app_id": anApp})
	}, malformed: bad("POST", "/api/v1/app/delete-app")},
	"POST /api/v1/app/rename-app": {valid: func(s string) call {
		return post("/api/v1/app/rename-app", map[string]any{"server_id": s, "app_id": anApp, "name": "y"})
	}, malformed: bad("POST", "/api/v1/app/rename-app")},
	"GET /api/v1/domains/check": {malformed: call{Method: http.MethodGet, Path: "/api/v1/domains/check",
		Query: url.Values{"domain": {"not a domain"}}}},

	"GET /api/v1/registry/list": {valid: func(s string) call { return get("/api/v1/registry/list", sq(s)) }, malformed: bad("GET", "/api/v1/registry/list")},
	"POST /api/v1/registry/create": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/registry/create", Query: sq(s), Body: map[string]any{"address": "ghcr.io", "username": "u"}}
	}, malformed: bad("POST", "/api/v1/registry/create")},
	"POST /api/v1/registry/delete": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/registry/delete", Query: sq(s), Body: map[string]any{"address": "ghcr.io"}}
	}, malformed: bad("POST", "/api/v1/registry/delete")},
	"GET /api/v1/network/list": {valid: func(s string) call { return get("/api/v1/network/list", sq(s)) }, malformed: bad("GET", "/api/v1/network/list")},
	"POST /api/v1/network/create": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/network/create", Query: sq(s), Body: map[string]any{"name": "n"}}
	}, malformed: bad("POST", "/api/v1/network/create")},
	"POST /api/v1/network/delete": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/network/delete", Query: sq(s), Body: map[string]any{"name": "n"}}
	}, malformed: bad("POST", "/api/v1/network/delete")},
	"GET /api/v1/volume/list": {valid: func(s string) call { return get("/api/v1/volume/list", sq(s)) }, malformed: bad("GET", "/api/v1/volume/list")},
	"POST /api/v1/volume/create": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/volume/create", Query: sq(s), Body: map[string]any{"name": "v"}}
	}, malformed: bad("POST", "/api/v1/volume/create")},
	"POST /api/v1/volume/delete": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/volume/delete", Query: sq(s), Body: map[string]any{"name": "v"}}
	}, malformed: bad("POST", "/api/v1/volume/delete")},
	"POST /api/v1/agent/update": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/agent/update", Query: sq(s), Body: map[string]any{"version": "v1"}}
	}, malformed: bad("POST", "/api/v1/agent/update")},

	"GET /api/v1/server/get-public-key": {valid: func(s string) call { return get("/api/v1/server/get-public-key", sq(s)) }, malformed: bad("GET", "/api/v1/server/get-public-key")},
	"POST /api/v1/server/register":      {malformed: post("/api/v1/server/register", map[string]any{"code": "1"})},

	"POST /api/v1/auth/register":             {malformed: bad("POST", "/api/v1/auth/register")},
	"POST /api/v1/user/create-token":         {malformed: post("/api/v1/user/create-token", map[string]any{"name": ""})},
	"POST /api/v1/user/delete-token":         {malformed: bad("POST", "/api/v1/user/delete-token")},
	"POST /api/v1/user/change-password":      {malformed: post("/api/v1/user/change-password", map[string]any{"current_password": "x", "new_password": "1"})},
	"POST /api/v1/org/create-user":           {malformed: post("/api/v1/org/create-user", map[string]any{"name": "n", "email": "not-an-email", "role": "member"})},
	"POST /api/v1/org/update-member":         {malformed: post("/api/v1/org/update-member", map[string]any{"user_id": "u", "role": "emperor"})},
	"POST /api/v1/org/remove-member":         {malformed: post("/api/v1/org/remove-member", map[string]any{"user_id": ""})},
	"POST /api/v1/org/reset-member-password": {malformed: post("/api/v1/org/reset-member-password", map[string]any{"user_id": ""})},
	"POST /api/v1/org/update-organization":   {malformed: post("/api/v1/org/update-organization", map[string]any{"name": ""})},
}

// TestProbesCoverTable: every non-Mounted route with input (query or body)
// has a probe with a malformed call, and every probe names a real route.
func TestProbesCoverTable(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, rt := range table {
		key := rt.Method + " " + rt.Path
		known[key] = true
		if rt.Mounted || (rt.Query == nil && rt.Request == nil) {
			continue
		}
		if p, ok := probes[key]; !ok || p.malformed.Path == "" {
			t.Errorf("%s takes input but has no malformed probe", key)
		}
	}
	for key := range probes {
		if !known[key] {
			t.Errorf("probe %s names no route", key)
		}
	}
}

// TestUnauthenticated: every User route answers 401 to an anonymous call.
func TestUnauthenticated(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	anon := e.anon()
	for _, rt := range table {
		if rt.Auth != api.User {
			continue
		}
		t.Run(rt.Method+" "+rt.Path, func(t *testing.T) {
			anon.do(t, call{Method: rt.Method, Path: rt.Path, Invalid: true}).expect(t, http.StatusUnauthorized)
		})
	}
}

// TestForeignServer: a server of another org is 403 on every
// server-addressed route, before anything reaches the agent.
func TestForeignServer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	foreign := e.foreignServer(t)
	for key, p := range probes {
		if p.valid == nil {
			continue
		}
		t.Run(key, func(t *testing.T) {
			owner.do(t, p.valid(foreign)).expect(t, http.StatusForbidden)
		})
	}
	if n := e.agent.count(); n != 0 {
		t.Fatalf("%d command(s) reached the agent for a foreign server", n)
	}
}

// TestMalformed: one bad body or missing parameter per route is 400.
func TestMalformed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	for key, p := range probes {
		if p.malformed.Path == "" {
			continue
		}
		t.Run(key, func(t *testing.T) {
			owner.do(t, p.malformed).expect(t, http.StatusBadRequest)
		})
	}
}
```

- [ ] **Step 2: Prove the table check bites**

Temporarily delete the `"POST /api/v1/org/update-organization"` entry from `probes` and run `go test -count=1 -run TestProbesCoverTable ./internal/app/web/contract/`.
Expected: FAIL, `POST /api/v1/org/update-organization takes input but has no malformed probe`. Restore the entry.

- [ ] **Step 3: Run on both databases**

Run: `go test -count=1 -run 'TestProbes|TestUnauthenticated|TestForeignServer|TestMalformed' ./internal/app/web/contract/ && make test-pg TEST_PG_ARGS="-run 'TestProbes|TestUnauthenticated|TestForeignServer|TestMalformed'"`
Expected: PASS on both (anonymous calls answer the go-pkgz `text/plain` 401 or the webutil JSON 401, both documented).

- [ ] **Step 4: Commit**

```bash
git add internal/app/web/contract/matrix_test.go
git commit -m "test(contract): assert 401, foreign-org 403 and 400 across the route table

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: SQLite/Postgres parity transcript, with deterministic list order

**Files:**
- Create: `internal/app/web/contract/parity_test.go`, `internal/app/web/contract/diff_test.go`, `internal/app/web/contract/testdata/parity.golden.json` (generated)
- Modify: `internal/infra/db/repository/app.go` (`GetApps`), `server.go` (`GetServers`), `user.go` (`ListTokens`, `ListMembers`), `app_test.go`

**Interfaces:**
- Consumes: Task 3 harness (`claim`, `bearer`, `seedPendingServer`, `awaitResult`, `accepted`).
- Produces: `var update = flag.Bool("update", …)` (package `contract_test`); `func lineDiff(want, got string) string`.

- [ ] **Step 1: Pin the tie-break**

Append to `internal/infra/db/repository/app_test.go` (add `"strings"` and `"time"` to its imports):

```go
// GetApps breaks created_at ties (second precision) by app id, so both
// databases list apps saved in the same second in the same order.
func TestGetAppsOrdersTiesByID(t *testing.T) {
	repo := newAppRepo(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"app-b", "app-a", "app-c"} {
		if err := repo.SaveApp(ctx, model.App{ID: id, ServerID: "srv-1", Name: id, Icon: "i", Color: "#000000", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.GetApps(ctx, "srv-1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range list {
		got = append(got, a.ID)
	}
	if strings.Join(got, ",") != "app-a,app-b,app-c" {
		t.Fatalf("order = %v, want app-a,app-b,app-c", got)
	}
}
```

Run: `go test ./internal/infra/db/repository/ -run TestGetAppsOrdersTiesByID`
Expected: FAIL, `order = [app-b app-a app-c]`.

- [ ] **Step 2: Add primary-key tie-breaks**

`internal/infra/db/repository/app.go`, `GetApps`:

```go
		Where("server_id = ?", serverID).
		// created_at has second precision: break ties by the (UUIDv7,
		// time-ordered) id so SQLite and Postgres list in the same order.
		Order("created_at ASC", "app_id ASC").
```

`internal/infra/db/repository/server.go`, `GetServers`: `Order("created_at ASC", "server_id ASC").`
`internal/infra/db/repository/user.go`, `ListTokens`: `Order("created_at DESC", "token_id DESC").`
`internal/infra/db/repository/user.go`, `ListMembers`: `Order("organization_user.created_at ASC", "organization_user.user_id ASC").`

Run: `go test -count=1 ./internal/infra/db/repository/`
Expected: PASS.

- [ ] **Step 3: Write the parity test**

`internal/app/web/contract/diff_test.go`:

```go
package contract_test

import (
	"fmt"
	"strings"
)

// lineDiff lists the lines that differ between want and got (enough to
// spot a parity break without a diff dependency).
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			fmt.Fprintf(&b, "line %d:\n  want %s\n  got  %s\n", i+1, wl, gl)
		}
	}
	return b.String()
}
```

`internal/app/web/contract/parity_test.go`:

```go
package contract_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/app/web/handler/user"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

var (
	goldenPath = filepath.Join("testdata", "parity.golden.json")
	update     = flag.Bool("update", false, "rewrite testdata/parity.golden.json")
)

// step is one transcript entry: what was called and what came back.
type step struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	Body   any    `json:"body"`
}

var (
	uuidRE  = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	timeRE  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	tokenRE = regexp.MustCompile(`wfp_[A-Za-z0-9_-]+`)
)

// normalizer replaces volatile values with stable placeholders: the n-th
// distinct UUID becomes <id-n>, every timestamp <time>, every PAT <token>.
// Re-decoding into any and re-encoding sorts object keys.
type normalizer struct{ ids map[string]string }

func (n *normalizer) body(t *testing.T, raw []byte) any {
	t.Helper()
	s := tokenRE.ReplaceAllString(string(raw), "<token>")
	s = timeRE.ReplaceAllString(s, "<time>")
	s = uuidRE.ReplaceAllStringFunc(s, func(id string) string {
		if p, ok := n.ids[id]; ok {
			return p
		}
		p := fmt.Sprintf("<id-%d>", len(n.ids)+1)
		n.ids[id] = p
		return p
	})
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("normalize %s: %v", raw, err)
	}
	return v
}

// TestParity runs one fixed scenario and compares the normalized transcript
// with testdata/parity.golden.json. Both databases must produce the same
// file; -update rewrites it (run it on SQLite, then confirm on Postgres).
func TestParity(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	n := &normalizer{ids: map[string]string{}}
	var steps []step
	rec := func(name string, r *response) *response {
		steps = append(steps, step{Name: name, Status: r.Status, Body: n.body(t, r.Body)})
		return r
	}
	result := func(name string, c *client, k call) json.RawMessage {
		ev := awaitResult(t, c, c.accepted(t, k))
		steps = append(steps, step{Name: name + " (result)", Status: int(ev.Status), Body: n.body(t, ev.Payload)})
		return ev.Payload
	}

	owner := e.claim(t)
	var tok user.CreateTokenResponse
	rec("create token", owner.post(t, "/api/v1/user/create-token", user.CreateTokenRequest{Name: "parity"})).
		expect(t, http.StatusOK).data(t, &tok)
	p := e.bearer(t, tok.Token, owner)
	code := e.seedPendingServer(t)
	var reg server.RegisterServerResponse
	rec("register server", p.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: code})).
		expect(t, http.StatusOK).data(t, &reg)
	sid := reg.Server.ID

	var ids []string
	for _, name := range []string{"alpha", "beta"} {
		raw := result("save "+name, p, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: name, Icon: "box", Color: "#112233"}, Config: json.RawMessage(`{}`),
		}})
		var saved command.SaveAppResponse
		_ = json.Unmarshal(raw, &saved)
		ids = append(ids, saved.AppID)
	}
	q := url.Values{"server_id": {sid}}
	rec("list apps", p.get(t, "/api/v1/app/get-apps", q)).expect(t, http.StatusOK)
	result("rename alpha", p, call{Method: http.MethodPost, Path: "/api/v1/app/rename-app",
		Body: happ.RenameAppRequest{ServerID: sid, AppID: ids[0], Name: "alpha-2"}})
	result("delete beta", p, call{Method: http.MethodPost, Path: "/api/v1/app/delete-app",
		Body: happ.DeleteAppRequest{ServerID: sid, AppID: ids[1]}})
	rec("list apps again", p.get(t, "/api/v1/app/get-apps", q)).expect(t, http.StatusOK)
	rec("list members", p.get(t, "/api/v1/org/get-members", nil)).expect(t, http.StatusOK)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep "<id-1>" readable in the golden file
	enc.SetIndent("", "  ")
	if err := enc.Encode(steps); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	if *update {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (run go test ./internal/app/web/contract -run TestParity -update)", goldenPath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s transcript differs from %s (dialect %s):\n%s", dialect(), goldenPath, dialect(), lineDiff(string(want), string(got)))
	}
}
```

- [ ] **Step 4: Run it without a golden**

Run: `go test -count=1 -run TestParity ./internal/app/web/contract/`
Expected: FAIL, `read testdata/parity.golden.json: … (run go test ./internal/app/web/contract -run TestParity -update)`.

- [ ] **Step 5: Generate the golden on SQLite and review it**

Run: `go test -count=1 -run TestParity -update ./internal/app/web/contract/`
Review `internal/app/web/contract/testdata/parity.golden.json`. At plan time it was exactly:

```json
[
  {
    "name": "create token",
    "status": 200,
    "body": {
      "data": {
        "expires_at": null,
        "name": "parity",
        "prefix": "<token>",
        "token": "<token>",
        "token_id": "<id-1>"
      },
      "message": "token created",
      "success": true
    }
  },
  {
    "name": "register server",
    "status": 200,
    "body": {
      "data": {
        "server": {
          "created_at": "<time>",
          "id": "<id-2>",
          "last_seen_at": null,
          "name": "contract",
          "organization_id": "<id-3>"
        }
      },
      "message": "server added",
      "success": true
    }
  },
  {
    "name": "save alpha (result)",
    "status": 0,
    "body": {
      "app_id": "<id-4>",
      "revision": "rev-1"
    }
  },
  {
    "name": "save beta (result)",
    "status": 0,
    "body": {
      "app_id": "<id-5>",
      "revision": "rev-1"
    }
  },
  {
    "name": "list apps",
    "status": 200,
    "body": {
      "data": {
        "apps": [
          {
            "color": "#112233",
            "created_at": "<time>",
            "icon": "box",
            "id": "<id-4>",
            "name": "alpha",
            "server_id": "<id-2>",
            "template_id": "",
            "version": ""
          },
          {
            "color": "#112233",
            "created_at": "<time>",
            "icon": "box",
            "id": "<id-5>",
            "name": "beta",
            "server_id": "<id-2>",
            "template_id": "",
            "version": ""
          }
        ]
      },
      "success": true
    }
  },
  {
    "name": "rename alpha (result)",
    "status": 0,
    "body": {
      "app_id": "<id-4>",
      "name": "alpha-2"
    }
  },
  {
    "name": "delete beta (result)",
    "status": 0,
    "body": {
      "app_id": "<id-5>"
    }
  },
  {
    "name": "list apps again",
    "status": 200,
    "body": {
      "data": {
        "apps": [
          {
            "color": "#112233",
            "created_at": "<time>",
            "icon": "box",
            "id": "<id-4>",
            "name": "alpha-2",
            "server_id": "<id-2>",
            "template_id": "",
            "version": ""
          }
        ]
      },
      "success": true
    }
  },
  {
    "name": "list members",
    "status": 200,
    "body": {
      "data": [
        {
          "avatar_url": "",
          "created_at": "<time>",
          "email": "owner@example.test",
          "id": "<id-6>",
          "last_seen_at": "<time>",
          "name": "Owner",
          "provider": "local",
          "role": "owner"
        }
      ],
      "message": "members",
      "success": true
    }
  }
]
```

Every value that varies per run must be a placeholder; if a raw UUID, timestamp or `wfp_` token shows up, extend the normalizer instead of committing it.

- [ ] **Step 6: Confirm both databases reproduce it**

Run: `go test -count=3 -run TestParity ./internal/app/web/contract/ && make test-pg TEST_PG_ARGS='-count=5 -run TestParity'`
Expected: PASS every time. A Postgres mismatch prints the differing lines; fix the cause (small) or, per spec, add an explicit allowlisted difference with a reason in the golden and a follow-up in the PR body. None was needed at plan time.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db/repository internal/app/web/contract/parity_test.go internal/app/web/contract/diff_test.go internal/app/web/contract/testdata
git commit -m "test(contract): compare a SQLite/Postgres parity transcript with a golden file

List queries break created_at ties by primary key so both databases
return rows created in the same second in the same order.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Coverage guard — every table route must be called

**Files:**
- Create: `internal/app/web/contract/guard_test.go`
- Modify: `internal/app/web/contract/main_test.go`

**Interfaces:**
- Consumes: `hits`, `table` (Task 3).
- Produces: `func missingRoutes() []string`; `var unexercised map[string]string`; `func coverageReport([]string) string`; `func guardApplies() bool`.

- [ ] **Step 1: Add the guard**

Create `internal/app/web/contract/guard_test.go`:

```go
package contract_test

import (
	"fmt"
	"sort"
	"strings"
)

// unexercised lists table routes the suite cannot reach, with the reason.
// Keep it empty unless a route truly cannot be driven over HTTP.
var unexercised = map[string]string{}

// missingRoutes returns table routes no test reached, as "METHOD /path".
func missingRoutes() []string {
	hits.Lock()
	defer hits.Unlock()
	reached := map[string]bool{}
	for h := range hits.set {
		reached[h.Method+" "+h.Path] = true
	}
	var missing []string
	for _, rt := range table {
		key := rt.Method + " " + rt.Path
		if _, skip := unexercised[key]; skip || reached[key] {
			continue
		}
		missing = append(missing, key)
	}
	sort.Strings(missing)
	return missing
}

// coverageReport formats the guard's failure.
func coverageReport(missing []string) string {
	return fmt.Sprintf("contract coverage guard: %d route(s) in the table were never called:\n  %s\nadd a contract test for each (or list it in unexercised with a reason)",
		len(missing), strings.Join(missing, "\n  "))
}
```

Replace `internal/app/web/contract/main_test.go` with:

```go
package contract_test

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

// TestMain configures the process-wide environment the server reads, runs
// the suite, then enforces the coverage guard: every route in the table
// must have been called at least once.
func TestMain(m *testing.M) {
	flag.Parse()
	tmp, err := os.MkdirTemp("", "wf-contract-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for k, v := range map[string]string{
		"JWT_SECRET":           "contract-secret",
		"WEB_URL":              "http://127.0.0.1",
		"REGION":               "contract",
		"AVATARS_STORAGE_PATH": tmp,
		"AGENT_DATA_DIR":       tmp,
		"INGRESS_ENABLED":      "false",
	} {
		os.Setenv(k, v)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	if code == 0 && guardApplies() {
		if missing := missingRoutes(); len(missing) > 0 {
			fmt.Fprintln(os.Stderr, coverageReport(missing))
			code = 1
		}
	}
	os.Exit(code)
}

// guardApplies is false for partial runs (-run, -skip) and -short, where
// most routes are deliberately not exercised.
func guardApplies() bool {
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return false
		}
	}
	return !testing.Short()
}
```

- [ ] **Step 2: Prove it fails on a missing route**

In `selftest_test.go`, temporarily replace `e.anon().get(t, "/_/health", nil).expect(t, http.StatusOK)` with `_ = e`.
Run: `go test -count=1 ./internal/app/web/contract/`
Expected: FAIL with

```
contract coverage guard: 1 route(s) in the table were never called:
  GET /_/health
add a contract test for each (or list it in unexercised with a reason)
```

Restore the line.

- [ ] **Step 3: Run the full suite on both databases**

Run: `go test -race -count=1 ./internal/app/web/contract/ && make test-pg`
Expected: PASS on both (the guard runs because neither passes `-run`). `go test -run TestParity ./internal/app/web/contract/` also passes: the guard skips partial runs.

- [ ] **Step 4: Commit**

```bash
git add internal/app/web/contract/guard_test.go internal/app/web/contract/main_test.go
git commit -m "test(contract): fail the suite when a table route is never called

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: CI matrix, merged coverage, docs, baseline

**Files:**
- Modify: `.github/workflows/ci.yml`, `Makefile` (`test`), `AGENTS.md`, `CLAUDE.md`, `coverage-baseline.json`

**Interfaces:**
- Consumes: the whole suite; `WF_TEST_DATABASE_URL` (Task 3).
- Produces: CI job `contract` (matrix `db: [sqlite, postgres]`) and artifact `contract-postgres-coverage` (`contract-postgres.out`), consumed by the `coverage` job.

- [ ] **Step 1: Count the contract suite in Go coverage**

In `.github/workflows/ci.yml`, job `go`, replace the `go test` step:

```yaml
      # -coverpkg=./... so the API contract suite (a test-only package) counts
      # toward the handlers, usecases and repositories it drives.
      - name: go test
        run: go test -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
```

In `Makefile`, replace the `test` target and its comment:

```make
# Go tests exactly as CI runs them; coverage.out feeds `make cover` and
# `make diffcov`. -coverpkg=./... lets the API contract suite
# (internal/app/web/contract, test-only) count toward the code it drives.
test:
	$(GO) test -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
```

- [ ] **Step 2: Add the `contract` matrix job**

In `.github/workflows/ci.yml`, insert before `  coverage:` and change the `coverage` job's `needs`:

```yaml
  # The API contract suite against both databases. Each leg must match
  # testdata/parity.golden.json. The postgres leg's profile is merged into
  # the coverage gates: Postgres-only branches run nowhere else.
  contract:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        db: [sqlite, postgres]
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_PASSWORD: postgres
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U postgres -h 127.0.0.1"
          --health-interval 2s
          --health-timeout 5s
          --health-retries 30
    env:
      # Empty selects SQLite (one temp file per test).
      WF_TEST_DATABASE_URL: ${{ matrix.db == 'postgres' && 'postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' || '' }}
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Contract suite (${{ matrix.db }})
        run: >-
          go test -race -count=1 -covermode=atomic -coverpkg=./...
          -coverprofile=contract-${{ matrix.db }}.out ./internal/app/web/contract/

      - uses: actions/upload-artifact@v4
        if: matrix.db == 'postgres'
        with:
          name: contract-postgres-coverage
          path: contract-postgres.out
          if-no-files-found: error

  coverage:
    needs: [go, web, contract]
```

(Both legs start the service; the SQLite leg ignores it. That is simpler and more robust than a conditional service.)

- [ ] **Step 3: Merge the Postgres profile before the gates**

In the `coverage` job, after the `web-coverage` download step:

```yaml
      - uses: actions/download-artifact@v4
        with:
          name: contract-postgres-coverage

      # Duplicate blocks are fine: diffcov keeps the highest count per block.
      - name: Merge Postgres contract coverage
        run: tail -n +2 contract-postgres.out >> coverage.out
```

(`tail -n +2` drops the second `mode:` line so `go tool cover -func` still reads the file.)

Run: `python3 -c "import yaml; d=yaml.safe_load(open('.github/workflows/ci.yml')); print(list(d['jobs']), d['jobs']['coverage']['needs'])"`
Expected: `['go', 'web', 'installer', 'contract', 'coverage'] ['go', 'web', 'contract']`.

- [ ] **Step 4: Reproduce the gates locally with the merged profile**

```bash
make test
cid=$(docker run -d --rm -p 127.0.0.1:0:5432 -e POSTGRES_PASSWORD=postgres postgres:16 -c max_connections=200)
port=$(docker port "$cid" 5432/tcp | head -n1 | sed 's/.*://')
until docker exec "$cid" pg_isready -U postgres -h 127.0.0.1 >/dev/null 2>&1; do sleep 0.5; done
WF_TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:$port/postgres?sslmode=disable" \
  go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out ./internal/app/web/contract/
docker rm -f "$cid"
tail -n +2 contract-postgres.out >> coverage.out
make web-test
go run ./tools/diffcov --base ci/openapi-from-code --min 80 --go coverage.out --lcov web/coverage/lcov.info
go run ./tools/diffcov ratchet --baseline coverage-baseline.json --go coverage.out --lcov web/coverage/lcov.info
rm contract-postgres.out
```

Expected: the gate passes (plan time: `go 29/31 changed lines covered (93.5%)`; the two uncovered lines are the renamed `WireCore` calls inside `BootstrapAPI`/`BootstrapStandalone`, which no test runs). The ratchet passes and prints `::notice::raise coverage-baseline.json go to <value>` (plan time: 74.3).

- [ ] **Step 5: Raise the baseline**

Set `"go"` in `coverage-baseline.json` to the value the ratchet printed (leave `"web"` unchanged). After the PR's first CI run, if the CI notice prints a different value (runner differences), use CI's.

- [ ] **Step 6: Document the suite**

`AGENTS.md`, in the command list, after the `make openapi` bullet:

```markdown
- `make test-pg` – the API contract suite (`internal/app/web/contract`) against a throwaway `postgres:16` container (Docker); plain `go test ./...` runs it on SQLite. Extra flags: `make test-pg TEST_PG_ARGS='-run TestParity -v'`.
```

`AGENTS.md`, Testing Guidelines, after the frontend paragraph:

```markdown
The API contract suite (`internal/app/web/contract`, test files only) drives every route in the route table through the real router, middleware, database and in-process bridge, with a scripted fake agent (`agent.on`/`agent.fail`) instead of Docker. Every request and response is validated against `api/openapi.yaml`; agent-bound routes are followed over SSE to their `operation_result` (open the stream first: `client.subscribe`, done by `claim`/`pat`/`member`). A new route needs a contract test — `TestMain` fails listing any table route that was never called — and a `probes` entry if it takes input. It runs on SQLite by default and on Postgres when `WF_TEST_DATABASE_URL=postgres://…` is set (a schema per test). `TestParity` compares a normalized transcript with `testdata/parity.golden.json` on both databases; regenerate it with `go test ./internal/app/web/contract -run TestParity -update` and review the diff.
```

`AGENTS.md`, the CI paragraph: replace the text

```markdown
enforces gofmt, `go vet`, `go test -race`,
```

with

```markdown
enforces gofmt, `go vet`, `go test -race -coverpkg=./...`, the contract suite on SQLite and Postgres (`contract` matrix job, whose Postgres coverage is merged into the gates),
```

`CLAUDE.md`, Commands, after the `go test ./...` bullet:

```markdown
- `make test-pg` — the API contract suite (`internal/app/web/contract`) on a throwaway `postgres:16` container; `go test ./...` runs it on SQLite. Every table route must be called by it (the `TestMain` guard fails otherwise); `TestParity`'s golden (`-update`) must match on both databases. Locally `make diffcov` sees SQLite coverage only; CI merges the Postgres contract profile, which is the only coverage of Postgres-only branches.
```

`CLAUDE.md`, at the end of the "**To add a feature:**" sentence (Architecture section), append:

```markdown
Add a contract test in `internal/app/web/contract` (happy path, plus a `probes` entry when the route takes input).
```

- [ ] **Step 7: Full local run**

Run: `gofmt -l $(git ls-files '*.go'); go vet ./... && make test && make test-pg && go mod tidy && git diff --exit-code go.mod go.sum`
Expected: no gofmt output; everything passes; `go.mod`/`go.sum` unchanged.

- [ ] **Step 8: Commit**

```bash
git add .github/workflows/ci.yml Makefile AGENTS.md CLAUDE.md coverage-baseline.json
git commit -m "ci: run the contract suite on SQLite and Postgres and merge its coverage

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

The PR body lists the Postgres findings above, states that the parity allowlist is empty, and notes the coverage baseline jump (contract suite now counts via `-coverpkg`).
