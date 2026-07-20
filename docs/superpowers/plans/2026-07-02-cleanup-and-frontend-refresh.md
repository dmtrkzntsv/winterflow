# Backend Cleanup + Frontend Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute Phase 0 (backend cleanup, ~400 lines deleted, structural dedup) and Phase B (live status pipeline + frontend refresh to v1 look) of `docs/superpowers/specs/2026-07-02-v2-finish-migration-design.md`.

**Architecture:** Cleanup first (deletions, then structural refactors under characterization tests). Then the live-status backend (agent event push → hub/bridge → events queue → cache + SSE fan-out), then the web refresh (theme, URLs, editor tab, CodeMirror, logs viewer, v1-style cards fed by SSE). Each task is one commit on `migration`.

**Tech Stack:** Go 1.25 (std testing), Bun ORM + SQLite (in-memory for repo tests), protobuf (`make grpc`), React 19 + Vite, CodeMirror 6 (`@uiw/react-codemirror`).

## Global Constraints

- Two topologies stay; only transport differs. `bus.Bus`, `CommandDispatcher`, domain ports untouched.
- TDD for all backend behavior changes; target >60% statement coverage overall, touched packages well above.
- Verification per commit: `make build && go build ./cmd/hub ./cmd/agent && go vet ./... && go test ./...`; web tasks: `pnpm --dir web run build && pnpm --dir web run lint` (only pre-existing `sidebar.tsx` finding tolerated).
- Commits: one per task, conventional messages, Co-Authored-By + session trailers.
- No data migration; `apps.status` cadence 30s; status TTL stays 90s; sweeper 15s.

---

## Phase 0 — Cleanup

### Task 1: Dead code batch 1 — domain/db/web layers

