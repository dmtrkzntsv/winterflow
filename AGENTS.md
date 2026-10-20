# Repository Guidelines

## Project Structure & Module Organization

```
web/src/
  components/        # Re-usable presentation & layout primitives
    ui/              # Low-level design-system / shadcn components (button, card …)
    app-page/        # The app page's header and tabs (Overview, Logs, Configuration, …)
    alerts/          # Monitoring, Inbox and alert-rule components
    new-app/         # The New app chooser and its Template / Compose / Git forms
    <feature>-*      # Other feature-scoped components (app-*, backup-*, template-*, …)

  pages/             # Route-level components rendered by React-Router (settings-* are
                     #   the Settings → Server / Organization / Account pages)
  hooks/             # Custom React hooks
  contexts/          # React Context providers & related reducers
  lib/               # Framework-agnostic helpers (api, config, routes, utils)
  types/             # Shared TypeScript typings / interfaces
  styles/            # Global styles and Tailwind configuration
```


`cmd/` hosts the Go entrypoints (standalone, API, hub, agent), while shared business logic lives under `internal/` and helper packages under `pkg/`. 
Frontend code resides in `web/`, a Vite + React 19 workspace that uses pnpm (version pinned by `packageManager` in `web/package.json`; CI reads it too), Tailwind 4, and shadcn/ui; assets, hooks, and UI components are co-located under `web/src/`. 
Persistent artifacts such as SQLite files and local certificates live in `data/`. 
Keep tests beside the code they exercise (e.g., `internal/agents/agent_test.go`) to simplify package imports.

## Architecture

WinterFlow ships as four binaries from one repo and supports two topologies:

- **Standalone** (`cmd/standalone`): a single process — HTTP API, agent, and Docker Compose orchestrator — backed by SQLite. The command bus and the "hub" run **in-process** (`internal/app/agent/inprocess.go` over `internal/infra/transport/mem/bus`); no Redis or gRPC hop.
- **Distributed**: a horizontally-scalable **API** (`cmd/api`, the "brain", owns persistence) ⇄ **Redis Bus** (`internal/infra/transport/redis/bus`) ⇄ horizontally-scalable **Hub** (`cmd/hub`, gRPC server, mTLS) ⇄ **Agent** (`cmd/agent`). The API publishes a command onto the request queue; the Hub forwards it down the agent's gRPC stream; the agent's reply travels back up onto the response queue, where the API's `reply.Manager` wakes the blocked caller.

Layering is hexagonal: `internal/domain/{model,port,usecase,service}` (business logic, transport-agnostic) over `internal/infra/{db,transport,orchestrator,cert}` (adapters), with `internal/app/{web,agent}` as the delivery layer. There is **no DI/factory abstraction** — each binary wires its concrete dependencies explicitly in `internal/infra/bootstrap` and hands a `Deps` struct to the server.

**Persistence** is sqlc (`internal/infra/db`): per-engine SQL in `internal/infra/db/sqlc/query/{sqlite,pgsql}` and `internal/infra/db/migrations/{sqlite,pgsql}` (same file names and versions on both engines), generated packages `sqlitegen`/`pgsqlgen` (`make sqlc`, never edited by hand), and one package per repository under `internal/infra/db/repository/` (`repo.go` + `sqlite.go` + `pgsql.go`). Dialect differences live only in the `.sql` files. Migrations are forward-only and run on startup from `db.Open`.

**Single gRPC envelope + typed commands.** All hub↔agent traffic rides one envelope (`proto.RequestEnvelope`/`ResponseEnvelope`: a `type` string + JSON `payload`). The catalog of command types and their payload structs lives in `internal/domain/command`; `internal/infra/transport/codec` is the only place that (de)serializes payloads, so handlers on both ends work with typed Go values. Add a feature by: defining a command type + payload struct, adding an agent handler in `internal/app/agent`, and adding a usecase + route on the API side. A route is one `api.Route` entry in its handler package's `Routes()` (`internal/app/web/api`): the same table registers it with chi and generates `api/openapi.yaml`, so run `make openapi` and commit the result. Response schemas require every non-`omitempty` field (request-side copies of shared types are the lenient `<Name>Input` components); new SSE notification types are declared as `api.EventPayload`s on the `/api/v1/sse` route.

