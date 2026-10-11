# CI, coverage gates and API contract tests — design

Date: 2026-10-10. Status: approved in conversation.

## Context

Winterflow has no CI. The only workflow (`release.yml`) runs `go test ./...` on
push to main and then releases. Go coverage is 59.4% total. `web/` has no test
runner. Postgres is supported (`internal/infra/db/bun.go`) but never tested.
The HTTP API (~44 routes in `internal/app/web/routes.go`) has handler unit
tests with fakes, and nothing tests the assembled API end to end.

The twillingate cloud portal is about to become a real API client, and
sub-projects B (domains) and D (templates) add API surface. Before them, the
repo gets three stacked PRs:

| PR | Scope |
|---|---|
| 1 | CI workflow, diff-coverage gate (80% on new code), coverage ratchet, web test runner |
| 2 | Typed route table and OpenAPI 3.1 generated from code (no behaviour change) |
| 3 | API contract suite against the generated spec, run on SQLite and Postgres |

After them: PR 4 removes roles (every user is admin), PR 5 is domains (B). Both
must pass the new gates.

## Requirements (from the user)

- Tests run in CI on every PR.
- New code has at least **80%** unit-test coverage; eventually all code does.
- Every API contract is covered by a test.
- The same API tests run on both SQLite and Postgres and are compared.
- The web UI is held to the same 80% rule.
- The OpenAPI spec is generated from code.
- Agent-bound routes are tested through to the SSE `operation_result`.

## PR 1 — CI and coverage gates

### Workflow `.github/workflows/ci.yml`

Triggers: `pull_request` (any base) and `push` to `main`. Concurrency group
per ref, cancel in progress for PRs. Three jobs in parallel:

- **go**: `actions/setup-go` from `go.mod`.
  - `gofmt -l` over the repo must print nothing (fix the existing offender
    `internal/app/web/handler/user/handler.go`).
  - `go vet ./...`.
  - `go test -race -covermode=atomic -coverprofile=coverage.out ./...`.
  - Upload `coverage.out` as an artifact.
- **web**: pnpm 10, node 22.
  - `pnpm --dir web install --frozen-lockfile`.
  - `pnpm --dir web run lint`.
  - `pnpm --dir web run test:coverage` (writes `web/coverage/lcov.info`).
  - `pnpm --dir web run build`.
  - Upload lcov as an artifact.
- **installer**: `bash scripts/install_test.sh`.
- **coverage** (needs go and web): downloads both artifacts, runs the
  diff-coverage gate and the ratchet. On `push` to main only the ratchet runs.

### Diff-coverage gate: `tools/diffcov`

A small Go command (stdlib only), run as `go run ./tools/diffcov`.

- Inputs: `--base <ref>` (PR base SHA), `--go coverage.out`, `--lcov
  web/coverage/lcov.info`, `--min 80`.
- It runs `git diff --unified=0 <base>...HEAD` and collects added line numbers
  per file.
- **Go:** a changed line counts if it falls inside a coverprofile block. It is
  covered if any block containing it has count > 0. Lines outside every block
  (comments, declarations, blank lines) are not executable and don't count.
- **Web:** a changed line counts if lcov has a `DA:<line>,<hits>` record for
  it. It is covered when hits > 0.
- Excluded paths: `*_test.go`, `*.test.ts(x)`, `*.pb.go`,
  `web/src/components/ui/**`, `cmd/**/main.go`, `tools/**`, `web/src/main.tsx`.
- Output: per-language covered/total and percent, plus a list of uncovered
  changed lines (`file:line`). It exits 1 if either language with at least one
  executable changed line is below `--min`. A language with zero executable
  changed lines passes.
- The tool has unit tests: diff parsing, coverprofile parsing, lcov parsing,
  exclusions, and threshold logic.

### Ratchet `coverage-baseline.json`

```json
{ "go": 59.4, "web": 0.0 }
```

- The `coverage` job computes total Go coverage (`go tool cover -func` total)
  and total web line coverage (from lcov).
- It fails if either total is more than 0.1 points below its baseline.
- When a total exceeds its baseline by at least 1.0, it prints
  `::notice::raise coverage-baseline.json <lang> to <value>`. The PR author
  bumps the file; nothing is auto-committed.
