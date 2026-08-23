# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

`AGENTS.md` is the canonical contributor guide (structure, coding style, testing, commit conventions, security). Read it too — this file covers the architecture and commands that need cross-file context to grasp.

## Commands

Backend (Go 1.25):
- `make build` — bundles the SPA (`make web-build` → `web/dist`, embedded via `go:embed`) then compiles `standalone` + `api` into `bin/`. Note: `hub` and `agent` are NOT in this target; build them with `go build ./cmd/hub ./cmd/agent`. A bare `go build` embeds whatever is in `web/dist` — the committed placeholder on a fresh checkout, i.e. an API-only binary.
- `make standalone | api | hub | agent` — `go run` the chosen binary.
- `make lint` — `go vet ./...` plus the web ESLint suite.
- `go test ./...` — all tests. Single package: `go test ./internal/infra/transport/codec/`. Single test: `go test ./internal/infra/orchestrator/docker_compose/ -run TestAggregateStatus`.
- `make generate-hub-certs` / `make generate-agent-certs` — bootstrap local mTLS material (agent certs depend on hub certs existing first).
- `make grpc` — regenerate Go from `internal/infra/transport/grpc/proto/hub.proto` (installs `protoc-gen-go`/`protoc-gen-go-grpc`). `make sqlc` regenerates SQL (note: no `sqlc.yaml` checked in yet).

Frontend (`web/`, pnpm + Vite + React 19):
- `make web` (= `pnpm --dir web dev`), `pnpm --dir web run build` (runs `tsc -b` first), `pnpm --dir web run lint`.

**The web UI ships inside the Go binary** (econumo-style): `web/embed.go` does `go:embed all:dist` and `internal/app/web/spa` serves it as the router's NotFound handler with SPA history-mode fallback (reserved prefixes `/api`, `/auth`, `/avatar`, `/_` still 404; missing extensioned paths 404 instead of returning the shell). Production bundles are same-origin: `web/.env.production` pins empty `VITE_*_BASE_URL`s (→ `window.location.origin` in `web/src/config.ts`) and `VITE_APP_MODE=standalone`; the api Dockerfile builds with `VITE_APP_MODE=distributed`. Keep `web/dist/.gitkeep` committed — the placeholder is what makes a frontend-free checkout compile (and `DistFS()` report "no build").

Config comes from `.env` (loaded via `godotenv`) or the process environment. Copy `.env.example` → `.env` (frontend build-time vars: `web/.env.example`). `.env`, `data/`, `bin/`, and `*.sqlite*` are gitignored.

**`.env.example` is the canonical, commented inventory of every env var the backend reads.** Whenever you add, rename, remove, or change the default of an env var in Go (any `os.Getenv`/`os.LookupEnv`, normally via a `pkg/config` getter), update `.env.example` in the same change — variable, default, and a comment saying what it does and which topology reads it. The installer's generated config (`scripts/install.sh`) points users at it, so a stale example is user-facing. Same rule for frontend `VITE_*` vars and `web/.env.example`.

## Architecture

This is the **v2 monorepo** that merges the v1 `winterflow-agent` and `winterflow-app` into one codebase producing four binaries. The same business logic serves two topologies; the only thing that differs is how commands are transported.

**Topologies (this is the central design):**
- **Standalone** (`cmd/standalone`, `config.NewServerConfig("standalone")`): one process running the HTTP API, the agent, and the Docker Compose orchestrator over SQLite. The "bus" and "hub" run in-process — there is no Redis or gRPC hop.
- **Distributed** (`config.NewServerConfig("distributed")`): horizontally-scalable **API** (`cmd/api`, the "brain", owns persistence) ⇄ **Redis Bus** ⇄ horizontally-scalable **Hub** (`cmd/hub`, gRPC server with mTLS) ⇄ **Agent** (`cmd/agent`). The API publishes a command to the request queue; the Hub forwards it down the addressed agent's gRPC stream; the agent's reply travels back up onto the response queue, where the API's `reply.Manager` wakes the blocked caller.