## Build, Test, and Development Commands
- `make build` – compiles the standalone and API binaries into `bin/`.
- `make distributed-build` – bundles the distributed UI and builds `bin/api` and `bin/hub` for the systemd installer.
- `make api | make hub | make agent` – runs the selected service with `go run`.
- `make web` – starts the Vite dev server via `pnpm --dir web dev`.
- `pnpm --dir web run build` – type-checks (`tsc -b`) and produces a production bundle.
- `pnpm --dir web run lint` – runs the ESLint suite configured for React 19.
- `make test` – Go tests exactly as CI runs them (`-race`, coverage into `coverage.out`); `make web-test` – Vitest with coverage into `web/coverage/lcov.info`.
- `make cover` – total Go and web coverage against `coverage-baseline.json` (CI's ratchet); `make diffcov` – CI's gate: lines changed since `$(DIFFCOV_BASE)` (default `origin/main`; override for stacked branches) must be ≥80% covered per language. The ratchet baseline (`coverage-baseline.json`) is calibrated to CI's merged SQLite+Postgres profile, so these SQLite-only local runs can report below it; for CI's numbers append the Postgres profile first (`make test && make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out' && tail -n +2 contract-postgres.out >> coverage.out && make web-test`), then run `go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info` or `go run ./tools/diffcov --base <base> --min 80 --go coverage.out --lcov web/coverage/lcov.info`.
- `make openapi` – regenerates `api/openapi.yaml` (OpenAPI 3.1) from the route tables; `TestOpenAPIUpToDate` fails until the committed file matches.
- `make test-pg` – the API contract suite (`internal/app/web/contract`) and the `internal/infra/db` packages (migration runner, repositories) against a throwaway `postgres:16` container (Docker); plain `go test ./...` runs them on SQLite. Extra flags: `make test-pg TEST_PG_ARGS='-run TestParity -v'`.
- `make sqlc` – regenerates `internal/infra/db/sqlc/gen` from the queries and migrations; `make sqlc-check` (CI) fails when it is stale.
- `make generate-hub-certs` / `make generate-agent-certs` – bootstrap local mTLS material for hub↔agent testing.

## Coding Style & Naming Conventions
Go code must stay `gofmt`-clean and pass `go vet`; prefer descriptive, lowercase package names (`internal/tunnels`). 
Exported Go types use CamelCase, while private identifiers remain lowerCamelCase. 
Frontend code follows TypeScript strict mode with React function components and Tailwind utility classes; co-locate styles via Tailwind tokens instead of ad-hoc CSS files. 
Use PascalCase for components (`AgentList`) and kebab-case for filenames (`agent-list.tsx`) unless the file defines a component (then match its name).

## Testing Guidelines
Unit and integration coverage rely on the standard Go toolchain: run `go test ./...` from the repo root. 
Add table-driven tests beside the code they exercise (`*_test.go` in the same package) when adding logic in `internal/` or `pkg/` — favour testing pure helpers and message/transport correlation (see `internal/infra/transport/codec`, `internal/infra/transport/grpc/hub`, and `internal/infra/orchestrator/docker_compose` for examples). 
Frontend unit tests use Vitest + Testing Library on jsdom (`pnpm --dir web test`; `test:coverage` also writes `web/coverage/lcov.info`). Put `*.test.ts(x)` beside the code, keep pure helpers in `web/src/lib/` and test them directly, and render components inside their real context providers (`ProfileContext`, `AppsContext`, `MemoryRouter`) with fake objects for agent-bound APIs — see `web/src/pages/settings-volumes.test.tsx`. 
The API contract suite (`internal/app/web/contract`, test files only) drives every route in the route table through the real router, middleware, database and in-process bridge, with a scripted fake agent (`agent.on`/`agent.fail`) instead of Docker. Every request and response is validated against `api/openapi.yaml`; agent-bound routes are followed over SSE to their `operation_result` (open the stream first: `client.subscribe`, done by `claim`/`pat`/`member`). A new route needs a happy-path contract test — `TestMain` fails listing any table route that never answered below 400 (401/403/400 probes do not count; with `WF_CONTRACT_GUARD=require`, as in CI, a partial `-run`/`-skip`/`-short` run fails instead of skipping the guard) — and a `probes` entry if it takes input. It runs on SQLite by default and on Postgres when `WF_TEST_DATABASE_URL=postgres://…` (or `postgresql://`) is set (a schema per test); any other scheme fails the run. `TestParity` compares a normalized transcript with `testdata/parity.golden.json` on both databases; regenerate it with `go test ./internal/app/web/contract -run TestParity -update` and review the diff.
CI (`.github/workflows/ci.yml`, run by every PR) enforces gofmt, `go vet`, `go test -race -coverpkg=./...`, the contract suite on SQLite (inside `go test`) and, with the `internal/infra/db` packages, on Postgres (`contract` job, whose coverage is merged into the gates), `make sqlc-check`, web lint/test/build and `scripts/install_test.sh`, plus two coverage gates from `tools/diffcov`: changed lines must be ≥80% covered per language (tests, `*.pb.go`, `internal/infra/db/sqlc/gen/` (also left out of the ratchet total), `cmd/*/main.go`, `tools/`, `web/src/components/ui/`, `web/src/test/` and `web/src/main.tsx` are exempt; so are Go files with a `// Code generated … DO NOT EDIT.` header, and comment/blank lines inside Go statement blocks are ignored), and total coverage may not fall more than 0.1 below `coverage-baseline.json`. When CI prints `::notice::raise coverage-baseline.json …`, bump the file in the same PR. 
Always mention which suites you executed in PR descriptions.

## Commit & Pull Request Guidelines
The Git history follows Conventional Commits (`feat:`, `fix:`, `refactor:`). 
Keep messages imperative (“feat: add tunnel watcher”) and scope them narrowly. 
PRs should describe motivation, summarize code changes, list validation commands, and link issues or Linear tickets. 
Include screenshots or recordings for UI-facing updates (Vite dev server output, component previews) and note any configuration steps contributors must repeat.

## Security & Configuration Tips
Never commit secrets or TLS keys: `.gitignore` already excludes build outputs, but confirm new artifacts stay out of version control. 
Use the provided `generate-*-certs` targets for local mTLS needs, and store `.env` values under `data/` or in your shell, not in source. 
When reviewing contributions, ensure new tunnels, agents, or CLI flags respect the 1-user-per-org model and keep Redis/PostgreSQL credentials confined to configuration files.
