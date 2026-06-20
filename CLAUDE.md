# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

`AGENTS.md` is the canonical contributor guide (structure, coding style, testing, commit conventions, security). Read it too — this file covers the architecture and commands that need cross-file context to grasp.

## Commands

Backend (Go 1.25):
- `make build` — compiles `standalone` + `api` into `bin/`. Note: `hub` and `agent` are NOT in this target; build them with `go build ./cmd/hub ./cmd/agent`.
- `make standalone | api | hub | agent` — `go run` the chosen binary.
- `make lint` — `go vet ./...` plus the web ESLint suite.
- `go test ./...` — all tests. Single package: `go test ./internal/infra/transport/codec/`. Single test: `go test ./internal/infra/orchestrator/docker_compose/ -run TestAggregateStatus`.
- `make generate-hub-certs` / `make generate-agent-certs` — bootstrap local mTLS material (agent certs depend on hub certs existing first).
- `make grpc` — regenerate Go from `internal/infra/transport/grpc/proto/hub.proto` (installs `protoc-gen-go`/`protoc-gen-go-grpc`). `make sqlc` regenerates SQL (note: no `sqlc.yaml` checked in yet).

Frontend (`web/`, pnpm + Vite + React 19):
- `make web` (= `pnpm --dir web dev`), `pnpm --dir web run build` (runs `tsc -b` first), `pnpm --dir web run lint`.

Config comes from `.env` (loaded via `godotenv`) or the process environment. Copy `.env.dist` → `.env`. `.env`, `data/`, `bin/`, and `*.sqlite*` are gitignored.

## Architecture

This is the **v2 monorepo** that merges the v1 `winterflow-agent` and `winterflow-app` into one codebase producing four binaries. The same business logic serves two topologies; the only thing that differs is how commands are transported.

**Topologies (this is the central design):**
- **Standalone** (`cmd/standalone`, `config.NewServerConfig("standalone")`): one process running the HTTP API, the agent, and the Docker Compose orchestrator over SQLite. The "bus" and "hub" run in-process — there is no Redis or gRPC hop.
- **Distributed** (`config.NewServerConfig("distributed")`): horizontally-scalable **API** (`cmd/api`, the "brain", owns persistence) ⇄ **Redis Bus** ⇄ horizontally-scalable **Hub** (`cmd/hub`, gRPC server with mTLS) ⇄ **Agent** (`cmd/agent`). The API publishes a command to the request queue; the Hub forwards it down the addressed agent's gRPC stream; the agent's reply travels back up onto the response queue, where the API's `reply.Manager` wakes the blocked caller.

**The command round-trip** (trace this to understand the whole system):
1. HTTP handler (`internal/app/web/handler/`) → usecase (`internal/domain/usecase/`) → `port.AppService.CreateApp(...)` with a result callback.
2. The service implementation (`internal/infra/transport/redis/service/app`, type `BusAppService` — bus-agnostic despite the package path) publishes a `bus.CommandMessage` keyed by a `request_id` and registers a reply channel.
3. The Hub (`internal/infra/transport/grpc/hub`) consumes the request queue via `StartBusBridge`, looks up the agent stream, and sends a `proto.RequestEnvelope`. In standalone, `internal/app/agent/inprocess.go` (`InProcessBridge`) does the same in-memory.
4. The agent's `Dispatcher` (`internal/app/agent`) decodes the envelope, runs the Docker Compose orchestrator, and returns a `proto.ResponseEnvelope`.
5. The Hub/bridge publishes the result as a `model.Notification{Ref: request_id, ...}` onto the response queue; the API's bus subscriber routes it to `reply.Manager.Publish(ref, ...)`, unblocking step 2's callback, which then publishes to the shared `NotificationManager` (SSE → browser).

**Single gRPC envelope + typed commands** (the deliberate simplification over v1's ~14 oneof RPC pairs): all hub↔agent traffic rides one envelope (`proto.RequestEnvelope`/`ResponseEnvelope` = a `type` string + JSON `payload`). The catalog of command types and payload structs lives in `internal/domain/command`; `internal/infra/transport/codec` is the ONLY place that (de)serializes payloads. **To add a feature:** define a command type + payload struct in `internal/domain/command`, register it in `codec.NewRequestPayload`, add a handler in the agent `Dispatcher`, and add a usecase + route on the API side.

**No DI/factory** — there is intentionally no factory abstraction (an earlier `AppFactory` was removed). Each binary wires its concrete dependencies explicitly in `internal/infra/bootstrap` (`BootstrapStandalone`/`BootstrapAPI` return `*Deps`; `BootstrapHUB` returns `*HubDeps`) and hands the struct to `web.NewServer`. The single shared `NotificationManager` must come from `Deps` — constructing a second one breaks SSE delivery.

**Bus abstraction:** `internal/infra/transport/bus.Bus` is the pub/sub contract. Two impls are interchangeable: `redis/bus` (distributed) and `mem/bus` (standalone in-process). Queue names are region-scoped (`requests:<REGION>` / `responses:<REGION>`); `REGION` must match across API and Hub.

**Layering** is hexagonal: `internal/domain/{model,port,usecase,service}` (transport-agnostic business logic) over `internal/infra/{db,transport,orchestrator,cert}` (adapters), with `internal/app/{web,agent}` as the delivery layer. Persistence uses Bun ORM (`internal/infra/db`, SQLite or Postgres via `DATABASE_URL`); migrations live in `internal/infra/db/migrations` and run on startup.

**Orchestration:** the agent deploys apps via the `docker compose` CLI (not the Docker SDK), in `internal/infra/orchestrator/docker_compose`. App sources are written per-revision under `{AGENT_DATA_DIR}/apps_templates/{appID}/{rev}/`, rendered with `${VAR}` substitution (`pkg/template`) into `{AGENT_DATA_DIR}/apps/{appID}/`, then brought up. Only the last 3 revisions are kept.

**Auth:** `go-pkgz/auth` with Google OAuth + an env-credential fallback (`internal/app/web/auth`); JWT-protected `/api/v1/*` routes; mTLS for hub↔agent.