- The baseline values in PR 1 are measured at the time of the PR, not copied
  from this spec.

### Web test runner

- Dev deps: `vitest`, `@vitest/coverage-v8`, `@testing-library/react`,
  `@testing-library/user-event`, `@testing-library/jest-dom`, `jsdom` (all
  MIT).
- Scripts: `test` (`vitest run`) and `test:coverage` (`vitest run
  --coverage`).
- Config: in `vite.config.ts` or `vitest.config.ts`, environment jsdom,
  coverage provider v8, reporters `text` + `lcov`. The coverage `include` is
  `src/**`, and the `exclude` is `src/components/ui/**`, `src/main.tsx` and
  `**/*.d.ts`.
- First tests prove the setup on real code: the pure helpers in
  `web/src/pages/settings-volumes.tsx` (short volume name, grouping), exported
  if needed, plus one component render test of `VolumesCard` with a mocked
  `useDocker`.
- `web/dist/.gitkeep` stays committed.

### Makefile

Add the targets `test` (go test with coverage), `web-test`, `cover` (prints
both totals) and `diffcov` (runs the gate against `origin/main`).

### Release gating

`release.yml` changes its trigger to `workflow_run` on workflow `ci`, types
`[completed]`, branch `main`, and runs only when
`github.event.workflow_run.conclusion == 'success'`. Checkout uses
`github.event.workflow_run.head_sha`. Its own `go test` step is removed,
because CI ran it. `workflow_dispatch` stays. The PR body flags this trigger
change for the user's explicit confirmation before merge.

## PR 2 — OpenAPI 3.1 generated from code

### Route table

A new package `internal/app/web/api`:

```go
type Auth int
const (
    Public Auth = iota // no auth middleware
    User               // authMW (PAT or JWT)
)

type Route struct {
    Method     string           // http.MethodGet / MethodPost
    Path       string           // "/api/v1/app/save-app"
    Handler    http.HandlerFunc
    Auth       Auth
    Middleware []func(http.Handler) http.Handler // extra, e.g. rate limit, admin
    Summary    string
    Tag        string           // "apps", "docker", "server", "user", "org", "auth", "system"
    Query      any              // struct with `query:"..."` tags, or nil
    Request    any              // JSON body type, or nil
    Response   any              // `data` payload type for 2xx, or nil
    Accepted   bool             // 202 + {request_id}
    Result     any              // command response type delivered over SSE (Accepted routes)
    Errors     []int            // documented error statuses, e.g. 400, 401, 403, 404, 409
}
```

- Each handler package exports `func (h *Handler) Routes() []api.Route`.
- Request and response structs used in the table are exported (renamed
  minimally).
- `routes.go` keeps building the middleware chains (authMW, adminMW, rate
  limiter, CORS). It collects all tables and registers each route with chi as
  `Router.With(chain...).Method(r.Method, r.Path, r.Handler)`. Registered
  paths and middleware must be exactly what they are today: a test compares
  the chi route list before and after (`chi.Walk`) against a golden list.
- Non-JSON or library routes (`/_/health`, SSE `GET /api/v1/sse`, go-pkgz
  `/auth/*`, avatar, SPA fallback) are described in the table with
  `Handler: nil` and a `Mounted: true` marker where the library mounts them.
  They are documented but not re-registered.

### Generator

- `github.com/swaggest/openapi-go` (MIT, `openapi31` reflector) builds the
  document from the table.
- Every JSON response is wrapped in the envelope `webutil` writes. Read
  `internal/app/web/util/response.go` for the exact shape (e.g. `{"message":
  string, "data": T}`); the schema must match the bytes on the wire.
- Error responses share one `Error` schema, matching `webutil.Error`.
- Accepted routes respond `202` with `data: {request_id: string}` and carry
  the extension `x-operation-result: {$ref: '#/components/schemas/<Result>'}`.
- The SSE route documents `text/event-stream` with a `Notification` component
  (`internal/domain/model/notification.go`).
- Security schemes: `bearer` (PAT `wfp_…` or JWT) and `cookie` (JWT cookie);
  User routes require either.
