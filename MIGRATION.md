# WinterFlow v1 → v2 Migration

Status doc for the ongoing port of WinterFlow from its two v1 repos into this v2
monorepo. Read this with `CLAUDE.md` (architecture + commands) and `AGENTS.md`
(contributor guide). This file is the "where are we / how to continue" map.

## Goal

Port **all** of v1's product functionality into the **v2 monorepo**, which
unifies the v1 `winterflow-agent` and `winterflow-app` codebases into one repo
that builds four binaries (`standalone`, `api`, `hub`, `agent`) and serves two
topologies from the same business logic:

- **Standalone** — one process: HTTP API + agent + Docker Compose orchestrator
  over SQLite, with the bus and hub running in-process (no Redis, no gRPC hop).
- **Distributed** — horizontally-scalable **API** ⇄ **Redis bus** ⇄ **Hub**
  (gRPC + mTLS) ⇄ **Agent**.

The only thing that differs between topologies is how a command is transported;
the domain logic is shared.

### Source repos (reference only)

The v1 repos were cloned to `/tmp` for reference during the migration:

- `/tmp/winterflow-v1-agent` — v1 agent (orchestrator, self-update, crypto).
- `/tmp/winterflow-v1-app` — v1 web app + backend API.
- Live v1 (production) for UI/visual reference: `https://app.winterflow.io/`.

> These are not part of the repo and may not exist in a fresh environment —
> re-clone from `github.com/winterflowio/winterflow-agent` and
> `github.com/winterflowio/winterflow-app` if you need them again.

## The core architectural decision: the async command contract

This is the single most important thing to understand before adding anything.

v1 used blocking request/reply. **v2 is fire-and-forward**, with three delivery
patterns chosen by whether the agent is involved:

1. **API-local read → `200` + data (synchronous).** Anything the API can answer
   from its own DB/cache: `get-servers`, the app *list* (`get-apps`), cached
   status (`get-servers-status`, `get-apps-status`), the public key. No agent
   hop, no SSE.
2. **Agent-bound command → `202 {request_id}`, result over SSE.** Anything that
   needs the agent: every mutation plus agent-authoritative reads
   (`app.get`, `app.logs`, `apps.list`, registry/network list, `agent.update`).
   The handler validates, dispatches to the bus, and returns `202` immediately —
   it never blocks. The agent's result returns up the bus and is delivered to
   the browser over the **SSE stream** (`/api/v1/notification/stream`),
   correlated by `request_id`.
3. **Agent-initiated push → SSE (unsolicited).** Heartbeat liveness +
   capabilities + app/server status events flow up the agent stream; the Hub
   forwards them to `events:<region>`; the region's API updates its DB/cache and
   pushes to open SSE connections.

**Addressing: a `REGION` = exactly one API instance.** Queue names are
region-scoped (`requests:<REGION>`, `responses:<REGION>`, `events:<REGION>`),
each consumed by that region's single API. No per-instance ids, no fan-out.
`REGION` must match across API and Hub.

**Info vs. status are separate concerns:**
- **Info** (servers, apps — name/config/metadata) is **DB-backed**, served
  synchronously. The DB is a reconciled cache; **the agent's filesystem is the
  source of truth** for deployed apps.
- **Status** (liveness, container state) is **never stored in the DB**. It lives
  in an in-memory, TTL'd cache on the API (`internal/domain/service/status`),
  fed by agent events. Missing/expired ⇒ **unknown** (we never infer "offline"
  from silence). `last_seen` IS persisted (durable info, distinct from live
  status).

**Apps two-stage load:** the UI renders instantly from the DB cache
(`get-apps`), then dispatches `apps.list` to the agent; on its SSE result the API
runs a **full sync** (`SyncApps`: upsert reported, delete missing) so the DB
mirrors the agent, and the UI re-reads.

## The single gRPC envelope + typed commands

All hub↔agent traffic rides **one** envelope
(`proto.RequestEnvelope`/`ResponseEnvelope` = a `type` string + JSON `payload`),
replacing v1's ~14 hand-rolled oneof RPC pairs. The command catalog lives in
`internal/domain/command`; `internal/infra/transport/codec` is the ONLY place
that (de)serializes payloads.