**The command round-trip** (trace this to understand the whole system):
1. HTTP handler (`internal/app/web/handler/`) → usecase (`internal/domain/usecase/`) → `port.AppService.SaveApp(...)` with a result callback.
2. The service implementation (`internal/infra/transport/redis/service/app`, type `BusAppService` — bus-agnostic despite the package path) publishes a `bus.CommandMessage` keyed by a `request_id` and registers a reply channel.
3. The Hub (`internal/infra/transport/grpc/hub`) consumes the request queue via `StartBusBridge`, looks up the agent stream, and sends a `proto.RequestEnvelope`. In standalone, `internal/app/agent/inprocess.go` (`InProcessBridge`) does the same in-memory.
4. The agent's `Dispatcher` (`internal/app/agent`) decodes the envelope, runs the Docker Compose orchestrator, and returns a `proto.ResponseEnvelope`.
5. The Hub/bridge publishes the result as a `model.Notification{Ref: request_id, ...}` onto the response queue; the API's bus subscriber routes it to `reply.Manager.Publish(ref, ...)`, unblocking step 2's callback, which then publishes to the shared `NotificationManager` (SSE → browser).

**Single gRPC envelope + typed commands** (the deliberate simplification over v1's ~14 oneof RPC pairs): all hub↔agent traffic rides one envelope (`proto.RequestEnvelope`/`ResponseEnvelope` = a `type` string + JSON `payload`). The catalog of command types and payload structs lives in `internal/domain/command`; `internal/infra/transport/codec` is the ONLY place that (de)serializes payloads. **To add a feature:** define a command type + payload struct in `internal/domain/command`, add one registration line in the agent Dispatcher's `newHandlers` map (`internal/app/agent/dispatcher.go`) plus the orchestrator method, and add a usecase/handler + route on the API side.

**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete`, `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`; agent — `agent.update` (self-update). All are agent-bound (202 + `request_id`, result over SSE).

**App secrets are ECIES-encrypted end-to-end** (`pkg/crypto`, P-256 ECDH→SHA-256→AES-256-GCM): the browser encrypts a secret with the agent's public key (served by `GET /api/v1/server/get-public-key`, sourced from the `public_key` capability or the local agent cert in standalone); the agent decrypts with its mTLS private key before render. Editing sends the `<encrypted>` placeholder for unchanged secrets (the agent preserves the stored value, never returns plaintext). Registry passwords use the same scheme.

**Agent resilience:** the agent runs a supervising `Run(ctx)` loop (`internal/infra/transport/grpc/agent`) that reconnects with exponential backoff (1s–30s) on stream loss; the Hub deletes agent registrations on stream close and reaps agents idle past a TTL (~3 missed heartbeats).

**No DI/factory** — there is intentionally no factory abstraction (an earlier `AppFactory` was removed). Each binary wires its concrete dependencies explicitly in `internal/infra/bootstrap` (`BootstrapStandalone`/`BootstrapAPI` return `*Deps`; `BootstrapHUB` returns `*HubDeps`) and hands the struct to `web.NewServer`. The single shared `NotificationManager` must come from `Deps` — constructing a second one breaks SSE delivery.

**Bus abstraction:** `internal/infra/transport/bus.Bus` is the pub/sub contract. Two impls are interchangeable: `redis/bus` (distributed) and `mem/bus` (standalone in-process). Queue names are region-scoped (`requests:<REGION>` / `responses:<REGION>`); `REGION` must match across API and Hub.

**Layering** is hexagonal: `internal/domain/{model,port,usecase,service}` (transport-agnostic business logic) over `internal/infra/{db,transport,orchestrator,cert}` (adapters), with `internal/app/{web,agent}` as the delivery layer. Persistence uses Bun ORM (`internal/infra/db`, SQLite or Postgres via `DATABASE_URL`); migrations live in `internal/infra/db/migrations` and run on startup.

**Orchestration:** the agent deploys apps via the `docker compose` CLI (not the Docker SDK), in `internal/infra/orchestrator/docker_compose`. Each app lives in `{AGENT_DATA_DIR}/apps-data/{appID}/` — a git repository (go-git) that IS the deployment: compose runs in it directly, interpolating `${VAR}` from the committed `.env` (+ gitignored `.env.secrets`, materialized from the ECIES-encrypted committed `secrets.json` at deploy). `{AGENT_DATA_DIR}/apps/` holds human-readable `{slug}` symlinks. Every save/rename is a commit; rollback restores an old tree as a new commit and redeploys. History is unlimited. Git-sourced apps additionally clone their upstream into a gitignored `source/` (SHA pinned in the committed `source.lock`, so rollback restores the source position too), run compose against the repo's compose file, and can auto-update via an agent-side poller.

**Auth:** `go-pkgz/auth`. Local email+password is the always-on default (`internal/app/web/auth/local.go`, bcrypt in `user_credentials`); login is verify-only — accounts come from `POST /api/v1/auth/register` (fresh instance = one-time claim step creating the admin + the org; distributed self-signup gated by `REGISTRATION_ENABLED`, own org per registrant) or from admins at `/org/members`. Orgs carry name/icon/color (`org/get-organization`, admin `org/update-organization`). Google OAuth is optional (client id/secret pair, distributed only). JWT-protected `/api/v1/*` routes; personal access tokens (`wfp_…`, SHA-256-hashed at rest, `pkg/pat`) accepted as `Bearer` (via `internal/app/web/middleware/patauth`) or Basic-auth password, managed at `/user/tokens`. Roles: owner/admin administer (members via `/org/members`, server registration, registry/network mutations — gated by `internal/app/web/middleware/rbac`); members keep the full app lifecycle. mTLS for hub↔agent.

## Security, stability, and resource invariants

Winterflow ships to small always-on home servers — think mini-PCs and Raspberry Pi-class boards: few cores, possibly ARM, often fanless, thermally constrained. Don't assume any particular CPU, core count, or architecture; assume the weakest plausible box. Idle CPU burn, unbounded growth, and missing authorization are bugs, not polish. A 2026-08 full audit established these rules — keep them when adding code:

**Security (fail closed):**
- A `server_id` from a request is NOT authorization. Every server-addressed route must verify org ownership before dispatching: `webutil.RequireServerAccess` (backed by `ServerRepository.UserOwnsServer`); the app handlers' `authorize` and the docker handlers' `caller` helpers do this — reuse them. The guard denies when unwired (nil), and tests assert 403 per route.
- bcrypt-priced endpoints (login/register) sit behind the per-IP limiter in `internal/app/web/middleware/ratelimit`; put any new expensive unauthenticated endpoint behind it too.
- Secret values never leave the agent (mask with the `<encrypted>` placeholder; secret files 0600). Docker/git CLI args are always `[]string` — never build shell strings; user-supplied paths go through `safeRel`, app ids through `filepath.Base`.

**Stability (months of uptime between restarts):**
- Every network operation needs a deadline. The standalone command pipeline is drained by ONE goroutine (`InProcessBridge`), so a single unbounded hang wedges all app management until restart — see `ensureSource`'s 5-minute timeout for the pattern.
- Channel hygiene: close and send must be serialized by the same lock (see `mem/bus`); pump goroutines select on `ctx.Done()` for every blocking send; concurrent writes to one gRPC stream are forbidden — use the per-agent send mutex (`hub.agent.send`).
- Per-app in-memory bookkeeping must be pruned on app deletion (`Repository.forgetApp`).

**Resources (idle cost near zero):**
- Never exec per app in polled paths — process forks (docker/compose/git) are the expensive unit on target hardware. Status collection is ONE `docker ps --all` grouped by the compose project label (`dockerPSByProject`); keep that shape.
- App git histories are unlimited and append-only: anything derived from a history walk must be cached keyed by HEAD (see `commitCount`); source polling skips fetch/checkout work when the upstream hasn't moved and fetches only the configured branch.
- Fan out SSE/notifications only on change (`status.Cache.SetAppStatus` returns `changed`), never unconditionally per tick.
- The embedded ingress carries a predefined per-client-IP throttle (in-repo Caddy module `winterflow_rate_limit`, `INGRESS_RATE_LIMIT_RPS`/`_BURST`, default 50/100, 0 disables) plus header/idle timeouts — new ingress routes must stay behind it (it's prepended to both servers in `BuildConfig`).
- `scripts/install.sh` owns operational limits: journald namespace `winterflow` with rotation caps, Docker container-log rotation, and unit `CPUQuota`/`MemoryMax`. After editing it, run `bash scripts/install_test.sh` (uses `--dry-run`, no root needed).

**Frontend:** `web/src/components/ui/` is intentionally vendored shadcn/ui — never delete its components (or their package.json deps) as "dead code", even when unimported.