- `go run ./cmd/openapi > api/openapi.yaml`, and `make openapi` does the same.
- `TestOpenAPIUpToDate` regenerates the document in memory and diffs it with
  the committed `api/openapi.yaml`. On mismatch it fails with "run make
  openapi".
- The document validates with `kin-openapi` `openapi3` loader `Validate`
  (test-only dependency, MIT).

## PR 3 — API contract suite on SQLite and Postgres

### Harness `internal/app/web/contract` (package `contract_test` + helpers)

- **DB:** `WF_TEST_DATABASE_URL`; when unset, a temp SQLite file per test. A
  `postgres://` URL makes each test create a fresh schema (`CREATE SCHEMA
  t_<random>`, `search_path` set on the connection) and drop it on cleanup.
  Migrations run through the normal startup path.
- **Stack:** built with the same constructors `BootstrapStandalone` uses, but
  with an in-memory bus, the real `InProcessBridge`, and a **fake agent**
  instead of the docker-compose dispatcher. The fake agent implements the
  transport dispatcher interface. It returns scripted
  `proto.ResponseEnvelope`s per command type through the real codec, with
  success by default and per-test overrides (error, warnings, custom payload).
  Ingress is off.
- **Server:** `httptest.NewServer(web.NewServer(deps).Router)`.
- **Clients:**
  - `claim()` runs the first-run register/claim flow and returns an
    authenticated client (JWT cookie).
  - `pat()` creates a PAT and returns a bearer client.
  - `anon()` is unauthenticated.
- **SSE:** `awaitResult(t, client, requestID)` subscribes to `/api/v1/sse` and
  returns the `operation_result` for that ref, with a 5 s timeout.
- **Validation:** every request the helpers send is validated against
  `api/openapi.yaml` (`openapi3filter.ValidateRequest`), and every response
  too (`ValidateResponse`). For Accepted routes, the SSE result payload is
  validated against the route's `x-operation-result` schema.

### Coverage guard

- A recorder middleware, wrapped around the router only in the harness,
  records `(method, route pattern, status)`.
- `TestMain` runs the suite, then compares against the route table. Every
  route (excluding `Mounted` library routes that can't be exercised, and the
  SPA) must have at least one recorded call. Otherwise it fails, listing the
  missing routes.

### What each route's tests assert

- **Happy path:** status and schema, plus database side effects through a
  follow-up GET where one exists (e.g. after `save-app`, `get-apps` lists it;
  after `delete-app`, it doesn't).
- **Auth:** `anon()` gets 401 on User routes.
- **Server ownership:** a second org's server id gets 403 on server-addressed
  routes.
- **Validation:** one malformed body or missing parameter gets 400.
- **Agent-bound routes:** a 202, then `awaitResult` gives a success payload
  matching the schema. One error-scripted case per command family gives
  `status != 0` with an error message.

### SQLite vs Postgres parity

- In CI, the contract job runs as a matrix `db: [sqlite, postgres]`, with a
  `postgres:16` service container (`POSTGRES_PASSWORD=postgres`, health
  check).
- `TestParity` runs a fixed scenario: claim, create a PAT, register a server,
  save two apps, list them, rename one, delete one, and list org members. It
  records every response body, normalised: UUIDs, timestamps, tokens and
  request ids are replaced with stable placeholders, and key order is fixed.
  The normalised transcript is compared with the golden file
  `internal/app/web/contract/testdata/parity.golden.json`, which both
  databases must match. `-update` rewrites it.
- `make test-pg` starts `postgres:16` in Docker on a random port and runs the
  contract suite with `WF_TEST_DATABASE_URL` set. The plain `go test ./...`
  stays on SQLite.

### Coverage interplay

The contract suite runs in the normal `go test ./...` (SQLite), so it counts
toward the Go coverage totals and the diff gate.

## Out of scope

- Fixing Postgres bugs the parity test uncovers beyond what's needed for green
  CI. Each one is fixed in PR 3 if it is small. Otherwise it is listed in the
  PR as a follow-up and the parity golden marks it as a known difference
  (explicit allowlist with a reason).
- Raising existing coverage to 80%. The ratchet handles that over time.
- E2E browser tests.