### The repeatable pattern — every feature follows this

1. **Command type + payload structs** in `internal/domain/command/`.
2. **Orchestrator op** in `internal/infra/orchestrator/docker_compose/`
   (CLI-based).
3. **One registration line** in the agent Dispatcher's `newHandlers` map
   (`internal/app/agent/dispatcher.go`) — the generic `handle[Req, Resp]`
   adapter does the decode/encode.
4. **API side**: domain `port` method → usecase (`internal/domain/usecase/...`)
   that calls `CommandDispatcher.Dispatch` (publishes, returns `request_id`; use
   the `OnResult` hook for DB side effects) → HTTP handler + route in
   `internal/app/web/`. Handler returns `202 {request_id}`; result over SSE.
5. **Web UI**: context/hook dispatches the command and `await`s the matching SSE
   notification by `request_id` (the `waitFor(requestId)` helper in
   `notifications-context`). Refetch the affected list on success.
6. **Tests** for pure helpers/codec; Docker integration tests behind the
   `integration` build tag for orchestrator ops.

`app.save` is the reference agent-command path. `get-servers`/`get-apps` are the
reference DB-backed reads.

## App secrets — ECIES end-to-end

App secrets (and registry passwords) are encrypted in the browser and decrypted
only on the agent. Scheme (`pkg/crypto`, ported verbatim from v1): **P-256 ECDH
→ SHA-256 → AES-256-GCM**, wire format `base64(ephemeralPubKey(65) | iv(12) |
ciphertext+tag)`.

- The agent publishes its EC public key as a `public_key` capability; the API
  serves it at `GET /api/v1/server/get-public-key` (standalone falls back to the
  local agent cert). The agent decrypts with its mTLS private key.
- The browser implements the matching encrypt in `web/src/lib/ecies.ts` via Web
  Crypto.
- On edit, unchanged secrets are sent as the `<encrypted>` placeholder; the
  agent preserves the stored ciphertext and never returns plaintext (app.get
  masks encrypted fields). Since A1, secrets are encrypted at rest on the
  agent too (committed `secrets.json` holds ciphertext only).

## What's done (✅) and what's not

### ✅ Phase 1 — Foundation
- Fire-and-forward command contract (202 + SSE), region addressing.
- App DB persistence + `apps.list` reconcile (full sync).
- SSE stream uses the authenticated user id.
- Capabilities/heartbeat write path; in-memory TTL status cache; `last_seen` in DB.
- PAT (personal access token) auth wired into `BasicAuthChecker`.

### ✅ Phase 2 — App lifecycle (backend + UI, incl. secrets)
- Commands: `app.save`, `app.get`, `apps.list`, `apps.status`, `app.control`
  (start/stop/restart/update), `app.delete`, `app.rename`, `app.logs`.
- ECIES secret pipeline (crypto pkg, public-key endpoint, browser encrypt,
  `<encrypted>` placeholder preservation).
- Web: create-app editor (compose + files + variables + icon picker with v1's
  full icon set), **edit existing app**, app **details page** (controls + logs +
  settings tabs).

### ✅ Phase 3 — Docker registries & networks
- Commands: `registry.list/create/delete`, `network.list/create/delete`.
- Orchestrator via `docker login/logout` and `docker network ls/create/rm`.
- Registry passwords use the same ECIES scheme.
- Web: folded into the **Server Settings** page (`/settings`).
- **Note:** intentionally **no DB tables** for registries/networks — they live on
  the agent (`~/.docker/config.json` and the Docker daemon); the agent is the
  source of truth, matching v1's actual behavior.

### ✅ Phase 4 — Agent self-update & hardening
- Agent gRPC **reconnect with backoff** (1s–30s) via a supervising `Run(ctx)`
  loop (`internal/infra/transport/grpc/agent`).
- **`agent.update`** self-update (download release for os/arch, atomic replace,
  exit to restart; only newer versions; `pkg/version`).
- **Hub stale-agent cleanup**: removes agents on stream close + reaps agents idle
  past a TTL (~3 missed heartbeats).