**Files (all Modify/Delete):**
- `internal/infra/db/repository/user.go` — delete `CreateToken`, `ListTokens`, `RevokeToken` (~lines 181-243); keep `FindByToken`.
- `internal/domain/port/user.go` — delete the three token-mgmt port methods; keep `FindByToken`.
- `internal/domain/model/user.go` — delete `model.Token`.
- `internal/domain/port/server.go` — delete `AddServer` (repo+service), `IsServerRegistered`.
- `internal/infra/db/repository/server.go` — delete `AddServer`, `IsServerRegistered` impls.
- `internal/infra/db/service/server.go` — delete `AddServer` no-op.
- `internal/domain/usecase/server/usecase.go` — delete `AddServer`; also delete unused `NotificationManager` field/dep and its threading from `internal/app/web/routes.go` + `internal/app/web/handler/server/handler.go`.
- `internal/domain/dto/server.go` — delete `ServerDTO` (delete file if empty).
- `internal/infra/db/models/models.go` — delete `ReleaseVersion`; `internal/infra/db/bootstrap.go` — deregister; migration drop-list entry stays (harmless `IF EXISTS`) — actually remove the entry too.
- `internal/domain/port/agent.go` — delete file (interface has zero consumers); `internal/infra/agent/service/agent.go` — delete `HasConfig`, `GenerateConfig`, `HasKeys`, `GenerateKeys` stubs (keep `Register`, `IsRegistered` — verify `IsRegistered` callers first; review says none: delete it too).
- `internal/domain/model/agent.go` — delete `AgentConfig` if orphaned.
- `internal/app/web/middleware/timeout/` — delete directory.
- `internal/app/web/util/response.go` — delete `Failure`.
- `pkg/util/file.go` — delete `FileExists` (file too if empty); `pkg/util/id.go` — delete stray `uuid.New()` line.
- `pkg/config/config.go` — delete `GetApiURL`, `GetMode`, `GetHubCSRPath`, `GetHubFullchainPath`, `GetAgentCSRPath`; move the 10 `Get*Filename()` values to consts in `internal/infra/cert/manager.go`.
- `internal/domain/model/server.go` — delete `Certificate` type + field; KEEP `Capabilities`/`Capability` (Task 12 populates them); delete `Feature` only if `Features` field is never populated AND not needed by B (it isn't — delete field + type, capabilities carry what B needs... **No**: `SaveCapabilities` persists features; keep the DB side, delete only the never-populated `model.Server.Features` field if `GetServers` never fills it — verify with grep before deleting).
- `internal/domain/model/organization.go` — delete `Organization` struct + `RoleAdmin`; keep `RoleOwner`.
- `pkg/util/date.go` — move `NewDateTime` to `internal/infra/db/types` (new func in existing file), update all repository callers, delete `pkg/util/date.go`.

**Steps:**
- [ ] Grep-verify each symbol has zero callers before deleting (`grep -rn "<Symbol>" --include="*.go" | grep -v _test`); skip (and note) any that turn out live.
- [ ] Delete in the order above; after each file group: `go build ./...`.
- [ ] `go vet ./... && go test ./...` — green (fix any test referencing deleted symbols by deleting that test case, not resurrecting code).
- [ ] Commit: `refactor: delete dead code (PAT mgmt, AddServer chain, vestigial ports/models)`

### Task 2: Dead code batch 2 — transport layer

**Files:**
- Delete `internal/infra/transport/dto/` (whole package).
- `internal/infra/transport/grpc/hub/hub.go` — delete `pendingRequests`+lock, `metrics` map, and the whole `cancelFunc` mechanism (registry stores stream only; `reapOnce`/`Shutdown`/stream-close defer stop calling cancel). Update `reaper_test.go` accordingly.
- `internal/infra/transport/redis/bus/bus.go` — delete `BusMessage` alias, use `bus.Message`.
- `internal/infra/transport/codec/codec.go` — delete `UnixTimestamp`. (Keep `NewRequestPayload`/`EncodeRequest` until Task 4 decides their fate.)

**Steps:**
- [ ] Delete; `go build ./... && go vet ./... && go test ./...` green (agent registry entry struct shrinks — adjust `reaper_test.go` constructor literals only).
- [ ] Commit: `refactor: drop dead transport scaffolding (dto pkg, hub request/metrics maps, no-op cancel)`

### Task 3: `bus.SubscribeJSON` helper + response-subscriber dedup

**Files:**
- Create: `internal/infra/transport/bus/subscribe.go` + `subscribe_test.go`
- Modify: `internal/infra/bootstrap/api.go` (`startResponseSubscriber` takes `bus.Bus`), `internal/infra/bootstrap/standalone.go` (delete inline copy, call `startResponseSubscriber`), `internal/infra/bootstrap/events.go` (use helper).

**Interfaces — Produces:**
```go
// SubscribeJSON consumes queue until ctx is done, unmarshalling each message
// into T and invoking handle. Malformed payloads are logged and skipped.
func SubscribeJSON[T any](ctx context.Context, b Bus, queue string, log *logger.Logger, handle func(T))
```

- [ ] **Write failing test** (`subscribe_test.go`, uses `mem/bus`): publish two valid `CommandMessage` + one garbage payload on a queue; assert handler receives exactly the two decoded values, in order; assert returns when ctx cancelled.
- [ ] `go test ./internal/infra/transport/bus/` → FAIL (undefined `SubscribeJSON`).
- [ ] Implement (goroutine-free: runs the loop in a goroutine internally, matching current call sites' `go func()` usage — signature above, fire-and-forget).
- [ ] Test passes. Refactor the three call sites (`startResponseSubscriber` param `b bus.Bus`; standalone deletes its inline loop; `startEventsSubscriber` keeps its `handleEvent` but consumes via helper).
- [ ] Full suite green. Commit: `refactor: generic SubscribeJSON bus consumer; share response subscriber across topologies`

### Task 4: Shared bridge conversions in codec

**Files:**
- Modify: `internal/infra/transport/codec/codec.go` + `codec_test.go`, `internal/infra/transport/grpc/hub/hub.go` (`dispatchToAgent`, `publishResult` mapping), `internal/app/agent/inprocess.go` (`handle`).

**Interfaces — Produces:**
```go
func EnvelopeFromCommand(cmd bus.CommandMessage) *proto.RequestEnvelope   // sets Base, RequestId, Type, ContentType, SchemaVersion from command constants
func NotificationFromResponse(requestID string, resp *proto.ResponseEnvelope) model.Notification
```

- [ ] **Write failing tests**: `EnvelopeFromCommand` round-trip (all fields, constants not literals); `NotificationFromResponse` success (payload passthrough) + error (`ResponseCode != SUCCESS` → `StatusError`, `Detail` → `Error`) + empty payload.
- [ ] FAIL → implement in codec (move the inprocess.go version, it's the correct one) → PASS.
- [ ] Refactor hub + inprocess to call them; delete hub's hardcoded `"1.0.0"`/`"application/json"` literals and its inline notification mapping (keep hub's synthetic "agent not connected" error path).
- [ ] Suite green. Commit: `refactor: single envelope/notification conversion shared by hub and in-process bridge`

### Task 5: Bootstrap `wireCore`

**Files:** Modify `internal/infra/bootstrap/{api.go,standalone.go}`, maybe new `core.go`.

- [ ] Extract `wireCore(ctx, b bus.Bus, conn *db.BunConnection, cfg, log) *Deps` building repos, `NotificationManager`, `dispatch.Manager`, `status.Cache`, and starting response+events subscribers; `BootstrapAPI` = redis conn + `wireCore`; `BootstrapStandalone` = membus + `wireCore` + cert/bridge/register/pulse extras. Pure move — no behavior change.
- [ ] `make build && go build ./cmd/hub ./cmd/agent && go test ./...`; boot `standalone` briefly (`timeout 5 ./bin/standalone` with temp `.env`) — starts clean.
- [ ] Commit: `refactor: shared wireCore bootstrap for standalone and API`

---

## Phase B — Live status backend

### Task 6: Status cache transitions + sweeper primitive

**Files:** `internal/domain/service/status/cache.go` + `cache_test.go`.

**Interfaces — Produces:**
```go
func (c *Cache) MarkOnline(serverID string, now time.Time) bool        // true when unknown→online
func (c *Cache) SetAppStatus(serverID string, apps []command.AppStatus, now time.Time) bool // returns MarkOnline's transition
func (c *Cache) ExpireStale(now time.Time) []string                   // server IDs that flipped online→unknown; each returned once
```

- [ ] **Failing tests:** first `MarkOnline` returns true; second within TTL returns false; after expiry returns true again. `ExpireStale` before TTL → empty; after TTL → `[id]` once, second call → empty; re-online then expire → returned again. `SetAppStatus` propagates the transition bool.
- [ ] FAIL → implement (entry keeps `expiresAt`; `ExpireStale` deletes expired entries and returns their IDs) → PASS.
- [ ] Commit: `feat(status): liveness transition detection and stale expiry sweep`

### Task 7: Repo — org-member user IDs for a server

**Files:** `internal/domain/port/server.go`, `internal/infra/db/repository/server.go` + new `server_test.go` (in-memory SQLite via existing db bootstrap helpers; check `internal/infra/db` for an existing test harness first and reuse it).

**Interfaces — Produces:** `GetServerUserIDs(ctx, serverID string) ([]string, error)` — user IDs of the server's organization's members.

- [ ] **Failing test:** seed org + 2 users (members) + 1 outsider + server → returns exactly the 2 member IDs; unknown server → empty, no error.
- [ ] FAIL → implement (join `organization_users` on `servers.organization_id`) → PASS.
- [ ] Commit: `feat(db): resolve a server's org member user ids`

### Task 8: SSE fan-out — server_status + apps_status notifications, sweeper

**Files:**
- Modify: `internal/domain/model/notification.go` (types + payloads), `internal/infra/bootstrap/events.go` (fan-out in `handleEvent`, new `startStatusSweeper`), wire `nm`+repo through `startEventsSubscriber` call sites (both topologies via Task 5's `wireCore`); standalone's `markEmbeddedServerOnline` also publishes transition via the same helper.
- Test: `internal/infra/bootstrap/events_test.go` (fakes for `port.NotificationManager` + a `userIDs` func).

**Interfaces — Produces:**
```go
const NotificationServerStatus NotificationType = "server_status"   // payload {server_id, liveness}
const NotificationAppsStatus   NotificationType = "apps_status"     // payload {server_id, apps:[{app_id,status}]}
type ServerStatusPayload struct { ServerID string `json:"server_id"`; Liveness status.Liveness `json:"liveness"` }
type AppsStatusPayload struct { ServerID string `json:"server_id"`; Apps []command.AppStatus `json:"apps"` }
func startStatusSweeper(ctx, cache *status.Cache, fanout func(serverID string, n model.Notification), log) // 15s ticker → ExpireStale → server_status unknown
```
Fan-out helper: resolve user IDs via repo, `nm.Publish(uid, n)` for each; log-and-continue on repo error.

- [ ] **Failing tests:** (a) `handleEvent` EventServerOnline with transition → each org user receives one `server_status{online}`; without transition → none. (b) EventAppsStatus → users receive `apps_status` with decoded apps AND `server_status{online}` if it was also a transition. (c) sweeper tick with expired server → `server_status{unknown}` fan-out. Restructure `handleEvent` deps into a small `eventSink` struct so fakes inject cleanly.
- [ ] FAIL → implement → PASS. Suite green.
- [ ] Commit: `feat(api): push server/app status transitions to browsers over SSE`

### Task 9: Proto AgentEvent + hub forwarding

**Files:** `internal/infra/transport/grpc/proto/hub.proto` (+ `make grpc` regen), `internal/infra/transport/grpc/hub/hub.go` (stream case), `internal/infra/transport/grpc/agent/agent.go` (send API).

**Proto:**
```proto
message AgentEvent {
  BaseMessage base = 1;
  string kind = 2;      // bus.EventKind value, e.g. "apps.status"
  bytes payload = 3;    // JSON body for kind
}
// AgentMessage oneof: AgentEvent event = 2;
```

**Interfaces — Produces:** `(a *Agent) SendEvent(kind string, payload []byte) error` (nil/no-op error when stream down); hub `AgentStream` case `*proto.AgentMessage_Event` → `h.publishEvent(bus.EventKind(ev.Kind), agentID, ev.Payload)`.

- [ ] Edit proto, `make grpc`, `go build ./...`.
- [ ] Hub case + agent `SendEvent` (guard stream nil, reuse send mutex pattern of heartbeat). Unit-test the hub mapping if the stream handler structure permits a narrow test; otherwise covered by Task 10's reporter test + E2E.
- [ ] Suite green. Commit: `feat(grpc): agent-initiated event message on the stream`

### Task 10: Agent status reporter (both topologies)

**Files:**
- Create: `internal/app/agent/reporter.go` + `reporter_test.go`
- Modify: `cmd/agent/main.go` (start reporter with gRPC sink), `internal/infra/bootstrap/standalone.go` (start reporter with bus sink; delete `markEmbeddedServerOnline` — the reporter's apps.status event now feeds liveness via `SetAppStatus`, and capabilities save at startup covers registration).

**Interfaces — Produces:**
```go
type StatusSource interface { GetAppsStatus(ctx context.Context) ([]command.AppStatus, error) }
type EventSink func(kind bus.EventKind, payload []byte) error
func RunStatusReporter(ctx context.Context, src StatusSource, sink EventSink, interval time.Duration, log *logger.Logger)
// sends immediately on start, then every interval; marshals command.GetAppsStatusResponse{Apps: ...}
```
Standalone sink publishes `bus.EventMessage{ServerID: localServerID, Kind: kind, Payload: p}` on the events queue; gRPC sink is `agent.SendEvent(string(kind), p)`.

- [ ] **Failing tests:** immediate first report; ticker fires (use 10ms interval); source error → logged, loop continues; ctx cancel stops.
- [ ] FAIL → implement → PASS.
- [ ] Wire both binaries. `make build`, suite green.
- [ ] Commit: `feat(agent): periodic apps-status reporting up the event stream`

### Task 11: Hardware/IP capability collectors

**Files:**
- Create: `pkg/sysinfo/sysinfo.go` + `sysinfo_test.go` (port from `/tmp/winterflow-v1-agent/pkg/capabilities/{server_ip,system_cpu_cores,system_memory,system_disk}.go` — stdlib only).
- Modify: `cmd/agent/main.go` (add to capabilities map), `internal/infra/bootstrap/standalone.go` (SaveCapabilities for the embedded server at startup: collectors + version + public_key from local cert — mirror what `RegisterAgent` does hub-side).

**Interfaces — Produces:**
```go
func CPUCores() string            // strconv(runtime.NumCPU())
func MemoryTotalBytes() string    // /proc/meminfo MemTotal → bytes; "" on failure
func DiskTotalBytes(path string) string // syscall.Statfs; "" on failure/windows
func ServerIP() string            // UDP dial 8.8.8.8:80 local addr trick; "" on failure
```
Capability keys: `server_ip`, `system_cpu_cores`, `system_memory_total`, `system_disk_total`.

- [ ] **Failing tests (linux):** CPUCores parses to int >0; MemoryTotalBytes parses to int > 1<<20; DiskTotalBytes("/") > 0; DiskTotalBytes("/nonexistent") == ""; ServerIP parses as IP or is "" (CI-safe).
- [ ] FAIL → implement → PASS. Wire both binaries (agent data dir as disk path).
- [ ] Commit: `feat(agent): report ip, cpu, memory, disk capabilities`

### Task 12: Serve capabilities on get-servers

**Files:** `internal/domain/model/server.go` (JSON tags on `Capability`), `internal/infra/db/repository/server.go` (`GetServers` loads + maps capabilities via Bun relation), `server_test.go` (extend Task 7 harness).

- [ ] **Failing test:** seed server + 2 capabilities → `GetServers` returns them as `[]model.Capability{{Name,Value}}`; JSON marshal contains `"capabilities":[{"name":...,"value":...}]`.
- [ ] FAIL → implement (add `Relation("Capabilities")` to the query, map in `toDomainServer`) → PASS.
- [ ] Commit: `feat(api): expose server capabilities in get-servers`

---

## Phase B — Web

### Task 13: v1 theme tokens

**Files:** `web/src/index.css`.

- [ ] Replace the `:root` and `.dark` token blocks with v1's (source: `/tmp/winterflow-v1-app/web/src/index.css` lines 11-77: radius 0.5rem, primary oklch(0.623 0.214 259.815), white surfaces, neutral sidebar; keep any v2-only vars the components reference — grep before deleting).
- [ ] `pnpm --dir web run build && run lint`. Visual sanity via dev server screenshot if available.
- [ ] Commit: `feat(web): adopt v1 theme tokens`

### Task 14: v1 URL structure

**Files:** `web/src/main.tsx` (`/apps/new` → `/create-app`; delete `/apps/:appId/edit` route), plus every `navigate`/`Link` referencing them (`grep -rn "apps/new\|/edit" web/src`).

- [ ] Update routes + links; `create-app.tsx` keeps edit-mode code until Task 15 but the route is gone — do Tasks 14+15 in one commit if the build breaks in between (they may merge; acceptable).
- [ ] Build + lint. Commit: `feat(web): v1 url structure (/create-app; edit route removed)`

### Task 15: Editor tab + create-only create-app

**Files:**
- Create: `web/src/components/app-editor-panel.tsx` — props `{ appId: string }`; owns load (`getApp`), state, save (ECIES encrypt via `getPublicKey`+`encryptSecret`, `<encrypted>` placeholder preserved), toasts; renders `AppEditor` + Save button. Logic moved verbatim from `create-app.tsx`'s edit branches.
- Modify: `web/src/pages/app-details.tsx` — tabs Logs/Editor/Settings; active tab from `?tab=` (`useSearchParams`, default `logs`); Editor tab renders `AppEditorPanel`; header Edit button → sets `?tab=editor`.
- Modify: `web/src/pages/create-app.tsx` — strip `isEdit`/`appId` branches.

- [ ] Implement; verify create flow and edit flow both compile and the save payload shape is unchanged (same `createApp` call).
- [ ] Build + lint. Commit: `feat(web): editor as app-details tab; create-app page is create-only`

### Task 16: CodeMirror 6 editor

**Files:** `web/package.json` (`pnpm --dir web add @uiw/react-codemirror @codemirror/lang-yaml`), `web/src/components/code-editor.tsx` (new thin wrapper: props `{value, onChange, filename}`; yaml extension for `.yml/.yaml`, plain otherwise; theme-aware via `basicSetup`), `web/src/components/app-editor.tsx` (swap `Textarea` for file contents → `CodeEditor`).

- [ ] Implement; keep the variables inputs as plain inputs (only file contents get CodeMirror).
- [ ] Build + lint (watch bundle size; CodeMirror should add ~300KB pre-gzip, fine).
- [ ] Commit: `feat(web): CodeMirror 6 file editor (yaml highlighting)`

### Task 17: Lightweight logs viewer

**Files:** Create `web/src/components/logs-view.tsx`; modify `app-details.tsx` `LogsTab` to use it.

Component contract: props `{lines: string[], loading, onRefresh, tail, onTailChange}`; renders toolbar (tail select 200/500/1000, Refresh button) + `<pre>` in a fixed-height scroll container, monospace, `whitespace-pre-wrap`; auto-scrolls to bottom on new lines unless the user has scrolled up (track via `onScroll` distance-from-bottom > 40px). `LogsTab` keeps its fetch/waitFor logic, passes `tail` through to the API call.

- [ ] Implement. Build + lint. Commit: `feat(web): lightweight logs viewer (tail select, sticky autoscroll)`

### Task 18: Server cards — SSE liveness + specs

**Files:** `web/src/context/servers-context.tsx` (+ base), `web/src/components/server-cards.tsx`.

Context additions: `Server.capabilities: Record<string,string>` (mapped from `capabilities` array in the response); `statusByServer: Record<string, "online"|"unknown">` seeded from one `GET /server/get-servers-status` on mount, then updated via `useNotifications().subscribe` filtering `n.type === "server_status"` (payload `{server_id, liveness}`); on unknown→online transition call `refresh()`.

Card (v1 layout, 348×150-ish): name + status dot (`bg-green-500` online / `bg-gray-400` unknown), IP (`server_ip`), specs line `"{system_cpu_cores} cores • {fmtBytes(system_memory_total)} • {fmtBytes(system_disk_total)} disk"` (omit missing), agent `version`, last-seen line only when not online. `fmtBytes` helper in `web/src/lib/utils.ts` (GB, 1 decimal).

- [ ] Implement. Build + lint. Commit: `feat(web): live server cards (sse liveness, hardware specs, ip)`

### Task 19: App cards — v1 style + animation + SSE status

**Files:** `web/src/context/apps-context.tsx` (subscribe `apps_status` → merge into existing `statusByApp`; keep the 15s poll as reconnect fallback), the app-card component used by `home.tsx` (locate via `grep -rn "AppCard\|statusByApp" web/src/components`), `web/src/index.css` (keyframes).

Card: square tile (`h-32 w-32`), colored icon block (app color bg, white icon), border-2 colored by status (running=green, stopped=gray, error=red — map from existing status values), status label/dot at bottom, `transition-all hover:rotate-3 cursor-pointer`. Entrance: `@keyframes card-in { from {opacity:0; transform:translateY(8px)} to {opacity:1; transform:none} }`, applied with `animation-delay: index*40ms` inline style, `animation-fill-mode: backwards`.

- [ ] Implement. Build + lint. Commit: `feat(web): v1-style animated app cards with live status`

### Task 20: Final verification + docs

- [ ] Full backend: `make build && go build ./cmd/hub ./cmd/agent && go vet ./... && go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -1` — record total %; if <60%, add tests to the largest under-covered touched packages (codec, bus, status, bootstrap events, reporter) until >60% or document the gap honestly.
- [ ] Web: build + lint.
- [ ] Standalone E2E (needs Docker): boot, deploy nginx app, `docker ps` shows it; card status flips without reload (SSE); stop agent process is N/A in standalone — instead verify server card online + specs render; editor tab round-trip; logs tail.
- [ ] Update `MIGRATION.md`: follow-ups section (server cards done, editor tab done, theme done, apps-status producer fixed), command surface unchanged, note the cleanup.
- [ ] Commit: `docs: record cleanup + frontend refresh in MIGRATION.md`