- **Data-restore** (v1's `--restore`) was deliberately NOT ported as a separate
  protocol — v2's continuous `apps.list`→`SyncApps` reconcile already re-seeds
  the DB when an agent reconnects with its data volume intact.

### ✅ UI structure alignment to v1 (most recent work)
- App **details page** at `/app/:appId` (header icon/name/status/version +
  control buttons + Logs/Settings tabs). App cards link here.
- Dashboard: selectable **server cards row** (`ServerCards`) + apps wrapped in a
  titled **"Apps" Card**.
- **Server Settings** page (`/settings`) holds registries/networks/agent-update;
  sidebar nav = Dashboard + Apps group + bottom-pinned Server Settings.

### ✅ Phase 5 — Backend cleanup + live status + frontend refresh (2026-07)

Spec: `docs/superpowers/specs/2026-07-02-v2-finish-migration-design.md`.

- **Backend cleanup (~900 lines):** dead code deleted (PAT management surface,
  AddServer chain, vestigial ports/models, hub cancel machinery, transport/dto);
  `bus.SubscribeJSON` generic consumer; shared `codec.EnvelopeFromCommand` /
  `NotificationFromResponse` used by both bridges; shared `wireCore` bootstrap
  for standalone + API.
- **Live status pipeline (the apps-status producer was missing — the endpoint
  always returned `[]`):** the agent now pushes `apps.status` every 30s
  (immediately on start) — distributed via the new `AgentEvent` stream message,
  standalone straight onto the events queue. The API fans transitions out over
  SSE as unsolicited notifications: `server_status {server_id, liveness}` on
  unknown↔online flips (a 15s sweeper emits the offline direction) and
  `apps_status {server_id, apps}` per report, to all members of the owning org.
  The agent also heartbeats immediately on stream start (previously commands
  bounced with "agent not connected" for up to 30s after connect).
- **Hardware capabilities:** agents report `server_ip`, `system_cpu_cores`,
  `system_memory_total`, `system_disk_total` (pkg/sysinfo, stdlib collectors);
  `get-servers` serializes capabilities.
- **Frontend:** v1 theme tokens; v1 URL structure (`/create-app`; the edit
  route is gone — editing is an Editor tab on app details, `?tab=` addressable);
  CodeMirror 6 file editor (yaml highlighting); lightweight logs viewer
  (tail select + sticky autoscroll); v1-style square app cards with
  status-colored borders and staggered entrance animation, fed by SSE; server
  cards with status dot, IP, specs, agent version, SSE-driven.
- **Tests:** backend coverage 10.7% → **60.6%** (63.9% excluding generated
  gRPC code), incl. a full hub↔agent E2E over real in-process mTLS.

### ❌ Explicitly out of scope (deferred — NOT in the migration)
- **Catalog / one-click app templates** (considered 2026-07 and dropped).
- **Organization & member management** (v2 auto-creates one org per user).
- **Stripe billing / subscriptions** (the `subscription_status` columns were
  removed; when re-added it'll be in dedicated tables).

### ✅ Phase A1 — git-per-app deployment rework (2026-07)

The app folder IS the deployment (`docker compose` runs in it directly):

```
{AGENT_DATA_DIR}/
  apps/                      # `ls apps/` = the app list: {slug} -> ../apps-data/{appID}
  apps-data/{appID}/         # canonical folder, a git repository
    .winterflow/config.json  # committed app config blob
    .winterflow/secrets.json # committed, ECIES ciphertext only
    compose.yml, <files...>  # committed verbatim
    .env                     # committed plain variables
    .env.secrets             # gitignored; decrypted at deploy
```

- **History = git** (go-git, no host git needed): every save/rename commits;
  `app.rollback` restores an old tree as a NEW commit (linear history) and
  redeploys; `app.revisions` lists the log. UI: **History tab** with rollback.
- **Secrets are now encrypted at rest** — the old layout stored resolved
  plaintext in revisions; now plaintext exists only in the gitignored deploy
  outputs, never in git objects. `"<encrypted>"` placeholders preserve the
  previous *ciphertext* without any decryption.
- **Compose-native env:** no custom `${VAR}` rendering (`pkg/template`
  deleted); compose interpolates from `--env-file .env --env-file
  .env.secrets`. App version = commit count.
- Ride-alongs: dispatcher switch → typed registration map (adding a command =
  one line; `codec.NewRequestPayload`/`EncodeRequest` deleted);
  `RequireUser`/`DecodeBody` web helpers (auth failures now 401, was 400);
  `db/service` shim + `usecase/server`/`usecase/docker` pass-throughs deleted
  (repositories implement the ports; `usecase/app` stays for its OnResult
  hooks).
- No data migration: the old `apps_templates`/rendered layout simply stops
  being read (v2 was never released).
- Verified end-to-end in a real browser + Docker: 16-check E2E covering slug
  symlinks, commits, secret-at-rest guarantees, edit→commit, UI rollback.

### ✅ Phase A2 — git-sourced apps + image tags (2026-07)

- **Deploy from a repo URL:** the "Deploy from Git" editor card configures
  repo URL/branch/compose path; the agent clones into the app's gitignored
  `source/`, pins the deployed SHA in the committed `source.lock`, and runs
  compose against `source/{compose_path}` (repo-root compose auto-detected;
  a winterflow-authored root `compose.yml` referencing `./source` works when
  the repo has none). Env files are passed explicitly so the app's committed
  `.env` keeps driving interpolation.
- **Rollback restores the source position too** — the lock rides in every
  save commit, and rollback checks `source/` out at exactly that SHA.
- **Auto-update:** an agent-side poller (30s tick, per-app interval, default
  120s, toggleable) fetches upstreams and re-pins + redeploys on new commits
  — works behind NAT, no webhooks needed.
- **Private repos:** ECIES-encrypted access token stored in `secrets.json`
  (`x-access-token` transport auth), placeholder semantics like every secret.
- **`image.tags`:** registry HTTP v2 tag listing with the agent's docker
  credentials (Docker Hub bearer flow, pagination); the editor shows image
  chips under compose files with a filterable tag picker that rewrites the
  reference.
- Verified: 13-check live E2E (clone/pin/gitignore, deploy from repo compose,
  update→re-pin, rollback→source v1).

### ✅ Post-migration: draft saves (2026-07)

`app.save` accepts `draft: true`: changes are committed without deploying.
The gitignored `.winterflow/deployed` mark (written after every successful
compose up) tracks what is actually live, `app.revisions` returns it, and
the History tab badges **Deployed** vs **Draft** with a Deploy button for an
undeployed HEAD. The Editor tab offers **Save draft** next to
**Save & redeploy**.

## 🎉 Migration status: COMPLETE

Every planned phase has shipped: foundation, app lifecycle + secrets,
registries/networks, hardening, UI structure, backend cleanup, live status,
frontend refresh, the git-per-app deployment rework (A1), and git-sourced
apps (A2). Deliberately not ported: catalog/templates (dropped), orgs &
members, billing.

### ⚠️ Known partial / follow-ups

All previously-listed follow-ups are resolved (2026-07):
- The web ESLint suite passes with **zero errors** (the shadcn `sidebar.tsx`
  hook re-export moved out of the component file).
- `cert.Manager.GenerateServer(false)` now **heals consistently**: a broken
  CA pair regenerates the CA and everything signed by it; a new server key
  cascades to the server cert and full chain.
- The agent's identity is **stable and cert-derived**: `AGENT_ID` env >
  agent-cert CommonName (registration now writes the server id there) > an
  id persisted under the data dir. No more per-run `agent-<timestamp>`.

## Current command + route surface (source of truth: `routes.go`, `command.go`)

Commands: `app.save`, `app.get`, `apps.list`, `apps.status`, `app.control`,
`app.delete`, `app.rename`, `app.logs`, `app.revisions`, `app.rollback`,
`image.tags`, `registry.list/create/delete`, `network.list/create/delete`,
`agent.update`.

API routes (all `/api/v1`):
- Info (200 sync): `server/get-servers`, `server/get-servers-status`,
  `server/get-public-key`, `app/get-apps`, `app/get-apps-status`.
- Agent-bound (202 + SSE): `app/create-app`, `app/get-app`, `app/get-logs`,
  `app/get-revisions`, `app/rollback-app`, `image/get-tags`, `app/control-app`,
  `app/delete-app`, `app/rename-app`, `app/refresh-apps`,
  `registry/{list,create,delete}`, `network/{list,create,delete}`,
  `agent/update`.
- SSE: `notification/stream`. Auth/server: `server/register`, `/auth/*`.
- Unsolicited SSE notification types (no `ref`): `server_status`
  (`{server_id, liveness}`) and `apps_status` (`{server_id, apps}`), pushed to
  the owning org's members on agent events/transitions.

Web pages (`web/src/pages/`): `home` (`/`), `app-details` (`/app/:appId`,
tabs Logs/Editor/History/Settings via `?tab=`), `create-app` (`/create-app`,
create-only), `settings`, `login`.

## Key files by area

- **Command catalog / codec:** `internal/domain/command/*.go`,
  `internal/infra/transport/codec/codec.go`.
- **Agent dispatch:** `internal/app/agent/dispatcher.go`,
  `dispatcher_docker.go`, `inprocess.go` (standalone in-process bridge).
- **Orchestrator (docker CLI):** `internal/infra/orchestrator/docker_compose/`
  (`operations.go`, `lifecycle.go`, `logs.go`, `docker.go`, `updater.go`,
  `secrets.go`, `status.go`, `revisions.go`).
- **Crypto:** `pkg/crypto/ecies.go`; browser `web/src/lib/ecies.ts`.
- **Async contract / SSE:** `internal/infra/transport/dispatch/manager.go`
  (request_id→userID, OnResult), `internal/infra/bootstrap/{api,standalone,events}.go`,
  `internal/app/web/handler/notification/handler.go`.
- **Hub:** `internal/infra/transport/grpc/hub/hub.go` (bridge, publishResult,
  publishEvent, reaper). **Agent transport:** `internal/infra/transport/grpc/agent/agent.go`
  (`Run` reconnect loop).
- **Persistence:** `internal/infra/db/{models,migrations,repository}` (Bun;
  SQLite or Postgres via `DATABASE_URL`).
- **Web delivery:** `internal/app/web/{routes.go,bootstrap.go,handler/*}`.
- **Web UI:** `web/src/{pages,context,components,lib}/`. Contexts:
  `servers-context`, `apps-context`, `notifications-context` (SSE + `waitFor`),
  `use-docker`.

## How to continue the migration

1. Pick the next feature (a deferred area, or a follow-up above).
2. Follow **the repeatable pattern** for backend; for UI follow the existing
   context/hook + `waitFor(requestId)` approach.
3. For agent-bound work, decide info-vs-status: info → DB + 200; status →
   in-memory cache; mutation/agent-read → 202 + SSE.
4. Verify per the checklist below before committing. Commit each feature
   separately with a descriptive message.

## Verification

- Backend: `make build` + `go build ./cmd/hub ./cmd/agent`, `go vet ./...`,
  `go test ./...`. Orchestrator integration (needs Docker):
  `go test -tags integration ./internal/infra/orchestrator/docker_compose/`.
- Async contract: every agent-bound endpoint returns `202 {request_id}` with no
  multi-second block; the result arrives on `/api/v1/notification/stream` keyed
  by that id. No `reply.Manager`/`WaitForReply` remains.
- Standalone E2E: `make standalone`; exercise endpoints with an authed session;
  confirm Docker effects (`docker ps`) and DB rows (`sqlite3 winterflow.sqlite`).
- Distributed E2E: Redis + `make hub`/`make agent`/`make api`; confirm the
  round-trip returns over SSE.
- Web: `pnpm --dir web run build` (tsc + Vite) and `run lint` (expect only the
  one pre-existing `sidebar.tsx` error).

## Branch / history

Work lands on the **`v2`** branch (PR #2 against `main`). The migration is a
linear series of feature commits — `git log --oneline` on `v2` reads as the
migration changelog (foundation → app lifecycle → secrets → registries/networks
→ hardening → UI structure).
