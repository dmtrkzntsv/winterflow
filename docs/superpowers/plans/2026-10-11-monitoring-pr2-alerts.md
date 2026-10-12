# Monitoring PR 2 (alerts and notification channels) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Org-wide alert rules with per-server and per-app overrides. Rules are evaluated in the API from the metrics and status events. Notifications go to org-wide Shoutrrr channels, set up through a friendly per-service form. An org Alerts page has History, Rules and Channels tabs, plus alert cards, badges and an active-alerts strip.

**Architecture:**
- A pure domain package `internal/domain/alert` holds the model: metric catalog, rules and overrides, the effective-settings merge, per-sample metric extraction, the 60-point window buffer and the message text.
- `internal/domain/service/alerting` has two parts:
  - the `Evaluator`: in-memory buffers and states, fed by the events sink and the 15 s sweep;
  - the `Notifier`: one goroutine draining a bounded queue through a `Sender`.
- `internal/infra/notify` adapts Shoutrrr. `pkg/secretbox` encrypts channel URLs.
- `internal/infra/db/repository/alert` persists everything.
- State transitions are compare-and-set in SQL, so several API replicas (each one receives every event over Redis pub/sub) write one event and send one notification per transition.
- Routes live under `/api/v1/alerts/*`. The SPA adds `/org/alerts` and alert surfaces on the existing monitoring screens.

**Tech Stack:** Go 1.25 (`crypto/hkdf`, AES-GCM), `github.com/nicholas-fedor/shoutrrr v0.18.0`, sqlc over `database/sql` (SQLite and Postgres), chi with the typed route table and OpenAPI, React 19, Vite, Vitest with Testing Library, vendored shadcn/ui.

**Spec:** `docs/superpowers/specs/2026-10-11-monitoring-and-alerts-design.md`. This PR covers §3, the alert parts of §4 (active-alerts strip, badges, Alerts cards, the Org → Alerts page), §6 and §7. PR 1 (metrics) is merged as #13 (`8497b38`); this plan is written against that code.

Prototype status: unlike PR 1, this code was **not** compiled in a scratch copy. Every type and signature it uses from `main` was read at `8497b38` (see "Existing code this plan relies on"). If a step's code fails to compile because of a slip (a missing import, a renamed generated type), fix the slip without changing behaviour and mention it in your report. If a test's expected output differs, investigate before changing the test.

## Design decisions locked in (rulings on spec gaps and conflicts)

1. **Several API replicas.** The Redis bus is pub/sub, so every API replica receives every event. The spec says "the sole consumer", which is wrong.
   - Each replica keeps identical buffers and runs the same evaluation.
   - The database arbitrates each transition:
     - firing: `INSERT … ON CONFLICT DO UPDATE … WHERE state <> 'firing'`;
     - resolving: `UPDATE … WHERE state = 'firing'`.
   - Only the replica whose write changed a row (`won`) appends the `alert_events` row and queues the notification, in the same transaction as the state write.
   - Every replica that computed the transition publishes the SSE `alert` notice to its own browsers. Each browser is connected to exactly one replica.
2. **Targets are `(server_id, app_id)`**, with `app_id = ''` for the server itself, as in `metric_samples`. This replaces the spec's opaque `target_id`.
   - `alert_overrides`, `alert_states` and `alert_events` carry `server_id` with an FK `ON DELETE CASCADE`, so a future server-delete path cleans them up for free.
   - App deletion (`AppRepository.deleteApp`, used by `DeleteApp` and the `SyncApps` reconcile) deletes the app's overrides, states and events in the same transaction, as it already does for samples.
3. **Rules are read from the DB on each evaluation:**
   - two small queries per org per evaluation pass;
   - about one pass per server per minute for metrics and two per minute for `apps.status`;
   - plus the 15 s sweep.

   There is no cache and no cross-replica invalidation, and rule edits take effect on the next sample. Server and app names come from a directory (all servers and apps, two queries) cached for 60 s.
4. **Server down** does not use `servers.last_seen`. Standalone never touches it: its pulse is `apps.status`, which does not call `TouchLastSeen`.
   - The evaluator keeps `heard[server] = time of the last event of any kind`. A server not heard since the API started counts from the API start time.
   - It fires when `now − heard ≥ max(duration, 90 s)`, where 90 s is `statusTTL`, the liveness TTL.
   - It resolves on the next event from that server.
   - App rules need samples or status events, and a down server sends neither, so they are naturally suppressed while their server is down. No extra code is needed.
5. **App down** works as follows:
   - On every `apps.status` event, each known app with `status_code == problematic` starts or continues a `problemSince` clock.
   - It fires once the clock reaches the duration, and resolves on the first report that is not problematic (stopped included).
   - The clock is in memory: after an API restart a still-problematic app re-arms. It stays firing in the DB until it recovers.
6. **Window rule** (spec §3 "Evaluation"):
   - Buffers keep the newest 60 points per target, each stamped with its **minute** (`metrics.Tier1.BucketStart`), so a replayed or refilled minute is deduplicated.
   - The average covers the points newer than `newest − duration` and counts only with `≥ ceil(0.8 × duration)` points.
   - Above the threshold means firing; at or below means ok. Strictly `>`.
7. **Disabling while firing.** If a rule or override is disabled, or a rule is deleted, while firing, the state row is deleted (rule deletion cascades) and the local state dropped. No "resolved" notification is sent.
8. **Channel URLs and the `<encrypted>` convention.**
   - `url_encrypted` holds `base64(nonce‖AES-256-GCM(url))`, with the key `HKDF-SHA256(JWT_SECRET, info "winterflow notification channel urls v1")`.
   - The API never returns the URL. It returns `service` (the URL scheme, `smtp`, `generic`, …) and `fields`: non-secret form values the browser chose to store, such as `{"ui_service":"ntfy","server":"ntfy.sh"}`. These are plaintext and for display and edit prefill only.
   - An update with an empty `url` keeps the stored URL, service and fields; only name, default and enabled change.
   - Changing the destination means re-entering the whole form, secrets included.
   - Without `JWT_SECRET`, saving or testing a channel answers 400 `channel encryption is unavailable: set JWT_SECRET`, and the notifier records `encryption unavailable` per delivery.
9. **Multiple default channels are allowed.** Every enabled default channel receives notifications for rules routed to "default channels".
   - A rule's `channel_ids` is a JSON array, with three meanings:
     - `null`: the org's default channels;
     - `[]`: no notifications (History only);
     - a list: exactly those channels.
   - Ids of deleted channels are ignored at delivery time.
   - Overrides use the same encoding, except that `null` there means "inherit the rule's".
10. **Rule `target_kind` and `metric` are immutable.** `save-rule` with an id updates only `threshold`, `duration_minutes`, `channel_ids` and `enabled`, and answers 400 when kind or metric differ. Down rules store threshold `0`.
11. **Default seeding** happens at three points instead of inside the three org-creation paths of the user repository:
    - at API startup;
    - on every alerts maintenance tick (10 min);
    - at the start of `GET /api/v1/alerts/get-rules`.

    The `MarkAlertDefaultsSeeded` compare-and-set makes it race-free across replicas, and the flag makes deletions permanent. Cost if wrong: a brand-new org can be without default rules for up to 10 minutes, unless someone opens Org → Alerts.
12. **History pruning:**
    - age over 90 days, via one `DELETE … WHERE created_at < now-90d`;
    - beyond 2,000 per org: orgs over the cap are trimmed to the newest 2,000.

    Both run on the 10-minute maintenance tick. Events store `server_name` and `app_name` at event time, so History needs no joins and keeps the names of deleted apps.
13. **Badges and the strip:**
    - `GET /api/v1/metrics/overview` gains `alerts_firing` per server.
    - `GET /api/v1/alerts/get-active?server_id=&app_id=` serves the dashboard strip, the app header block, and the Alerts cards.
    - This replaces "firing count in app responses" from the spec: the app routes are agent round-trips and should not grow DB joins.
14. **SSE `alert`** carries `alert.Notice` (kind, rule, metric, target, value, threshold, title). The SPA shows a toast and refreshes its active-alert views. Clients connected to a replica that did not compute the transition (only possible on that replica's first minute) still catch up through the 60 s poll.
15. **Shoutrrr version.** `github.com/nicholas-fedor/shoutrrr` **v0.18.0** is the newest release whose `go` directive (1.25.0) is not above ours (1.25.3); v0.19+ require Go 1.27. Its `toolchain go1.27.0` line is ignored for dependencies. It pulls in `eclipse/paho.golang`, dual-licensed EPL-2.0 / EDL-1.0 (BSD-3); we take it under EDL. This goes in the PR description. Errors returned by Shoutrrr can embed the URL, so every error text is passed through `notify.Redact` before it is stored or returned.
16. **Route naming** follows the existing verb style: `/api/v1/alerts/get-channels`, `save-channel`, `delete-channel`, `test-channel`, `get-rules`, `save-rule`, `delete-rule`, `get-overrides`, `save-override`, `delete-override`, `get-events`, `get-active`. All are `Auth: api.User` and `Tag: "alerts"`. "Not found" answers 400, as `util.Error` does everywhere.
17. **Org scope** is the caller's primary org (`PrimaryOrganizationID`, as in `handler/org`). A server-addressed call is authorized in two steps:
    - first `webutil.RequireServerAccess`, which answers 403;
    - then the server's org must equal the caller's primary org, also 403.

18. **Outbound destination guard (SSRF).** Channel URLs are user-supplied, and the API makes the request: a save, a Test click, or every alert.
    - In the distributed topology any self-registered org could aim a webhook at the API's own network: Redis, Postgres, `169.254.169.254`.
    - A standalone homelab, by contrast, legitimately notifies a LAN ntfy, Gotify or mail server.
    - So `ALERTS_ALLOW_PRIVATE_TARGETS` defaults to **true in standalone and false on the distributed API**.
    - When private targets are forbidden:
      - HTTP-based services get an injected `http.Client` whose dialer refuses loopback, RFC 1918/ULA, link-local, CGNAT, multicast and unspecified addresses at connect time. This holds even after DNS rebinding.
      - Other services (smtp, mqtt, xmpp, …) have their URL host resolved and refused if any address is private. A small time-of-check-to-time-of-use window remains, an accepted cost.
    - Saving such a URL is allowed; sending is what is guarded.

## Existing code this plan relies on (verified at 8497b38)

- `internal/infra/bootstrap/events.go`:
  - `eventSink.handle` switches on `bus.EventServerOnline`, `EventCapabilities`, `EventAppsStatus` and `EventServerMetrics`, the last one through `storeMetrics(ctx, ev, now)`.
  - `sweep(ctx, cache, now)` runs every `sweepInterval = 15s` in its own goroutine.
  - `statusFanout{userIDs, publish, log}` has `send(ctx, serverID, model.Notification)`.
  - `startEventsSubscriber(ctx, b, cache, serverRepo, nm, cfg, log, indexSync, ms, latest)` builds the sink.
- `internal/infra/bootstrap/core.go`:
  - `WireCore(ctx, b, conn, cfg, log) (*Deps, *serverrepo.Repo)` and `repos{user, server, app, appDomain, serverDomain, metric}`;
  - `newRepos(conn, log)`;
  - the `statusTTL = 90 * time.Second` constant in `events.go`.
- `internal/infra/bootstrap/metrics.go` contains `runMetricsMaintenance`, the pattern for the alerts maintenance loop.
- `internal/domain/metrics`:
  - `Values map[string]float64`;
  - `Tier1.BucketStart(t)`;
  - `UnavailableField = "unavailable"`;
  - `Bucket{Start, Samples, Data}`;
  - server fields `cpu, mem_used, mem_total, disk_pct, load1, load5, load15, net_rx_bps, net_tx_bps, temp`;
  - app fields `cpu, mem_used, mem_limit, disk`.
  - `put` stores zeros and skips NaN/Inf.
- `internal/infra/db/repository/metric/repo.go` already has the private querier method `ListMetricSamplesInRange(ctx, db, rangeParams{Tier, FromTime, ToTime})`.
- `internal/infra/db/repository/app/repo.go`: `deleteApp(ctx, appID)` runs inside the caller's transaction and returns early on `""`.
- `internal/infra/db/dbtest` provides `Open, Ctx, Exec, QueryString, ID(n), SeedOrg, SeedServer(t, d, orgID, serverID), SeedApp(t, d, appID, serverID, name), SeedMember, Log`.
- `internal/infra/db/types`: `DateTime` (with `.Time()`) and `NewDateTime()`. sqlc maps nullable `TIMESTAMP` to `*types.DateTime` and nullable text to `*string`. There is **no** mapping for nullable Postgres `float8`/`int4` yet (Task 3 adds it).
- `internal/app/web/util` provides:
  - `RequireUser(w, r) (string, bool)`;
  - `RequireServerAccess(w, r, access, userID, serverID) bool`;
  - `Error` (400), `Success`, `Forbidden`;
  - `DecodeBody[T](w, r) (T, bool)`.
- `internal/app/web/api.Route{Method, Path, Handler, Auth, Summary, Description, Tag, Query, Request, Response, Errors, EventPayloads…}`.
- Contract suite:
  - `newEnv(t)`, `e.claim(t)` (returns an owner client), `owner.serverID(t)`, `owner.get(t, path, q)`, `owner.post(t, path, body)` (see `org_test.go`), `owner.do(t, call)`, `.expect(t, status).data(t, &v)`;
  - `e.publishEvent(t, bus.EventMessage)`, `eventually(t, cond)`;
  - the `probes` map in `matrix_test.go` with helpers `get`, `post`, `bad`, `sq`, and the `anApp` constant.
- `model.Notification{Type, Ref, Payload, Timestamp}`; the SSE route table is in `handler/notification/routes.go`.
- `pkg/config`: `GetJwtSecret()`, `GetWebURL()`, `IsMetricsEnabled()`.
- `pkg/util.GenerateID()` returns a UUIDv7.
- Web:
  - `usePolled<T>(path, normalize)` and `METRICS_REFRESH_MS` in `web/src/hooks/use-metrics.ts`;
  - `readApiError` in `web/src/lib/api.ts`; `apiBaseUrl` from `@/config`;
  - `useNotifications()` (`subscribe(handler) => unsubscribe`) in `@/context/use-notifications`;
  - `useServers()` (`servers`, `activeServerId`) and `useApps()` (`apps` of the active server);
  - `useServerPath()`, `useAppBreadcrumbs`;
  - `toast` from `sonner`;
  - `web/src/components/ui/*`: card, dialog, tabs, table, switch, select, radio-group, checkbox, badge, input, label, button, tooltip, alert-dialog, empty, spinner;
  - the test pattern of `web/src/pages/org-members.test.tsx`: a `fakeAPI()` fetch mock keyed by path, with `vi.mock("@/layouts/use-app-layout", …)`.

## Main moved on after prototyping (rulings for executors)

`feat/alerts` merged `main` @ b8caadb, which added #12 (storages) and #14 (backups). The prototype was cut from 8497b38, so these places differ. Apply each step's **intent** there; do not reproduce old context lines.

- **Migration:** it is `0012_alerts` (`0011_server_storages` exists).
- **`internal/infra/bootstrap/core.go` (Task 7):** main changed the code the Task 7 diff edits.
  - `startEventsSubscriber` now takes `indexSyncs{newDomainSync(…), newStorageSync(…)}`; add `al.evaluator` as the new last argument.
  - `repos` and `Deps` gained `serverStorage` / `ServerStorageRepository`; add `alert` / `AlertRepository`, `AlertNotifier` and `ChannelBox` beside them.
  - `newRepos` now ends with the storage repository before `metric`; add `alertrepo.New(conn.Tx, conn.Driver, log, util.GenerateID)` last.
  - `domainsync.go` was renamed to `indexsync.go`. Not touched by this plan.
- **Shared tables:** `internal/app/web/routes.go`, `matrix_test.go`, `timestamps_test.go`, `shapes_test.go` and `.env.example` gained storage and backup entries. Add the alert entries alongside them.
- **`web/vite.config.ts` (Task 12):** main already sets `testTimeout: 15_000` and `asyncUtilTimeout: 3000` in `setup.ts`.
  - Keep main's timeout unless the alert dialog tests fail on it; then raise it to `20_000` with the plan's comment.
  - Still add the three Radix Select polyfills to `setup.ts`.
- **`web/src/pages/app-details.tsx` (Task 13):** heavily reworked by #14.
  - Put `<AppAlertsBadge serverId={…} appId={…} />` next to the app's status indicator in the header.
  - Pass `appName` to `AppMetricsTab` wherever that tab is rendered.
- **`go.mod`:** main changed dependencies; `go get …shoutrrr@v0.18.0` then `go mod tidy` still applies. Re-check that `go 1.25.3` (or main's current directive) is unchanged.

## Global Constraints

- Migration `0012_alerts.sql` (PR #12 merged `0011_server_storages`), identical in `migrations/sqlite` and `migrations/pgsql` except types, as for `0010`. Keep `.sql` files ASCII. `TestVersionsAreASequence` requires contiguous versions.
- SQL is written per engine. Every query has the same `-- name:` in `query/sqlite/alerts.sql` and `query/pgsql/alerts.sql`. Run `make sqlc` after any change, never edit `sqlc/gen`, and add every new row/param type pair to `internal/infra/db/sqlc/shapes_test.go`.
- Inside `Tx.WithTx(ctx, fn)`, every query uses `r.tx.Querier(ctx)` with `fn`'s ctx.
- Every DB and network operation has a deadline:
  - each evaluator store call: `storeTimeout = 5 * time.Second`;
  - each channel send: `SendTimeout = 10 * time.Second`;
  - each notifier DB write: `storeTimeout`;
  - each maintenance tick: `alertMaintenanceTimeout = 2 * time.Minute`.
- Bounded memory:
  - 60 points per target (`alert.BufferSize`);
  - buffers, `problemSince` and `heard` are pruned to the directory on every refresh;
  - the notifier queue holds 256 jobs (`QueueSize`) and drops with a warning when full.
- No per-tick SSE fan-out: an `alert` notice is sent only on a transition.
- The distributed API never sends a notification to a private, loopback or link-local address (`ALERTS_ALLOW_PRIVATE_TARGETS`, ruling 18).
- Channel URLs are never returned to the browser and never logged. Every send or parse error goes through `notify.Redact` first.
- Every server-addressed route calls `webutil.RequireServerAccess` before anything else and has a `probes` entry with `valid` (so `TestForeignServer` asserts 403) and `malformed` (400). Every new route needs a happy-path contract call below 400.
- After any route or response-type change:
  - run `make openapi` and commit `api/openapi.yaml`;
  - run `go test ./internal/app/web -run TestRoutesGolden -update`;
  - if `TestParity` fails only because of the new routes, regenerate its golden with `-update` on SQLite and confirm it passes on Postgres (`make test-pg`).
- `.env.example`: document in the same change that rotating `JWT_SECRET` means re-entering channel URLs, and that `WEB_URL` builds the links inside notifications.
- Web:
  - never delete or rewrite anything under `web/src/components/ui/`;
  - component files export only components (`react-refresh/only-export-components`); helpers and constants go in `web/src/lib/`;
  - follow the established Frost look: `Card` surfaces, `text-muted-foreground` meta, `text-destructive` for firing, no new colours.
- On this machine every `go` command needs `GOTMPDIR=$HOME/.cache/wf-gotmp`; create the directory once with `mkdir -p $HOME/.cache/wf-gotmp`.
- Conventional Commits in the imperative mood (`feat(alerts): …`, `test(alerts): …`). Every commit message ends with exactly the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Several API replicas, or a replayed event.** Two evaluators compute the same transition, or one sees a duplicate sample. Expected: exactly one `alert_events` row and one notification. Duplicate minutes do not shift the window average. Tests:
   - `TestFireIsCompareAndSet` and `TestResolveIsCompareAndSet` (Task 4);
   - `TestTwoEvaluatorsOneEvent` (Task 5);
   - `TestBufferIgnoresSameOrOlderMinute` (Task 1).
2. **Sparse or missing data.** A server that just booted, a host without temperature sensors, an app without a memory limit, or gaps in the buffer must never fire on partial windows. A missing metric means "no decision", never a resolve. Tests:
   - `TestAverageNeedsEightyPercentCoverage` and `TestAppValuesMemoryFallsBackToHost` (Task 1);
   - `TestThresholdKeepsStateWithoutCoverage` (Task 5).
3. **API restart.** Restarting the API while alerts are firing must not re-send "fired" or send a spurious "resolved". Buffers refill from tier-1 rows, so a 10-minute rule can decide within a minute of startup. A down server is counted from the API start, not from a stale `last_seen`. Tests:
   - `TestLoadRestoresFiringAndBuffers` and `TestDownCountsFromStartWhenNeverHeard` (Task 5);
   - `TestTier1SinceReturnsAllTargets` (Task 3).
4. **Secrets in channel URLs.** URLs must never appear in API responses, logs, `last_error` or delivery errors, including Shoutrrr parse errors and send errors that echo the URL. A wrong `JWT_SECRET` after a rotation gives a clear per-channel error, not a panic. Tests:
   - `TestRedactRemovesURLAndSecrets` and `TestValidateErrorIsRedacted` (Task 2);
   - `TestOpenWithAnotherSecretFails` (Task 2);
   - `TestDeliverRecordsDecryptFailure` (Task 6);
   - `TestAlertChannelsNeverReturnURL` (Task 8).

   On the distributed API, a channel aimed at the API's own network (loopback, RFC 1918, cloud metadata) must never be contacted, even through a DNS name. Test:
   - `TestGuardRefusesPrivateTargets` (Task 2).
5. **Channel routing edges:**
   - a rule routed to a deleted channel;
   - `[]` (History only) versus `null` (defaults);
   - disabled channels;
   - no default channels at all;
   - a channel id from another org in a rule or override.

   Expected: deliveries only to existing, enabled channels of the event's org; an empty `deliveries` array otherwise; 400 for a foreign or unknown channel id at save time. Tests:
   - `TestRouteChannels` (Task 1);
   - `TestDeliverSendsToRoutedChannels` (Task 6);
   - `TestAlertRuleRejectsForeignChannel` (Task 8).

---
### Task 1: Domain alert model (catalog, rules, overrides, values, window, messages)

**Files:**
- Create: `internal/domain/alert/catalog.go`, `internal/domain/alert/rule.go`, `internal/domain/alert/model.go`, `internal/domain/alert/values.go`, `internal/domain/alert/window.go`, `internal/domain/alert/message.go`
- Test: `internal/domain/alert/alert_test.go`

**Interfaces:**
- Consumes: `winterflow/internal/domain/metrics` (`Values`, `Tier1`, `UnavailableField`).
- Produces (package `winterflow/internal/domain/alert`):
  - Kinds and metrics:
    - `type TargetKind string` (`TargetServer`, `TargetApp`);
    - `type Metric string` (`MetricDown, MetricCPU, MetricMemory, MetricDisk, MetricLoad1, MetricLoad5, MetricLoad15, MetricBandwidth, MetricTemperature`);
    - `type Unit string` (`UnitNone, UnitPercent, UnitLoad, UnitMBps, UnitCelsius, UnitGB`).
  - Catalog:
    - `type MetricInfo struct{ TargetKind TargetKind; Metric Metric; Label string; Unit Unit; Max float64 }` with `(MetricInfo) HasThreshold() bool`;
    - `func Catalog() []MetricInfo`, `func Lookup(TargetKind, Metric) (MetricInfo, bool)`;
    - `const MinDuration = 1`, `MaxDuration = 60`.
  - Targets: `type Target struct{ ServerID, AppID string }` with `(Target) Kind() TargetKind`; `type Key struct{ RuleID string; Target }`.
  - Rules:
    - `type Rule struct{ ID, OrgID string; TargetKind TargetKind; Metric Metric; Threshold float64; DurationMinutes int; ChannelIDs []string; Enabled bool; CreatedAt time.Time }`;
    - `(*Rule) Normalize()`, `(Rule) Validate() error`, `func DefaultRules() []Rule`.
  - Overrides and settings:
    - `type Override struct{ RuleID, ServerID, AppID string; Disabled bool; Threshold *float64; DurationMinutes *int; ChannelIDs []string }` with `(Override) Key() Key` and `(Override) Validate(Rule) error`;
    - `type Settings struct{ Enabled bool; Threshold float64; Duration time.Duration; ChannelIDs []string }`;
    - `func Effective(Rule, *Override) Settings`.
  - State, events and channels:
    - `type State string` (`StateOK`, `StateFiring`), `type EventKind string` (`EventFired`, `EventResolved`);
    - `type Delivery struct{ ChannelID, ChannelName string; OK bool; Error string }`;
    - `type Event struct{ ID, OrgID, RuleID, ServerID, AppID, ServerName, AppName string; TargetKind TargetKind; Metric Metric; Kind EventKind; Value, Threshold float64; Title, Message string; Deliveries []Delivery; CreatedAt time.Time }`;
    - `type Channel struct{ ID, OrgID, Name, Service string; Fields map[string]string; IsDefault, Enabled bool; LastError string; LastSentAt *time.Time; CreatedAt time.Time; URLEncrypted string }`;
    - `func RouteChannels(all []Channel, ids []string) []Channel`;
    - `type StateRow struct{ Key; TargetKind TargetKind; Metric Metric; State State; Since time.Time; Value, Threshold float64 }`;
    - `type Active struct{ RuleID string; TargetKind TargetKind; Metric Metric; ServerID, AppID string; Since time.Time; Value, Threshold float64 }`;
    - `type Notice struct{ Kind EventKind; RuleID string; TargetKind TargetKind; Metric Metric; ServerID, AppID string; Value, Threshold float64; Title string }`;
    - `type EventQuery struct{ OrgID, ServerID, AppID, RuleID string; Kind EventKind; BeforeAt time.Time; BeforeID string; Limit int }`.
  - Directory: `type ServerRef struct{ ID, OrgID, Name string }`, `type AppRef struct{ ID, ServerID, Name string }`, `type Directory struct{ Servers map[string]ServerRef; Apps map[string]AppRef }`.
  - Errors: `var ErrNotFound = errors.New("alert: not found")`.
  - Values and window:
    - `func ServerValues(metrics.Values) map[Metric]float64`, `func AppValues(v metrics.Values, hostMemTotal float64) map[Metric]float64`;
    - `const BufferSize = 60`, `const Coverage = 0.8`;
    - `type Point struct{ At time.Time; Values map[Metric]float64 }`;
    - `type Buffer struct` with `Add(Point) bool`, `Average(Metric, time.Duration) (float64, bool)`, `Len() int`.
  - Messages:
    - `type Names struct{ Server, App string }`, `type Message struct{ Title, Body string }`;
    - `type Transition struct{ Kind EventKind; Info MetricInfo; Names Names; Value, Threshold float64; Duration time.Duration; Link string }`;
    - `func Compose(Transition) Message`, `func FormatValue(MetricInfo, float64) string`, `func Link(webURL string, t Target) string`.

- [ ] **Step 1: Write the failing test**

`internal/domain/alert/alert_test.go`:

```go
package alert

import (
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/metrics"
)

func f(v float64) *float64 { return &v }
func n(v int) *int         { return &v }

func TestLookupCatalog(t *testing.T) {
	if _, ok := Lookup(TargetServer, MetricTemperature); !ok {
		t.Fatal("server temperature missing")
	}
	if _, ok := Lookup(TargetApp, MetricLoad1); ok {
		t.Fatal("apps have no load metric")
	}
	info, _ := Lookup(TargetApp, MetricDisk)
	if info.Unit != UnitGB || !info.HasThreshold() {
		t.Fatalf("app disk = %+v", info)
	}
	down, _ := Lookup(TargetServer, MetricDown)
	if down.HasThreshold() {
		t.Fatal("down has no threshold")
	}
	if len(Catalog()) != 13 {
		t.Fatalf("catalog size = %d", len(Catalog()))
	}
}

func TestRuleValidate(t *testing.T) {
	ok := []Rule{
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 90, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricLoad5, Threshold: 12.5, DurationMinutes: 1},
		{TargetKind: TargetApp, Metric: MetricDisk, Threshold: 250, DurationMinutes: 60},
		{TargetKind: TargetServer, Metric: MetricDown, DurationMinutes: 5},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []Rule{
		{TargetKind: "host", Metric: MetricCPU, Threshold: 90, DurationMinutes: 10},
		{TargetKind: TargetApp, Metric: MetricLoad1, Threshold: 1, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 0, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 101, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricTemperature, Threshold: 151, DurationMinutes: 10},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 90, DurationMinutes: 0},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 90, DurationMinutes: 61},
	}
	for _, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
	down := Rule{TargetKind: TargetServer, Metric: MetricDown, Threshold: 7, DurationMinutes: 5}
	down.Normalize()
	if down.Threshold != 0 {
		t.Fatalf("down threshold kept: %v", down.Threshold)
	}
}

func TestOverrideValidateAndEffective(t *testing.T) {
	rule := Rule{ID: "r1", TargetKind: TargetApp, Metric: MetricMemory, Threshold: 90, DurationMinutes: 10, Enabled: true}
	if err := (Override{RuleID: "r1", ServerID: "s"}).Validate(rule); err == nil {
		t.Fatal("app rule override without app_id accepted")
	}
	if err := (Override{RuleID: "r1", ServerID: "s", AppID: "a", Threshold: f(120)}).Validate(rule); err == nil {
		t.Fatal("threshold above 100% accepted")
	}
	if err := (Override{RuleID: "r1", ServerID: "s", AppID: "a", DurationMinutes: n(0)}).Validate(rule); err == nil {
		t.Fatal("duration 0 accepted")
	}
	srvRule := Rule{ID: "r2", TargetKind: TargetServer, Metric: MetricDown, DurationMinutes: 5, Enabled: true}
	if err := (Override{RuleID: "r2", ServerID: "s", AppID: "a"}).Validate(srvRule); err == nil {
		t.Fatal("server rule override with app_id accepted")
	}
	if err := (Override{RuleID: "r2", ServerID: "s", Threshold: f(3)}).Validate(srvRule); err == nil {
		t.Fatal("down override with a threshold accepted")
	}

	base := Effective(rule, nil)
	if !base.Enabled || base.Threshold != 90 || base.Duration != 10*time.Minute || base.ChannelIDs != nil {
		t.Fatalf("no override = %+v", base)
	}
	o := Override{RuleID: "r1", ServerID: "s", AppID: "a", Threshold: f(95), ChannelIDs: []string{}}
	got := Effective(rule, &o)
	if got.Threshold != 95 || got.Duration != 10*time.Minute || got.ChannelIDs == nil || len(got.ChannelIDs) != 0 {
		t.Fatalf("override = %+v", got)
	}
	o.Disabled = true
	if Effective(rule, &o).Enabled {
		t.Fatal("disabled override still enabled")
	}
	rule.Enabled = false
	if Effective(rule, nil).Enabled {
		t.Fatal("disabled rule still enabled")
	}
}

func TestDefaultRulesAreValid(t *testing.T) {
	rules := DefaultRules()
	if len(rules) != 5 {
		t.Fatalf("defaults = %d", len(rules))
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil || !r.Enabled || r.ChannelIDs != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
}

func TestServerValues(t *testing.T) {
	got := ServerValues(metrics.Values{
		"cpu": 42, "mem_used": 3e9, "mem_total": 4e9, "disk_pct": 71,
		"load1": 1.5, "load5": 1, "load15": 0.5, "net_rx_bps": 3e6, "net_tx_bps": 1e6, "temp": 64,
	})
	want := map[Metric]float64{
		MetricCPU: 42, MetricMemory: 75, MetricDisk: 71, MetricLoad1: 1.5, MetricLoad5: 1,
		MetricLoad15: 0.5, MetricBandwidth: 4, MetricTemperature: 64,
	}
	for m, v := range want {
		if got[m] != v {
			t.Errorf("%s = %v, want %v", m, got[m], v)
		}
	}
	sparse := ServerValues(metrics.Values{"mem_used": 1, "mem_total": 0, "net_rx_bps": 5})
	for _, m := range []Metric{MetricCPU, MetricMemory, MetricBandwidth, MetricTemperature} {
		if _, ok := sparse[m]; ok {
			t.Errorf("%s present without its inputs", m)
		}
	}
}

func TestAppValuesMemoryFallsBackToHost(t *testing.T) {
	limited := AppValues(metrics.Values{"cpu": 3, "mem_used": 512e6, "mem_limit": 1024e6, "disk": 25e9}, 8e9)
	if limited[MetricMemory] != 50 || limited[MetricCPU] != 3 || limited[MetricDisk] != 25 {
		t.Fatalf("limited = %v", limited)
	}
	host := AppValues(metrics.Values{"mem_used": 2e9}, 8e9)
	if host[MetricMemory] != 25 {
		t.Fatalf("host fallback = %v", host)
	}
	if _, ok := AppValues(metrics.Values{"mem_used": 2e9}, 0)[MetricMemory]; ok {
		t.Fatal("memory without limit or host total")
	}
	if got := AppValues(metrics.Values{metrics.UnavailableField: 1}, 8e9); len(got) != 0 {
		t.Fatalf("unavailable app = %v", got)
	}
}

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func fill(b *Buffer, from time.Time, mins int, v float64) {
	for i := 0; i < mins; i++ {
		b.Add(Point{At: from.Add(time.Duration(i) * time.Minute), Values: map[Metric]float64{MetricCPU: v}})
	}
}

func TestAverageNeedsEightyPercentCoverage(t *testing.T) {
	var b Buffer
	fill(&b, t0, 7, 95) // 12:00..12:06
	if _, ok := b.Average(MetricCPU, 10*time.Minute); ok {
		t.Fatal("7 of 10 samples decided")
	}
	b.Add(Point{At: t0.Add(7 * time.Minute), Values: map[Metric]float64{MetricCPU: 75}})
	avg, ok := b.Average(MetricCPU, 10*time.Minute)
	if !ok || avg != (7*95+75)/8.0 {
		t.Fatalf("8 of 10: avg=%v ok=%v", avg, ok)
	}
	// Only the window counts: a 2-minute window sees 12:06 and 12:07.
	avg, ok = b.Average(MetricCPU, 2*time.Minute)
	if !ok || avg != 85 {
		t.Fatalf("2m: avg=%v ok=%v", avg, ok)
	}
	if _, ok := b.Average(MetricMemory, time.Minute); ok {
		t.Fatal("a metric no point carries decided")
	}
}

func TestBufferIgnoresSameOrOlderMinute(t *testing.T) {
	var b Buffer
	if !b.Add(Point{At: t0.Add(30 * time.Second), Values: map[Metric]float64{MetricCPU: 10}}) {
		t.Fatal("first point rejected")
	}
	if b.Add(Point{At: t0.Add(50 * time.Second), Values: map[Metric]float64{MetricCPU: 99}}) {
		t.Fatal("same minute accepted twice")
	}
	if b.Add(Point{At: t0.Add(-time.Minute), Values: map[Metric]float64{MetricCPU: 99}}) {
		t.Fatal("older minute accepted")
	}
	fill(&b, t0.Add(time.Minute), BufferSize+5, 1)
	if b.Len() != BufferSize {
		t.Fatalf("len = %d", b.Len())
	}
}

func TestRouteChannels(t *testing.T) {
	all := []Channel{
		{ID: "a", IsDefault: true, Enabled: true},
		{ID: "b", IsDefault: true, Enabled: false},
		{ID: "c", Enabled: true},
	}
	ids := func(cs []Channel) string {
		var s []string
		for _, c := range cs {
			s = append(s, c.ID)
		}
		return strings.Join(s, ",")
	}
	if got := ids(RouteChannels(all, nil)); got != "a" {
		t.Errorf("defaults = %q", got)
	}
	if got := ids(RouteChannels(all, []string{})); got != "" {
		t.Errorf("history only = %q", got)
	}
	if got := ids(RouteChannels(all, []string{"c", "b", "gone"})); got != "c" {
		t.Errorf("explicit = %q", got)
	}
}

func TestComposeMessages(t *testing.T) {
	cpu, _ := Lookup(TargetServer, MetricCPU)
	fired := Compose(Transition{Kind: EventFired, Info: cpu, Names: Names{Server: "prod-1"}, Value: 93.4,
		Threshold: 90, Duration: 10 * time.Minute, Link: "https://wf.example/server/s1/monitoring"})
	if fired.Title != "🔥 prod-1 · CPU 93% > 90% for 10m" {
		t.Errorf("fired title = %q", fired.Title)
	}
	if !strings.Contains(fired.Body, "Server: prod-1") || !strings.Contains(fired.Body, "https://wf.example/server/s1/monitoring") {
		t.Errorf("fired body = %q", fired.Body)
	}
	resolved := Compose(Transition{Kind: EventResolved, Info: cpu, Names: Names{Server: "prod-1"}, Value: 41, Threshold: 90, Duration: 10 * time.Minute})
	if resolved.Title != "✅ Resolved · prod-1 · CPU 41%" {
		t.Errorf("resolved title = %q", resolved.Title)
	}
	down, _ := Lookup(TargetApp, MetricDown)
	appDown := Compose(Transition{Kind: EventFired, Info: down, Names: Names{Server: "prod-1", App: "nextcloud"}, Duration: time.Hour})
	if appDown.Title != "🔥 prod-1 / nextcloud is down for 1h" || !strings.Contains(appDown.Body, "App: nextcloud") {
		t.Errorf("app down = %+v", appDown)
	}
	up := Compose(Transition{Kind: EventResolved, Info: down, Names: Names{Server: "prod-1"}})
	if up.Title != "✅ Resolved · prod-1 is back up" {
		t.Errorf("server up title = %q", up.Title)
	}
	disk, _ := Lookup(TargetApp, MetricDisk)
	if got := FormatValue(disk, 14.25); got != "14.2 GB" {
		t.Errorf("disk = %q", got)
	}
}

func TestLink(t *testing.T) {
	if got := Link("https://wf.example/", Target{ServerID: "s1"}); got != "https://wf.example/server/s1/monitoring" {
		t.Errorf("server link = %q", got)
	}
	if got := Link("https://wf.example", Target{ServerID: "s1", AppID: "a1"}); got != "https://wf.example/server/s1/app/a1?tab=metrics" {
		t.Errorf("app link = %q", got)
	}
	if got := Link("", Target{ServerID: "s1"}); got != "" {
		t.Errorf("no WEB_URL = %q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/domain/alert/`
Expected: FAIL (`undefined: Lookup`, …).

- [ ] **Step 3: Write the implementation**

`internal/domain/alert/catalog.go`:

```go
// Package alert is the transport- and storage-agnostic alerting model: the
// metric catalog, org-wide rules with per-target overrides, the values a
// metrics sample yields per alert metric, the window buffer the evaluator
// averages over, and the notification text. Spec: monitoring-and-alerts §3.
package alert

import "slices"

// TargetKind is what a rule watches: a server or an app.
type TargetKind string

const (
	TargetServer TargetKind = "server"
	TargetApp    TargetKind = "app"
)

// Metric is an alertable measurement of a target.
type Metric string

const (
	MetricDown        Metric = "down"
	MetricCPU         Metric = "cpu"
	MetricMemory      Metric = "memory"
	MetricDisk        Metric = "disk"
	MetricLoad1       Metric = "load1"
	MetricLoad5       Metric = "load5"
	MetricLoad15      Metric = "load15"
	MetricBandwidth   Metric = "bandwidth"
	MetricTemperature Metric = "temperature"
)

// Unit says how a metric's values and thresholds read.
type Unit string

const (
	UnitNone    Unit = ""
	UnitPercent Unit = "%"
	UnitLoad    Unit = "load"
	UnitMBps    Unit = "MB/s"
	UnitCelsius Unit = "°C"
	UnitGB      Unit = "GB"
)

// Rule durations are whole minutes in [MinDuration, MaxDuration].
const (
	MinDuration = 1
	MaxDuration = 60
)

// MetricInfo describes one alertable metric of a target kind. Max bounds a
// threshold from above (0 = unbounded); thresholds are always > 0.
type MetricInfo struct {
	TargetKind TargetKind `json:"target_kind"`
	Metric     Metric     `json:"metric"`
	Label      string     `json:"label"`
	Unit       Unit       `json:"unit"`
	Max        float64    `json:"max"`
}

// HasThreshold reports whether the metric compares a value with a
// threshold; "down" has none.
func (m MetricInfo) HasThreshold() bool { return m.Metric != MetricDown }

// catalog is the spec's §3 metric table. Server disk is the fullest
// filesystem in percent; app disk is GB (volumes + app dir + layers).
var catalog = []MetricInfo{
	{TargetServer, MetricDown, "Down", UnitNone, 0},
	{TargetServer, MetricCPU, "CPU", UnitPercent, 100},
	{TargetServer, MetricMemory, "Memory", UnitPercent, 100},
	{TargetServer, MetricDisk, "Disk", UnitPercent, 100},
	{TargetServer, MetricLoad1, "Load 1m", UnitLoad, 0},
	{TargetServer, MetricLoad5, "Load 5m", UnitLoad, 0},
	{TargetServer, MetricLoad15, "Load 15m", UnitLoad, 0},
	{TargetServer, MetricBandwidth, "Bandwidth", UnitMBps, 0},
	{TargetServer, MetricTemperature, "Temperature", UnitCelsius, 150},
	{TargetApp, MetricDown, "Down", UnitNone, 0},
	{TargetApp, MetricCPU, "CPU", UnitPercent, 100},
	{TargetApp, MetricMemory, "Memory", UnitPercent, 100},
	{TargetApp, MetricDisk, "Disk", UnitGB, 0},
}

// Catalog returns every alertable metric, servers first.
func Catalog() []MetricInfo { return slices.Clone(catalog) }

// Lookup returns the catalog entry of a (kind, metric) pair.
func Lookup(kind TargetKind, m Metric) (MetricInfo, bool) {
	for _, info := range catalog {
		if info.TargetKind == kind && info.Metric == m {
			return info, true
		}
	}
	return MetricInfo{}, false
}
```

`internal/domain/alert/rule.go`:

```go
package alert

import (
	"fmt"
	"math"
	"time"
)

// Target is what a rule is evaluated for: a server (AppID "") or one app on
// it, the same addressing as metric_samples.
type Target struct {
	ServerID string `json:"server_id"`
	AppID    string `json:"app_id"`
}

// Kind is the target's kind.
func (t Target) Kind() TargetKind {
	if t.AppID == "" {
		return TargetServer
	}
	return TargetApp
}

// Key addresses one rule's state for one target.
type Key struct {
	RuleID string
	Target
}

// Rule is an org-wide alert rule. ChannelIDs nil routes to the org's
// default channels, an empty list to none (History only), otherwise to
// exactly those channels. Down rules carry Threshold 0.
type Rule struct {
	ID              string     `json:"id"`
	OrgID           string     `json:"-"`
	TargetKind      TargetKind `json:"target_kind"`
	Metric          Metric     `json:"metric"`
	Threshold       float64    `json:"threshold"`
	DurationMinutes int        `json:"duration_minutes"`
	ChannelIDs      []string   `json:"channel_ids"`
	Enabled         bool       `json:"enabled"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Normalize zeroes the threshold of a metric that has none.
func (r *Rule) Normalize() {
	if info, ok := Lookup(r.TargetKind, r.Metric); ok && !info.HasThreshold() {
		r.Threshold = 0
	}
}

// Validate checks the metric exists for the kind, the duration is 1..60
// minutes and the threshold is within the metric's range.
func (r Rule) Validate() error {
	info, ok := Lookup(r.TargetKind, r.Metric)
	if !ok {
		return fmt.Errorf("unknown metric %q for %q targets", r.Metric, r.TargetKind)
	}
	if err := validDuration(r.DurationMinutes); err != nil {
		return err
	}
	if info.HasThreshold() {
		return validThreshold(info, r.Threshold)
	}
	return nil
}

func validDuration(m int) error {
	if m < MinDuration || m > MaxDuration {
		return fmt.Errorf("duration must be %d to %d minutes", MinDuration, MaxDuration)
	}
	return nil
}

func validThreshold(info MetricInfo, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || (info.Max > 0 && v > info.Max) {
		if info.Max > 0 {
			return fmt.Errorf("%s threshold must be above 0 and at most %s", info.Label, FormatValue(info, info.Max))
		}
		return fmt.Errorf("%s threshold must be above 0", info.Label)
	}
	return nil
}

// DefaultRules are seeded once per org (spec §3 "Defaults"): enabled, routed
// to the org's default channels.
func DefaultRules() []Rule {
	return []Rule{
		{TargetKind: TargetServer, Metric: MetricDown, DurationMinutes: 5, Enabled: true},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 90, DurationMinutes: 10, Enabled: true},
		{TargetKind: TargetServer, Metric: MetricMemory, Threshold: 90, DurationMinutes: 10, Enabled: true},
		{TargetKind: TargetServer, Metric: MetricDisk, Threshold: 90, DurationMinutes: 5, Enabled: true},
		{TargetKind: TargetApp, Metric: MetricDown, DurationMinutes: 5, Enabled: true},
	}
}

// Override adjusts one rule for one target. Nil fields inherit the rule's;
// ChannelIDs nil inherits, an empty list silences, a list reroutes.
type Override struct {
	RuleID          string   `json:"rule_id"`
	ServerID        string   `json:"server_id"`
	AppID           string   `json:"app_id"`
	Disabled        bool     `json:"disabled"`
	Threshold       *float64 `json:"threshold"`
	DurationMinutes *int     `json:"duration_minutes"`
	ChannelIDs      []string `json:"channel_ids"`
}

// Key is the override's (rule, target).
func (o Override) Key() Key {
	return Key{RuleID: o.RuleID, Target: Target{ServerID: o.ServerID, AppID: o.AppID}}
}

// Validate checks the override fits its rule: the target kind matches and
// any threshold/duration is in range (down rules take no threshold).
func (o Override) Validate(r Rule) error {
	if o.ServerID == "" {
		return fmt.Errorf("server_id is required")
	}
	if (o.AppID == "") != (r.TargetKind == TargetServer) {
		return fmt.Errorf("a %s rule needs a %s target", r.TargetKind, r.TargetKind)
	}
	info, ok := Lookup(r.TargetKind, r.Metric)
	if !ok {
		return fmt.Errorf("unknown metric %q", r.Metric)
	}
	if o.DurationMinutes != nil {
		if err := validDuration(*o.DurationMinutes); err != nil {
			return err
		}
	}
	if o.Threshold != nil {
		if !info.HasThreshold() {
			return fmt.Errorf("%s has no threshold", info.Label)
		}
		return validThreshold(info, *o.Threshold)
	}
	return nil
}

// Settings are a rule's effective settings for one target.
type Settings struct {
	Enabled    bool
	Threshold  float64
	Duration   time.Duration
	ChannelIDs []string
}

// Effective merges the rule with the target's override (nil: none).
func Effective(r Rule, o *Override) Settings {
	s := Settings{
		Enabled:    r.Enabled,
		Threshold:  r.Threshold,
		Duration:   time.Duration(r.DurationMinutes) * time.Minute,
		ChannelIDs: r.ChannelIDs,
	}
	if o == nil {
		return s
	}
	if o.Disabled {
		s.Enabled = false
	}
	if o.Threshold != nil {
		s.Threshold = *o.Threshold
	}
	if o.DurationMinutes != nil {
		s.Duration = time.Duration(*o.DurationMinutes) * time.Minute
	}
	if o.ChannelIDs != nil {
		s.ChannelIDs = o.ChannelIDs
	}
	return s
}
```

`internal/domain/alert/model.go`:

```go
package alert

import (
	"errors"
	"time"
)

// ErrNotFound is returned for a channel, rule or server that does not exist
// in the caller's org.
var ErrNotFound = errors.New("alert: not found")

// State is a (rule, target)'s alert state.
type State string

const (
	StateOK     State = "ok"
	StateFiring State = "firing"
)

// EventKind is a transition recorded in the history.
type EventKind string

const (
	EventFired    EventKind = "fired"
	EventResolved EventKind = "resolved"
)

// Delivery is one channel's outcome for an event.
type Delivery struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
}

// Event is one history row. Names are captured at event time.
type Event struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"-"`
	RuleID     string     `json:"rule_id"`
	ServerID   string     `json:"server_id"`
	AppID      string     `json:"app_id"`
	ServerName string     `json:"server_name"`
	AppName    string     `json:"app_name"`
	TargetKind TargetKind `json:"target_kind"`
	Metric     Metric     `json:"metric"`
	Kind       EventKind  `json:"kind"`
	Value      float64    `json:"value"`
	Threshold  float64    `json:"threshold"`
	Title      string     `json:"title"`
	Message    string     `json:"message"`
	Deliveries []Delivery `json:"deliveries"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Channel is an org's notification channel. The Shoutrrr URL is stored
// encrypted (URLEncrypted) and never leaves the API; Service is its scheme
// and Fields are the non-secret form values the browser chose to keep.
type Channel struct {
	ID           string            `json:"id"`
	OrgID        string            `json:"-"`
	Name         string            `json:"name"`
	Service      string            `json:"service"`
	Fields       map[string]string `json:"fields"`
	IsDefault    bool              `json:"is_default"`
	Enabled      bool              `json:"enabled"`
	LastError    string            `json:"last_error"`
	LastSentAt   *time.Time        `json:"last_sent_at"`
	CreatedAt    time.Time         `json:"created_at"`
	URLEncrypted string            `json:"-"`
}

// RouteChannels picks the enabled channels a notification goes to: the
// defaults when ids is nil, none when it is empty, else the listed ones
// (unknown ids are ignored). Order follows all.
func RouteChannels(all []Channel, ids []string) []Channel {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []Channel
	for _, c := range all {
		if !c.Enabled {
			continue
		}
		if (ids == nil && c.IsDefault) || want[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// StateRow is a stored state with its rule's kind and metric.
type StateRow struct {
	Key
	TargetKind TargetKind
	Metric     Metric
	State      State
	Since      time.Time
	Value      float64
	Threshold  float64
}

// Active is a firing alert for the badges, strip and cards.
type Active struct {
	RuleID     string     `json:"rule_id"`
	TargetKind TargetKind `json:"target_kind"`
	Metric     Metric     `json:"metric"`
	ServerID   string     `json:"server_id"`
	AppID      string     `json:"app_id"`
	Since      time.Time  `json:"since"`
	Value      float64    `json:"value"`
	Threshold  float64    `json:"threshold"`
}

// Notice is the payload of the SSE "alert" notification.
type Notice struct {
	Kind       EventKind  `json:"kind"`
	RuleID     string     `json:"rule_id"`
	TargetKind TargetKind `json:"target_kind"`
	Metric     Metric     `json:"metric"`
	ServerID   string     `json:"server_id"`
	AppID      string     `json:"app_id"`
	Value      float64    `json:"value"`
	Threshold  float64    `json:"threshold"`
	Title      string     `json:"title"`
}

// EventQuery filters the history (empty fields match all) and pages it
// newest first: rows strictly before (BeforeAt, BeforeID).
type EventQuery struct {
	OrgID    string
	ServerID string
	AppID    string
	RuleID   string
	Kind     EventKind
	BeforeAt time.Time
	BeforeID string
	Limit    int
}

// ServerRef and AppRef are the evaluator's directory of targets.
type ServerRef struct{ ID, OrgID, Name string }

type AppRef struct{ ID, ServerID, Name string }

// Directory is every server and app the API knows, keyed by id.
type Directory struct {
	Servers map[string]ServerRef
	Apps    map[string]AppRef
}
```

`internal/domain/alert/values.go`:

```go
package alert

import "winterflow/internal/domain/metrics"

// ServerValues extracts the server alert metrics from one sample's stored
// values. A metric whose inputs are missing is absent, never zero.
func ServerValues(v metrics.Values) map[Metric]float64 {
	out := map[Metric]float64{}
	copyField(out, v, MetricCPU, "cpu")
	if used, ok := v["mem_used"]; ok {
		if total := v["mem_total"]; total > 0 {
			out[MetricMemory] = 100 * used / total
		}
	}
	copyField(out, v, MetricDisk, "disk_pct")
	copyField(out, v, MetricLoad1, "load1")
	copyField(out, v, MetricLoad5, "load5")
	copyField(out, v, MetricLoad15, "load15")
	rx, okRx := v["net_rx_bps"]
	tx, okTx := v["net_tx_bps"]
	if okRx && okTx {
		out[MetricBandwidth] = (rx + tx) / 1e6
	}
	copyField(out, v, MetricTemperature, "temp")
	return out
}

// AppValues extracts the app alert metrics. Memory is a percentage of the
// container limit, else of the host's memory (hostMemTotal from the same
// sample); disk is GB. An app whose container metrics are unavailable
// yields nothing.
func AppValues(v metrics.Values, hostMemTotal float64) map[Metric]float64 {
	out := map[Metric]float64{}
	if v[metrics.UnavailableField] == 1 {
		return out
	}
	copyField(out, v, MetricCPU, "cpu")
	if used, ok := v["mem_used"]; ok {
		if limit := v["mem_limit"]; limit > 0 {
			out[MetricMemory] = 100 * used / limit
		} else if hostMemTotal > 0 {
			out[MetricMemory] = 100 * used / hostMemTotal
		}
	}
	if disk, ok := v["disk"]; ok {
		out[MetricDisk] = disk / 1e9
	}
	return out
}

func copyField(out map[Metric]float64, v metrics.Values, m Metric, field string) {
	if x, ok := v[field]; ok {
		out[m] = x
	}
}
```

`internal/domain/alert/window.go`:

```go
package alert

import (
	"math"
	"time"

	"winterflow/internal/domain/metrics"
)

const (
	// BufferSize is how many minutes of points a target keeps: the
	// longest rule window.
	BufferSize = MaxDuration
	// Coverage is the share of a window's expected one-per-minute points
	// that must exist before its average counts.
	Coverage = 0.8
)

// Point is one sample's alert values, stamped with its minute.
type Point struct {
	At     time.Time
	Values map[Metric]float64
}

// Buffer holds a target's newest points, oldest first. Not safe for
// concurrent use; the evaluator guards it.
type Buffer struct {
	points []Point
}

// Add appends p, stamped with its minute. A minute that is not newer than
// the last one (a replay, a refill overlap) is ignored and Add reports
// false.
func (b *Buffer) Add(p Point) bool {
	p.At = metrics.Tier1.BucketStart(p.At)
	if n := len(b.points); n > 0 && !p.At.After(b.points[n-1].At) {
		return false
	}
	b.points = append(b.points, p)
	if over := len(b.points) - BufferSize; over > 0 {
		b.points = append(b.points[:0:0], b.points[over:]...)
	}
	return true
}

// Len is the number of points held.
func (b *Buffer) Len() int { return len(b.points) }

// Average is m's mean over the points newer than (newest - window). It
// counts only when at least Coverage of the window's expected points carry
// m; ok is false otherwise (no decision).
func (b *Buffer) Average(m Metric, window time.Duration) (avg float64, ok bool) {
	if len(b.points) == 0 || window <= 0 {
		return 0, false
	}
	expected := int(window / time.Minute)
	need := int(math.Ceil(Coverage * float64(expected)))
	if need < 1 {
		need = 1
	}
	cutoff := b.points[len(b.points)-1].At.Add(-window)
	var sum float64
	var n int
	for i := len(b.points) - 1; i >= 0; i-- {
		p := b.points[i]
		if !p.At.After(cutoff) {
			break
		}
		if x, has := p.Values[m]; has {
			sum += x
			n++
		}
	}
	if n < need {
		return 0, false
	}
	return sum / float64(n), true
}
```

`internal/domain/alert/message.go`:

```go
package alert

import (
	"fmt"
	"strings"
	"time"
)

// Names are the human names of a target.
type Names struct{ Server, App string }

func (n Names) label() string {
	if n.App == "" {
		return n.Server
	}
	return n.Server + " / " + n.App
}

// Message is a notification's title and body.
type Message struct{ Title, Body string }

// Transition is what a notification describes.
type Transition struct {
	Kind      EventKind
	Info      MetricInfo
	Names     Names
	Value     float64
	Threshold float64
	Duration  time.Duration
	Link      string
}

// FormatValue renders v in the metric's unit ("93%", "2.31", "12.4 MB/s",
// "71 °C", "14.2 GB").
func FormatValue(info MetricInfo, v float64) string {
	switch info.Unit {
	case UnitPercent:
		return fmt.Sprintf("%.0f%%", v)
	case UnitLoad:
		return fmt.Sprintf("%.2f", v)
	case UnitMBps:
		return fmt.Sprintf("%.1f MB/s", v)
	case UnitCelsius:
		return fmt.Sprintf("%.0f °C", v)
	case UnitGB:
		return fmt.Sprintf("%.1f GB", v)
	}
	return fmt.Sprintf("%g", v)
}

// shortDuration renders whole minutes as "10m", whole hours as "1h".
func shortDuration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// Compose builds the notification text (spec §3 "Delivery"):
// "🔥 prod-1 · CPU 93% > 90% for 10m" / "✅ Resolved · prod-1 · CPU 41%".
func Compose(t Transition) Message {
	target := t.Names.label()
	var title, line string
	switch {
	case !t.Info.HasThreshold() && t.Kind == EventFired:
		title = fmt.Sprintf("🔥 %s is down for %s", target, shortDuration(t.Duration))
		if t.Info.TargetKind == TargetServer {
			line = fmt.Sprintf("The server has not reported for %d min.", int(t.Duration/time.Minute))
		} else {
			line = fmt.Sprintf("The app's containers have been failing (crashed, restarting or unhealthy) for %d min.", int(t.Duration/time.Minute))
		}
	case !t.Info.HasThreshold():
		title = fmt.Sprintf("✅ Resolved · %s is back up", target)
		line = "It is reporting normally again."
	case t.Kind == EventFired:
		title = fmt.Sprintf("🔥 %s · %s %s > %s for %s", target, t.Info.Label,
			FormatValue(t.Info, t.Value), FormatValue(t.Info, t.Threshold), shortDuration(t.Duration))
		line = fmt.Sprintf("%s averaged %s over %d min (threshold %s).", t.Info.Label,
			FormatValue(t.Info, t.Value), int(t.Duration/time.Minute), FormatValue(t.Info, t.Threshold))
	default:
		title = fmt.Sprintf("✅ Resolved · %s · %s %s", target, t.Info.Label, FormatValue(t.Info, t.Value))
		line = fmt.Sprintf("%s is back to %s (threshold %s).", t.Info.Label,
			FormatValue(t.Info, t.Value), FormatValue(t.Info, t.Threshold))
	}
	lines := []string{"Server: " + t.Names.Server}
	if t.Names.App != "" {
		lines = append(lines, "App: "+t.Names.App)
	}
	lines = append(lines, line)
	if t.Link != "" {
		lines = append(lines, t.Link)
	}
	return Message{Title: title, Body: strings.Join(lines, "\n")}
}

// Link is the web page of a target: the server's Monitoring page or the
// app's Metrics tab, under WEB_URL. Empty without WEB_URL.
func Link(webURL string, t Target) string {
	base := strings.TrimRight(webURL, "/")
	if base == "" {
		return ""
	}
	if t.AppID == "" {
		return base + "/server/" + t.ServerID + "/monitoring"
	}
	return base + "/server/" + t.ServerID + "/app/" + t.AppID + "?tab=metrics"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/domain/alert/ && go vet ./internal/domain/alert/`
Expected: `ok  winterflow/internal/domain/alert`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/alert
git commit -m "feat(alerts): domain model for rules, overrides, windows and messages

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 2: Channel URL encryption and the guarded Shoutrrr sender

**Files:**
- Modify: `go.mod`, `go.sum` (add `github.com/nicholas-fedor/shoutrrr v0.18.0`)
- Create: `pkg/secretbox/secretbox.go`, `internal/infra/notify/shoutrrr.go`, `internal/infra/notify/guard.go`
- Modify: `pkg/config/config.go` (`AllowsPrivateAlertTargets`), `.env.example` (`ALERTS_ALLOW_PRIVATE_TARGETS`)
- Test:
  - `pkg/secretbox/secretbox_test.go`;
  - `internal/infra/notify/shoutrrr_test.go`, `internal/infra/notify/guard_test.go`;
  - `pkg/config/config_test.go`.

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - package `winterflow/pkg/secretbox`:
    - `func New(secret string) (*Box, error)` (returns `ErrNoSecret` on `""`);
    - `(*Box).Seal(plain string) (string, error)`;
    - `(*Box).Open(sealed string) (string, error)` (returns `ErrCorrupt` on any failure, including a rotated secret).
  - package `winterflow/internal/infra/notify`:
    - `func NewShoutrrr(timeout time.Duration, allowPrivate bool) *Shoutrrr`;
    - `(*Shoutrrr).Send(ctx context.Context, rawURL, title, body string) error`;
    - `func Service(rawURL string) (string, error)`: the scheme without `+transport`, after Shoutrrr accepted the URL;
    - `func Redact(msg, rawURL string) string`, `func Private(net.IP) bool`, `var ErrPrivateTarget`.
  - `(*config.ServerConfig).AllowsPrivateAlertTargets() bool`: `ALERTS_ALLOW_PRIVATE_TARGETS` true/false. The default is true in standalone and false otherwise.

Verified on this repository:
- `go get github.com/nicholas-fedor/shoutrrr@v0.18.0` keeps `go 1.25.3`. It bumps a few indirect `golang.org/x/*` modules and protobuf by minor versions, and `go build ./...` still passes. Do **not** take v0.19+ (they need Go 1.27).
- The generic service with `template=json` posts `{"title": …, "message": …}`, which `TestSendPostsTitleAndBody` relies on.
- All seven HTTP services the form offers (telegram, discord, slack, ntfy, gotify, pushover, generic) accept the injected HTTP client (`types.HTTPClientSetter`).
- Mutation check: not injecting the guarded client fails `TestGuardRefusesPrivateTargets`.

- [ ] **Step 1: Add the dependency**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp GOTOOLCHAIN=local go get github.com/nicholas-fedor/shoutrrr@v0.18.0
grep -E '^go ' go.mod   # must still print: go 1.25.3
```

- [ ] **Step 2: Write the failing tests**

`pkg/secretbox/secretbox_test.go`:

```go
package secretbox

import (
	"errors"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := New("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	url := "telegram://123:ABC@telegram?chats=@ops"
	sealed, err := b.Seal(url)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "telegram") {
		t.Fatalf("plaintext visible: %s", sealed)
	}
	again, _ := b.Seal(url)
	if again == sealed {
		t.Fatal("same nonce twice")
	}
	got, err := b.Open(sealed)
	if err != nil || got != url {
		t.Fatalf("open = %q, %v", got, err)
	}
}

func TestOpenWithAnotherSecretFails(t *testing.T) {
	a, _ := New("one")
	b, _ := New("two")
	sealed, _ := a.Seal("ntfy://ntfy.sh/topic")
	if _, err := b.Open(sealed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{"", "not base64!", "QUJD"} {
		if _, err := a.Open(bad); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestNewRejectsEmptySecret(t *testing.T) {
	if _, err := New(""); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("err = %v", err)
	}
}
```

`internal/infra/notify/shoutrrr_test.go`:

```go
package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServiceValidates(t *testing.T) {
	cases := map[string]string{
		"telegram://123456:ABCdef@telegram?chats=@ops": "telegram",
		"generic+https://hooks.example.com/x":          "generic",
		"ntfy://ntfy.sh/topic":                         "ntfy",
	}
	for raw, want := range cases {
		got, err := Service(raw)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "not a url", "bogus://x", strings.Repeat("a", 3000)} {
		if _, err := Service(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
}

func TestValidateErrorIsRedacted(t *testing.T) {
	raw := "slack://hook:SECRETTOKEN123/B/X"
	_, err := Service(raw)
	if err == nil {
		t.Fatal("malformed slack URL accepted")
	}
	if strings.Contains(err.Error(), "SECRETTOKEN123") {
		t.Fatalf("secret leaked: %v", err)
	}
}

func TestRedactRemovesURLAndSecrets(t *testing.T) {
	raw := "smtp://mailer:hunter2hunter2@mail.example.com:587/?fromaddress=a@b.c&toaddresses=ops@example.com"
	msg := "dial " + raw + " failed for mailer:hunter2hunter2 and ops@example.com on port 587"
	got := Redact(msg, raw)
	for _, leak := range []string{raw, "hunter2hunter2", "ops@example.com"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked in %q", leak, got)
		}
	}
	if !strings.Contains(got, "587") {
		t.Errorf("short parts should stay: %q", got)
	}
}

func TestSendPostsTitleAndBody(t *testing.T) {
	got := make(chan map[string]string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(b, &m)
		got <- m
	}))
	defer srv.Close()
	raw := "generic://" + strings.TrimPrefix(srv.URL, "http://") + "/hook?template=json&disabletls=yes"
	if err := NewShoutrrr(5*time.Second, true).Send(context.Background(), raw, "T", "B"); err != nil {
		t.Fatal(err)
	}
	m := <-got
	if m["title"] != "T" || m["message"] != "B" {
		t.Fatalf("payload = %v", m)
	}
}

func TestSendTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	raw := "generic://" + strings.TrimPrefix(srv.URL, "http://") + "/hook?disabletls=yes"
	start := time.Now()
	err := NewShoutrrr(200*time.Millisecond, true).Send(context.Background(), raw, "T", "B")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}

func TestSendStopsOnContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	raw := "generic://" + strings.TrimPrefix(srv.URL, "http://") + "/hook?disabletls=yes"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := NewShoutrrr(time.Minute, true).Send(ctx, raw, "T", "B"); err == nil {
		t.Fatal("no error after ctx ended")
	}
}

func TestSendErrorIsRedacted(t *testing.T) {
	raw := "generic://127.0.0.1:9/hook-SECRETPATH?disabletls=yes"
	err := NewShoutrrr(2*time.Second, true).Send(context.Background(), raw, "T", "B")
	if err == nil {
		t.Fatal("send to a closed port succeeded")
	}
	if strings.Contains(err.Error(), "SECRETPATH") {
		t.Fatalf("secret leaked: %v", err)
	}
}
```

`internal/infra/notify/guard_test.go`:

```go
package notify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrivate(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.10", "169.254.169.254",
		"100.64.0.1", "0.0.0.0", "::1", "fd00::1", "fe80::1", "224.0.0.1"} {
		if !Private(net.ParseIP(s)) {
			t.Errorf("%s not private", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "149.154.167.220", "2606:4700:4700::1111", "100.128.0.1"} {
		if Private(net.ParseIP(s)) {
			t.Errorf("%s private", s)
		}
	}
}

// With private targets forbidden, an HTTP service never connects to a
// loopback server, and a non-HTTP one is refused before dialing.
func TestGuardRefusesPrivateTargets(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	guarded := NewShoutrrr(2*time.Second, false)
	ctx := context.Background()

	raw := "generic://" + strings.TrimPrefix(srv.URL, "http://") + "/hook?disabletls=yes"
	if err := guarded.Send(ctx, raw, "T", "B"); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("generic to loopback: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("the guarded client reached a loopback server")
	}
	if err := guarded.Send(ctx, "smtp://u:p@127.0.0.1:25/?fromaddress=a@b.c&toaddresses=d@e.f", "T", "B"); err == nil ||
		!strings.Contains(err.Error(), "private") {
		t.Fatalf("smtp to loopback: %v", err)
	}
	if err := checkHost(ctx, "smtp://u:p@10.0.0.5:25/"); !errors.Is(err, ErrPrivateTarget) {
		t.Fatalf("smtp to RFC 1918: %v", err)
	}
	if err := checkHost(ctx, "smtp://u:p@203.0.113.7:25/"); err != nil {
		t.Fatalf("public smtp refused: %v", err)
	}
	// HTTP services are checked at dial time, not by their URL host
	// (telegram://token@telegram has no real host).
	if err := checkHost(ctx, "telegram://1:a@telegram?chats=1"); err != nil {
		t.Fatalf("telegram refused: %v", err)
	}
	// Allowed (standalone): the same loopback send goes through.
	if err := NewShoutrrr(2*time.Second, true).Send(ctx, raw, "T", "B"); err != nil || hits.Load() != 1 {
		t.Fatalf("allowed send: err=%v hits=%d", err, hits.Load())
	}
}
```

Append to `pkg/config/config_test.go` (its `unsetenv` helper exists):

```go
func TestAllowsPrivateAlertTargets(t *testing.T) {
	standalone, api := NewServerConfig("standalone"), NewServerConfig("distributed")
	unsetenv(t, "ALERTS_ALLOW_PRIVATE_TARGETS")
	if !standalone.AllowsPrivateAlertTargets() || api.AllowsPrivateAlertTargets() {
		t.Fatal("defaults: standalone allows, distributed refuses")
	}
	t.Setenv("ALERTS_ALLOW_PRIVATE_TARGETS", "true")
	if !api.AllowsPrivateAlertTargets() {
		t.Error("true did not allow on the distributed API")
	}
	t.Setenv("ALERTS_ALLOW_PRIVATE_TARGETS", "off")
	if standalone.AllowsPrivateAlertTargets() {
		t.Error("off did not refuse in standalone")
	}
	t.Setenv("ALERTS_ALLOW_PRIVATE_TARGETS", "junk")
	if !standalone.AllowsPrivateAlertTargets() || api.AllowsPrivateAlertTargets() {
		t.Error("an unknown value must keep the topology default")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./pkg/secretbox/ ./internal/infra/notify/ ./pkg/config/`
Expected: FAIL (`undefined: New`, `undefined: Service`, `undefined: Private`, `AllowsPrivateAlertTargets undefined`).

- [ ] **Step 4: Write the implementation**

`pkg/secretbox/secretbox.go`:

```go
// Package secretbox encrypts small secrets at rest (notification channel
// URLs) with AES-256-GCM under a key derived from a server secret
// (JWT_SECRET) by HKDF-SHA256. Rotating the server secret makes existing
// ciphertexts unreadable: they must be re-entered.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// info separates this key from any other use of the same server secret.
const info = "winterflow notification channel urls v1"

var (
	// ErrNoSecret is returned by New for an empty server secret.
	ErrNoSecret = errors.New("secretbox: empty secret")
	// ErrCorrupt is returned by Open for a ciphertext it cannot read:
	// tampered, truncated, or sealed under another secret.
	ErrCorrupt = errors.New("cannot decrypt the stored URL (was JWT_SECRET changed?); re-enter it")
)

// Box seals and opens strings. Safe for concurrent use.
type Box struct {
	aead cipher.AEAD
}

// New derives the box key from secret.
func New(secret string) (*Box, error) {
	if secret == "" {
		return nil, ErrNoSecret
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, info, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plain into base64(nonce || ciphertext).
func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open decrypts what Seal produced.
func (b *Box) Open(sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < b.aead.NonceSize()+b.aead.Overhead() {
		return "", ErrCorrupt
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", ErrCorrupt
	}
	return string(plain), nil
}
```

`internal/infra/notify/shoutrrr.go`:

```go
// Package notify delivers alert notifications through Shoutrrr service
// URLs (telegram://, discord://, ntfy://, smtp://, generic://, ...). URLs
// carry secrets: every error leaving this package is redacted.
package notify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

// maxURLLength bounds a channel URL.
const maxURLLength = 2048

// Shoutrrr sends through Shoutrrr with a per-send timeout.
type Shoutrrr struct {
	timeout time.Duration
	// client is the guarded HTTP client; nil when private targets are
	// allowed (Shoutrrr's own client is used).
	client *http.Client
}

// NewShoutrrr builds a sender whose sends give up after timeout. Unless
// allowPrivate, sends to loopback, private and link-local addresses fail
// with ErrPrivateTarget (the distributed API must not reach its own
// network on a tenant's behalf).
func NewShoutrrr(timeout time.Duration, allowPrivate bool) *Shoutrrr {
	s := &Shoutrrr{timeout: timeout}
	if !allowPrivate {
		s.client = guardedClient(timeout)
	}
	return s
}

// Service validates rawURL as a Shoutrrr URL and returns its service name:
// the scheme without a "+transport" suffix ("generic+https" -> "generic").
func Service(rawURL string) (string, error) {
	if rawURL == "" || len(rawURL) > maxURLLength {
		return "", errors.New("the notification URL is empty or too long")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return "", errors.New("the notification URL is not a valid URL")
	}
	if _, err := shoutrrr.CreateSenderWithOptions(types.SenderOptions{}, rawURL); err != nil {
		return "", fmt.Errorf("invalid %s URL: %s", u.Scheme, Redact(err.Error(), rawURL))
	}
	return strings.SplitN(strings.ToLower(u.Scheme), "+", 2)[0], nil
}

// Send delivers one notification. It returns when the service answers, the
// sender's timeout passes or ctx ends, whichever is first.
func (s *Shoutrrr) Send(ctx context.Context, rawURL, title, body string) error {
	opts := types.SenderOptions{Timeout: s.timeout}
	if s.client != nil {
		if err := checkHost(ctx, rawURL); err != nil {
			return errors.New(Redact(err.Error(), rawURL))
		}
		opts.HTTPClient = s.client
	}
	r, err := shoutrrr.CreateSenderWithOptions(opts, rawURL)
	if err != nil {
		return errors.New(Redact(err.Error(), rawURL))
	}
	done := make(chan error, 1)
	go func() {
		for _, e := range r.Send(body, &types.Params{"title": title}) {
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			return errors.New(Redact(err.Error(), rawURL))
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("send aborted: %w", ctx.Err())
	}
}

// minSecretLen is the shortest URL part Redact masks; shorter parts (a
// port, "api") are not secrets and masking them would garble messages.
const minSecretLen = 6

// Redact removes rawURL and its secret-looking parts (user info, path
// segments, query values) from msg.
func Redact(msg, rawURL string) string {
	if rawURL == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, rawURL, "<url>")
	u, err := url.Parse(rawURL)
	if err != nil {
		return msg
	}
	var parts []string
	if u.User != nil {
		parts = append(parts, u.User.String(), u.User.Username())
		if p, ok := u.User.Password(); ok {
			parts = append(parts, p)
		}
	}
	parts = append(parts, strings.Split(u.Path, "/")...)
	for _, vs := range u.Query() {
		parts = append(parts, vs...)
	}
	for _, p := range parts {
		if len(p) >= minSecretLen {
			msg = strings.ReplaceAll(msg, p, "***")
		}
	}
	return msg
}
```

`internal/infra/notify/guard.go`:

```go
package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrPrivateTarget is returned for a send to a loopback, private,
// link-local or otherwise internal address when the server forbids them.
var ErrPrivateTarget = errors.New("the notification target is a private, loopback or link-local address, which this server does not allow")

// cgnat is the shared address space (RFC 6598), internal like RFC 1918.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// Private reports whether ip is not a public unicast address: loopback,
// RFC 1918 / ULA, link-local (cloud metadata lives there), CGNAT,
// multicast or unspecified.
func Private(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

// httpServices are the Shoutrrr schemes that send through the injected
// HTTP client, whose dialer refuses private addresses at connect time (no
// DNS-rebinding window). Every other scheme (smtp, mqtt, xmpp, …) dials
// on its own and is checked by resolving its host first.
var httpServices = map[string]bool{
	"bark": true, "discord": true, "generic": true, "googlechat": true, "gotify": true, "join": true,
	"lark": true, "matrix": true, "mattermost": true, "ntfy": true, "opsgenie": true, "pagerduty": true,
	"pushover": true, "rocketchat": true, "signal": true, "slack": true, "teams": true, "telegram": true,
	"twilio": true, "wecom": true, "zulip": true,
}

// guardedClient is an HTTP client whose connections to private addresses
// fail. It ignores proxy settings: a proxy would hide the destination.
func guardedClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || Private(ip) {
				return ErrPrivateTarget
			}
			return nil
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext
	return &http.Client{Transport: transport, Timeout: timeout}
}

// checkHost resolves the host of a non-HTTP service URL and refuses
// private addresses. HTTP services are guarded at dial time instead.
func checkHost(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("the notification URL is not a valid URL")
	}
	scheme := strings.SplitN(strings.ToLower(u.Scheme), "+", 2)[0]
	host := u.Hostname()
	if httpServices[scheme] || host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if Private(ip) {
			return ErrPrivateTarget
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("cannot resolve %s", host)
	}
	for _, a := range addrs {
		if Private(a.IP) {
			return ErrPrivateTarget
		}
	}
	return nil
}
```

Add to `pkg/config/config.go`, before `minMetricsDiskInterval`:

```go
// AllowsPrivateAlertTargets reports whether alert notifications may go to
// loopback, private and link-local addresses (a LAN ntfy, Gotify or mail
// server). Default: allowed in standalone (the box's own network is the
// point), refused by the distributed API, whose self-registered orgs must
// not reach its internal network. ALERTS_ALLOW_PRIVATE_TARGETS=true/false
// overrides either default.
func (c *ServerConfig) AllowsPrivateAlertTargets() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ALERTS_ALLOW_PRIVATE_TARGETS"))) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return c.IsStandalone()
	}
}
```

Add to `.env.example`, after the `METRICS_DISK_INTERVAL` block:

```
# --- Alerts (API/standalone) --------------------------------------------------

# Whether alert notifications may be sent to loopback, private (RFC 1918,
# ULA), link-local (incl. cloud metadata) and CGNAT addresses: a LAN ntfy,
# Gotify, webhook or mail server. Default: true in standalone, false on the
# distributed API (self-registered orgs must not reach its network). HTTP
# services are checked when connecting; others (smtp, …) by resolving the
# host first.
#ALERTS_ALLOW_PRIVATE_TARGETS=
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go mod tidy && GOTMPDIR=$HOME/.cache/wf-gotmp go test ./pkg/secretbox/ ./internal/infra/notify/ ./pkg/config/ && GOTMPDIR=$HOME/.cache/wf-gotmp go build ./...`
Expected: all three packages `ok`; the build succeeds. `go mod tidy` must leave `github.com/nicholas-fedor/shoutrrr v0.18.0` in the main `require` block.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum pkg/secretbox pkg/config internal/infra/notify .env.example
git commit -m "feat(alerts): encrypted channel URLs and a guarded, redacting Shoutrrr sender

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 3: Migration 0012, queries and the alert repository

**Files:**
- Create: `internal/infra/db/migrations/sqlite/0012_alerts.sql`, `internal/infra/db/migrations/pgsql/0012_alerts.sql`
- Create: `internal/infra/db/sqlc/query/sqlite/alerts.sql`, `internal/infra/db/sqlc/query/pgsql/alerts.sql`
- Modify: `internal/infra/db/sqlc/sqlc.yaml` (nullable Postgres `int4`/`float8`), `internal/infra/db/sqlc/shapes_test.go`
- Generated (by `make sqlc`, never by hand): `internal/infra/db/sqlc/gen/{sqlite,pgsql}/alerts.sql.go`, `models.go`, `querier.go`, `organizations.sql.go`
- Create: `internal/infra/db/repository/alert/repo.go`, `sqlite.go`, `pgsql.go`, `channels.go`, `rules.go`, `states.go`, `events.go`
- Test: `internal/infra/db/repository/alert/repo_test.go`, `internal/infra/db/repository/alert/states_test.go`
- Modify: `internal/infra/db/repository/timestamps_test.go`, which must write every new `TIMESTAMP` column. This is why the whole repository is one task.

**Interfaces:**
- Consumes: package `alert` from Task 1 (all model types, `ErrNotFound`, `DefaultRules`).
- Produces (package `winterflow/internal/infra/db/repository/alert`; import it as `alertrepo`):
  - Construction and constants:
    - `func New(tx *backend.TxManager, driver string, log *logger.Logger, newID func() string) (*Repo, error)`;
    - constants `EventRetention = 90*24h`, `EventsPerOrg = 2000`, `MaxEventsPage = 200`.
  - Channels:
    - `ListChannels(ctx, orgID) ([]alert.Channel, error)`;
    - `GetChannel(ctx, orgID, id) (alert.Channel, error)`;
    - `CreateChannel(ctx, alert.Channel) (alert.Channel, error)`;
    - `UpdateChannel(ctx, alert.Channel) error`;
    - `DeleteChannel(ctx, orgID, id) error`;
    - `RecordDelivery(ctx, channelID string, at time.Time, sendErr string) error`.
  - Rules:
    - `ListRules(ctx, orgID) ([]alert.Rule, error)`;
    - `GetRule(ctx, orgID, id) (alert.Rule, error)`;
    - `CreateRule(ctx, alert.Rule) (alert.Rule, error)`;
    - `UpdateRule(ctx, alert.Rule) error`;
    - `DeleteRule(ctx, orgID, id) error`;
    - `SeedDefaults(ctx) (int, error)`.
  - Overrides:
    - `ServerOrgID(ctx, serverID) (string, error)`;
    - `ListOverrides(ctx, orgID) ([]alert.Override, error)`;
    - `TargetOverrides(ctx, alert.Target) ([]alert.Override, error)`;
    - `SaveOverride(ctx, alert.Override) error`;
    - `DeleteOverride(ctx, alert.Key) error`.
  - States:
    - `Fire(ctx, alert.Key, value, threshold float64, alert.Event) (bool, error)`;
    - `Resolve(ctx, alert.Key, value float64, alert.Event) (bool, error)`;
    - `ClearState(ctx, alert.Key) error`;
    - `FiringStates(ctx) ([]alert.StateRow, error)`;
    - `ListActive(ctx, orgID) ([]alert.Active, error)`;
    - `CountFiring(ctx) (map[string]int, error)`.
  - Events and directory:
    - `ListEvents(ctx, alert.EventQuery) ([]alert.Event, error)`;
    - `SetDeliveries(ctx, eventID string, []alert.Delivery) error`;
    - `PruneEvents(ctx, now time.Time) (int64, error)`;
    - `Directory(ctx) (alert.Directory, error)`.
  - The sqlc queries `DeleteAlertOverridesByApp`, `DeleteAlertStatesByApp` and `DeleteAlertEventsByApp`, which Task 4 uses from the app repository.

Notes from prototyping on SQLite and Postgres 16:
- sqlc cannot bind a parameter inside `HAVING` (on SQLite it leaves `sqlc.arg(keep)` in the SQL text). The history cap therefore appears as the literal `2000` in `ListOversizedAlertOrganizations`, next to the `EventsPerOrg` constant.
- sqlc cannot analyse a `DELETE … WHERE id NOT IN (SELECT … FROM same_table …)`, so the trim is two queries: `GetAlertEventTrimCutoff` and `DeleteAlertEventsUpTo`.
- Postgres `LIMIT`/`OFFSET` parameters need `::bigint` so their Go type is `int64` on both engines. UUID columns compared with an optional text filter need `column::text`.
- SQLite reports 0 affected rows for an `ON CONFLICT DO UPDATE … WHERE` that does not match, as Postgres does. `TestFireIsCompareAndSet` pins this.

- [ ] **Step 1: Write the migrations**

`internal/infra/db/migrations/sqlite/0012_alerts.sql`:

```sql
-- Alerts (monitoring PR 2): org-wide notification channels and rules,
-- per-target overrides, the live state of each (rule, target) and the
-- event history. A target is (server_id, app_id) with app_id '' for the
-- server itself, as in metric_samples. Channel URLs carry secrets and are
-- stored AES-GCM encrypted (key derived from JWT_SECRET).

ALTER TABLE organizations ADD COLUMN alert_defaults_seeded BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE notification_channels (
    channel_id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    name VARCHAR(64) NOT NULL,
    service VARCHAR(32) NOT NULL,
    fields TEXT NOT NULL DEFAULT '{}',
    url_encrypted TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_error TEXT NOT NULL DEFAULT '',
    last_sent_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE INDEX idx_notification_channels_org ON notification_channels (organization_id);

CREATE TABLE alert_rules (
    rule_id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    target_kind VARCHAR(16) NOT NULL,
    metric VARCHAR(32) NOT NULL,
    threshold REAL NOT NULL DEFAULT 0,
    duration_minutes INTEGER NOT NULL,
    channel_ids TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE INDEX idx_alert_rules_org ON alert_rules (organization_id);

CREATE TABLE alert_overrides (
    rule_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    threshold REAL,
    duration_minutes INTEGER,
    channel_ids TEXT,
    PRIMARY KEY (rule_id, server_id, app_id),
    FOREIGN KEY (rule_id) REFERENCES alert_rules (rule_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE alert_states (
    rule_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    state VARCHAR(8) NOT NULL,
    since TIMESTAMP NOT NULL,
    value REAL NOT NULL DEFAULT 0,
    threshold REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (rule_id, server_id, app_id),
    FOREIGN KEY (rule_id) REFERENCES alert_rules (rule_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE alert_events (
    event_id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    rule_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    server_name VARCHAR(64) NOT NULL DEFAULT '',
    app_name VARCHAR(255) NOT NULL DEFAULT '',
    target_kind VARCHAR(16) NOT NULL,
    metric VARCHAR(32) NOT NULL,
    kind VARCHAR(8) NOT NULL,
    value REAL NOT NULL DEFAULT 0,
    threshold REAL NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    message TEXT NOT NULL,
    deliveries TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_alert_events_org_created ON alert_events (organization_id, created_at);
CREATE INDEX idx_alert_events_created ON alert_events (created_at);
```

`internal/infra/db/migrations/pgsql/0012_alerts.sql`:

```sql
-- Alerts (monitoring PR 2): org-wide notification channels and rules,
-- per-target overrides, the live state of each (rule, target) and the
-- event history. A target is (server_id, app_id) with app_id '' for the
-- server itself, as in metric_samples. Channel URLs carry secrets and are
-- stored AES-GCM encrypted (key derived from JWT_SECRET).

ALTER TABLE organizations ADD COLUMN alert_defaults_seeded BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE notification_channels (
    channel_id UUID PRIMARY KEY,
    organization_id UUID NOT NULL,
    name VARCHAR(64) NOT NULL,
    service VARCHAR(32) NOT NULL,
    fields TEXT NOT NULL DEFAULT '{}',
    url_encrypted TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_error TEXT NOT NULL DEFAULT '',
    last_sent_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE INDEX idx_notification_channels_org ON notification_channels (organization_id);

CREATE TABLE alert_rules (
    rule_id UUID PRIMARY KEY,
    organization_id UUID NOT NULL,
    target_kind VARCHAR(16) NOT NULL,
    metric VARCHAR(32) NOT NULL,
    threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_minutes INTEGER NOT NULL,
    channel_ids TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE INDEX idx_alert_rules_org ON alert_rules (organization_id);

CREATE TABLE alert_overrides (
    rule_id UUID NOT NULL,
    server_id UUID NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    threshold DOUBLE PRECISION,
    duration_minutes INTEGER,
    channel_ids TEXT,
    PRIMARY KEY (rule_id, server_id, app_id),
    FOREIGN KEY (rule_id) REFERENCES alert_rules (rule_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE alert_states (
    rule_id UUID NOT NULL,
    server_id UUID NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    state VARCHAR(8) NOT NULL,
    since TIMESTAMP NOT NULL,
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (rule_id, server_id, app_id),
    FOREIGN KEY (rule_id) REFERENCES alert_rules (rule_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE alert_events (
    event_id VARCHAR(36) PRIMARY KEY,
    organization_id UUID NOT NULL,
    rule_id UUID NOT NULL,
    server_id UUID NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    server_name VARCHAR(64) NOT NULL DEFAULT '',
    app_name VARCHAR(255) NOT NULL DEFAULT '',
    target_kind VARCHAR(16) NOT NULL,
    metric VARCHAR(32) NOT NULL,
    kind VARCHAR(8) NOT NULL,
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    message TEXT NOT NULL,
    deliveries TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_alert_events_org_created ON alert_events (organization_id, created_at);
CREATE INDEX idx_alert_events_created ON alert_events (created_at);
```

- [ ] **Step 2: Write the queries**

`internal/infra/db/sqlc/query/sqlite/alerts.sql`:

```sql
-- Alerts: channels, rules, overrides, states, events (see migration 0012).

-- name: ListUnseededOrganizationIDs :many
SELECT organization_id FROM organizations
WHERE alert_defaults_seeded = FALSE
ORDER BY organization_id ASC;

-- name: MarkAlertDefaultsSeeded :execrows
UPDATE organizations SET alert_defaults_seeded = TRUE
WHERE organization_id = ? AND alert_defaults_seeded = FALSE;

-- name: GetAlertServerOrganizationID :one
SELECT organization_id FROM servers WHERE server_id = ?;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels
WHERE organization_id = ?
ORDER BY created_at ASC, channel_id ASC;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels
WHERE organization_id = ? AND channel_id = ?;

-- name: InsertNotificationChannel :exec
INSERT INTO notification_channels (
    channel_id, organization_id, name, service, fields, url_encrypted,
    is_default, enabled, last_error, last_sent_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', NULL, ?);

-- name: UpdateNotificationChannel :execrows
UPDATE notification_channels
SET name = ?, service = ?, fields = ?, url_encrypted = ?, is_default = ?, enabled = ?, last_error = ?
WHERE organization_id = ? AND channel_id = ?;

-- name: DeleteNotificationChannel :execrows
DELETE FROM notification_channels WHERE organization_id = ? AND channel_id = ?;

-- name: RecordChannelSuccess :exec
UPDATE notification_channels SET last_error = '', last_sent_at = ? WHERE channel_id = ?;

-- name: RecordChannelFailure :exec
UPDATE notification_channels SET last_error = ? WHERE channel_id = ?;

-- name: ListAlertRules :many
SELECT * FROM alert_rules
WHERE organization_id = ?
ORDER BY target_kind DESC, metric ASC, threshold ASC, created_at ASC, rule_id ASC;

-- name: GetAlertRule :one
SELECT * FROM alert_rules WHERE organization_id = ? AND rule_id = ?;

-- name: InsertAlertRule :exec
INSERT INTO alert_rules (
    rule_id, organization_id, target_kind, metric, threshold, duration_minutes,
    channel_ids, enabled, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateAlertRule :execrows
UPDATE alert_rules SET threshold = ?, duration_minutes = ?, channel_ids = ?, enabled = ?
WHERE organization_id = ? AND rule_id = ?;

-- name: DeleteAlertRule :execrows
DELETE FROM alert_rules WHERE organization_id = ? AND rule_id = ?;

-- name: ListAlertOverrides :many
SELECT o.rule_id, o.server_id, o.app_id, o.disabled, o.threshold, o.duration_minutes, o.channel_ids
FROM alert_overrides AS o
JOIN alert_rules AS r ON r.rule_id = o.rule_id
WHERE r.organization_id = ?
ORDER BY o.rule_id ASC, o.server_id ASC, o.app_id ASC;

-- name: ListTargetAlertOverrides :many
SELECT * FROM alert_overrides
WHERE server_id = ? AND app_id = ?
ORDER BY rule_id ASC;

-- name: UpsertAlertOverride :exec
INSERT INTO alert_overrides (rule_id, server_id, app_id, disabled, threshold, duration_minutes, channel_ids)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (rule_id, server_id, app_id) DO UPDATE
SET disabled = excluded.disabled, threshold = excluded.threshold,
    duration_minutes = excluded.duration_minutes, channel_ids = excluded.channel_ids;

-- name: DeleteAlertOverride :execrows
DELETE FROM alert_overrides WHERE rule_id = ? AND server_id = ? AND app_id = ?;

-- FireAlertState moves a (rule, target) to firing unless it already is;
-- the row count says whether this call made the transition (several API
-- replicas evaluate the same events).
-- name: FireAlertState :execrows
INSERT INTO alert_states (rule_id, server_id, app_id, state, since, value, threshold)
VALUES (?, ?, ?, 'firing', ?, ?, ?)
ON CONFLICT (rule_id, server_id, app_id) DO UPDATE
SET state = 'firing', since = excluded.since, value = excluded.value, threshold = excluded.threshold
WHERE alert_states.state <> 'firing';

-- name: ResolveAlertState :execrows
UPDATE alert_states SET state = 'ok', since = ?, value = ?
WHERE rule_id = ? AND server_id = ? AND app_id = ? AND state = 'firing';

-- name: DeleteAlertState :exec
DELETE FROM alert_states WHERE rule_id = ? AND server_id = ? AND app_id = ?;

-- name: ListFiringAlertStates :many
SELECT s.rule_id, s.server_id, s.app_id, s.since, s.value, s.threshold, r.target_kind, r.metric
FROM alert_states AS s
JOIN alert_rules AS r ON r.rule_id = s.rule_id
WHERE s.state = 'firing'
ORDER BY s.rule_id ASC, s.server_id ASC, s.app_id ASC;

-- name: ListOrgFiringAlertStates :many
SELECT s.rule_id, s.server_id, s.app_id, s.since, s.value, s.threshold, r.target_kind, r.metric
FROM alert_states AS s
JOIN alert_rules AS r ON r.rule_id = s.rule_id
WHERE r.organization_id = ? AND s.state = 'firing'
ORDER BY s.since DESC, s.rule_id ASC;

-- name: CountFiringAlertsByServer :many
SELECT server_id, COUNT(*) AS firing FROM alert_states
WHERE state = 'firing'
GROUP BY server_id;

-- name: InsertAlertEvent :exec
INSERT INTO alert_events (
    event_id, organization_id, rule_id, server_id, app_id, server_name, app_name,
    target_kind, metric, kind, value, threshold, title, message, deliveries, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', ?);

-- name: SetAlertEventDeliveries :exec
UPDATE alert_events SET deliveries = ? WHERE event_id = ?;

-- ListAlertEvents pages an org's history newest first; an empty filter
-- matches everything, and the page starts strictly before
-- (before_at, before_id).
-- name: ListAlertEvents :many
SELECT * FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
  AND (CAST(sqlc.arg(server_id) AS TEXT) = '' OR server_id = CAST(sqlc.arg(server_id) AS TEXT))
  AND (CAST(sqlc.arg(app_id) AS TEXT) = '' OR app_id = CAST(sqlc.arg(app_id) AS TEXT))
  AND (CAST(sqlc.arg(rule_id) AS TEXT) = '' OR rule_id = CAST(sqlc.arg(rule_id) AS TEXT))
  AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR kind = CAST(sqlc.arg(kind) AS TEXT))
  AND (created_at < sqlc.arg(before_at)
       OR (created_at = sqlc.arg(before_at) AND event_id < CAST(sqlc.arg(before_id) AS TEXT)))
ORDER BY created_at DESC, event_id DESC
LIMIT sqlc.arg(row_limit);

-- name: DeleteAlertEventsBefore :execrows
DELETE FROM alert_events WHERE created_at < ?;

-- ListOversizedAlertOrganizations lists the orgs past the history cap. The
-- literal is alert repository's EventsPerOrg (sqlc cannot bind a HAVING
-- parameter); keep them in step.
-- name: ListOversizedAlertOrganizations :many
SELECT organization_id FROM alert_events
GROUP BY organization_id
HAVING COUNT(*) > 2000;

-- GetAlertEventTrimCutoff is the newest event of an org that falls outside
-- the newest `keep` (none when the org has at most `keep`).
-- name: GetAlertEventTrimCutoff :one
SELECT created_at, event_id FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
ORDER BY created_at DESC, event_id DESC
LIMIT 1 OFFSET sqlc.arg(keep);

-- name: DeleteAlertEventsUpTo :execrows
DELETE FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
  AND (created_at < sqlc.arg(cutoff_at)
       OR (created_at = sqlc.arg(cutoff_at) AND event_id <= CAST(sqlc.arg(cutoff_id) AS TEXT)));

-- name: ListAlertDirectoryServers :many
SELECT server_id, organization_id, name FROM servers ORDER BY server_id ASC;

-- name: ListAlertDirectoryApps :many
SELECT app_id, server_id, name FROM apps ORDER BY app_id ASC;

-- name: DeleteAlertOverridesByApp :exec
DELETE FROM alert_overrides WHERE app_id = ?;

-- name: DeleteAlertStatesByApp :exec
DELETE FROM alert_states WHERE app_id = ?;

-- name: DeleteAlertEventsByApp :exec
DELETE FROM alert_events WHERE app_id = ?;
```

`internal/infra/db/sqlc/query/pgsql/alerts.sql`:

```sql
-- Alerts: channels, rules, overrides, states, events (see migration 0012).

-- name: ListUnseededOrganizationIDs :many
SELECT organization_id FROM organizations
WHERE alert_defaults_seeded = FALSE
ORDER BY organization_id ASC;

-- name: MarkAlertDefaultsSeeded :execrows
UPDATE organizations SET alert_defaults_seeded = TRUE
WHERE organization_id = $1 AND alert_defaults_seeded = FALSE;

-- name: GetAlertServerOrganizationID :one
SELECT organization_id FROM servers WHERE server_id = $1;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels
WHERE organization_id = $1
ORDER BY created_at ASC, channel_id ASC;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels
WHERE organization_id = $1 AND channel_id = $2;

-- name: InsertNotificationChannel :exec
INSERT INTO notification_channels (
    channel_id, organization_id, name, service, fields, url_encrypted,
    is_default, enabled, last_error, last_sent_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '', NULL, $9);

-- name: UpdateNotificationChannel :execrows
UPDATE notification_channels
SET name = $1, service = $2, fields = $3, url_encrypted = $4, is_default = $5, enabled = $6, last_error = $7
WHERE organization_id = $8 AND channel_id = $9;

-- name: DeleteNotificationChannel :execrows
DELETE FROM notification_channels WHERE organization_id = $1 AND channel_id = $2;

-- name: RecordChannelSuccess :exec
UPDATE notification_channels SET last_error = '', last_sent_at = $1 WHERE channel_id = $2;

-- name: RecordChannelFailure :exec
UPDATE notification_channels SET last_error = $1 WHERE channel_id = $2;

-- name: ListAlertRules :many
SELECT * FROM alert_rules
WHERE organization_id = $1
ORDER BY target_kind DESC, metric ASC, threshold ASC, created_at ASC, rule_id ASC;

-- name: GetAlertRule :one
SELECT * FROM alert_rules WHERE organization_id = $1 AND rule_id = $2;

-- name: InsertAlertRule :exec
INSERT INTO alert_rules (
    rule_id, organization_id, target_kind, metric, threshold, duration_minutes,
    channel_ids, enabled, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UpdateAlertRule :execrows
UPDATE alert_rules SET threshold = $1, duration_minutes = $2, channel_ids = $3, enabled = $4
WHERE organization_id = $5 AND rule_id = $6;

-- name: DeleteAlertRule :execrows
DELETE FROM alert_rules WHERE organization_id = $1 AND rule_id = $2;

-- name: ListAlertOverrides :many
SELECT o.rule_id, o.server_id, o.app_id, o.disabled, o.threshold, o.duration_minutes, o.channel_ids
FROM alert_overrides AS o
JOIN alert_rules AS r ON r.rule_id = o.rule_id
WHERE r.organization_id = $1
ORDER BY o.rule_id ASC, o.server_id ASC, o.app_id ASC;

-- name: ListTargetAlertOverrides :many
SELECT * FROM alert_overrides
WHERE server_id = $1 AND app_id = $2
ORDER BY rule_id ASC;

-- name: UpsertAlertOverride :exec
INSERT INTO alert_overrides (rule_id, server_id, app_id, disabled, threshold, duration_minutes, channel_ids)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (rule_id, server_id, app_id) DO UPDATE
SET disabled = excluded.disabled, threshold = excluded.threshold,
    duration_minutes = excluded.duration_minutes, channel_ids = excluded.channel_ids;

-- name: DeleteAlertOverride :execrows
DELETE FROM alert_overrides WHERE rule_id = $1 AND server_id = $2 AND app_id = $3;

-- FireAlertState moves a (rule, target) to firing unless it already is;
-- the row count says whether this call made the transition (several API
-- replicas evaluate the same events).
-- name: FireAlertState :execrows
INSERT INTO alert_states (rule_id, server_id, app_id, state, since, value, threshold)
VALUES ($1, $2, $3, 'firing', $4, $5, $6)
ON CONFLICT (rule_id, server_id, app_id) DO UPDATE
SET state = 'firing', since = excluded.since, value = excluded.value, threshold = excluded.threshold
WHERE alert_states.state <> 'firing';

-- name: ResolveAlertState :execrows
UPDATE alert_states SET state = 'ok', since = $1, value = $2
WHERE rule_id = $3 AND server_id = $4 AND app_id = $5 AND state = 'firing';

-- name: DeleteAlertState :exec
DELETE FROM alert_states WHERE rule_id = $1 AND server_id = $2 AND app_id = $3;

-- name: ListFiringAlertStates :many
SELECT s.rule_id, s.server_id, s.app_id, s.since, s.value, s.threshold, r.target_kind, r.metric
FROM alert_states AS s
JOIN alert_rules AS r ON r.rule_id = s.rule_id
WHERE s.state = 'firing'
ORDER BY s.rule_id ASC, s.server_id ASC, s.app_id ASC;

-- name: ListOrgFiringAlertStates :many
SELECT s.rule_id, s.server_id, s.app_id, s.since, s.value, s.threshold, r.target_kind, r.metric
FROM alert_states AS s
JOIN alert_rules AS r ON r.rule_id = s.rule_id
WHERE r.organization_id = $1 AND s.state = 'firing'
ORDER BY s.since DESC, s.rule_id ASC;

-- name: CountFiringAlertsByServer :many
SELECT server_id, COUNT(*) AS firing FROM alert_states
WHERE state = 'firing'
GROUP BY server_id;

-- name: InsertAlertEvent :exec
INSERT INTO alert_events (
    event_id, organization_id, rule_id, server_id, app_id, server_name, app_name,
    target_kind, metric, kind, value, threshold, title, message, deliveries, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, '[]', $15);

-- name: SetAlertEventDeliveries :exec
UPDATE alert_events SET deliveries = $1 WHERE event_id = $2;

-- ListAlertEvents pages an org's history newest first; an empty filter
-- matches everything, and the page starts strictly before
-- (before_at, before_id).
-- name: ListAlertEvents :many
SELECT * FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
  AND (sqlc.arg(server_id)::text = '' OR server_id::text = sqlc.arg(server_id)::text)
  AND (sqlc.arg(app_id)::text = '' OR app_id = sqlc.arg(app_id)::text)
  AND (sqlc.arg(rule_id)::text = '' OR rule_id::text = sqlc.arg(rule_id)::text)
  AND (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind)::text)
  AND (created_at < sqlc.arg(before_at)
       OR (created_at = sqlc.arg(before_at) AND event_id < sqlc.arg(before_id)::text))
ORDER BY created_at DESC, event_id DESC
LIMIT sqlc.arg(row_limit)::bigint;

-- name: DeleteAlertEventsBefore :execrows
DELETE FROM alert_events WHERE created_at < $1;

-- ListOversizedAlertOrganizations lists the orgs past the history cap. The
-- literal is alert repository's EventsPerOrg (sqlc cannot bind a HAVING
-- parameter); keep them in step.
-- name: ListOversizedAlertOrganizations :many
SELECT organization_id FROM alert_events
GROUP BY organization_id
HAVING COUNT(*) > 2000;

-- GetAlertEventTrimCutoff is the newest event of an org that falls outside
-- the newest `keep` (none when the org has at most `keep`).
-- name: GetAlertEventTrimCutoff :one
SELECT created_at, event_id FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
ORDER BY created_at DESC, event_id DESC
LIMIT 1 OFFSET sqlc.arg(keep)::bigint;

-- name: DeleteAlertEventsUpTo :execrows
DELETE FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
  AND (created_at < sqlc.arg(cutoff_at)
       OR (created_at = sqlc.arg(cutoff_at) AND event_id <= sqlc.arg(cutoff_id)::text));

-- name: ListAlertDirectoryServers :many
SELECT server_id, organization_id, name FROM servers ORDER BY server_id ASC;

-- name: ListAlertDirectoryApps :many
SELECT app_id, server_id, name FROM apps ORDER BY app_id ASC;

-- name: DeleteAlertOverridesByApp :exec
DELETE FROM alert_overrides WHERE app_id = $1;

-- name: DeleteAlertStatesByApp :exec
DELETE FROM alert_states WHERE app_id = $1;

-- name: DeleteAlertEventsByApp :exec
DELETE FROM alert_events WHERE app_id = $1;
```

- [ ] **Step 3: Map nullable Postgres int4/float8 and generate**

In `internal/infra/db/sqlc/sqlc.yaml`, right after the existing Postgres entry `- db_type: "pg_catalog.int4"` / `go_type: "int64"`, add:

```yaml
          # Nullable override columns (alert_overrides.threshold/duration_minutes):
          # database/sql would otherwise use sql.Null* on Postgres only.
          - db_type: "pg_catalog.int4"
            nullable: true
            go_type:
              type: "int64"
              pointer: true
          - db_type: "pg_catalog.float8"
            nullable: true
            go_type:
              type: "float64"
              pointer: true
```

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp make sqlc`
Expected: new `gen/{sqlite,pgsql}/alerts.sql.go`; `models.go` gains `AlertEvent, AlertOverride, AlertRule, AlertState, NotificationChannel`, and `Organization` gains `AlertDefaultsSeeded bool`. Check that `grep -n 'interface{}\|sqlc\.arg' internal/infra/db/sqlc/gen/*/alerts.sql.go` prints only the two "Code generated" header lines.

- [ ] **Step 4: Assert identical shapes**

Append to the `var (…)` block of `internal/infra/db/sqlc/shapes_test.go` one pair per type, both directions, in this form:

```go
	_ = sqlitegen.AlertEvent(pgsqlgen.AlertEvent{})
	_ = pgsqlgen.AlertEvent(sqlitegen.AlertEvent{})
```

Add a pair for each of these types:
- `AlertEvent`, `AlertOverride`, `AlertRule`, `AlertState`
- `CountFiringAlertsByServerRow`
- `DeleteAlertEventsUpToParams`, `DeleteAlertOverrideParams`, `DeleteAlertRuleParams`, `DeleteAlertStateParams`, `DeleteNotificationChannelParams`
- `FireAlertStateParams`
- `GetAlertEventTrimCutoffParams`, `GetAlertEventTrimCutoffRow`, `GetAlertRuleParams`, `GetNotificationChannelParams`
- `InsertAlertEventParams`, `InsertAlertRuleParams`, `InsertNotificationChannelParams`
- `ListAlertDirectoryAppsRow`, `ListAlertDirectoryServersRow`, `ListAlertEventsParams`, `ListFiringAlertStatesRow`, `ListOrgFiringAlertStatesRow`, `ListTargetAlertOverridesParams`
- `NotificationChannel`, `Organization` (skip it if a pair already exists)
- `RecordChannelFailureParams`, `RecordChannelSuccessParams`, `ResolveAlertStateParams`
- `SetAlertEventDeliveriesParams`, `UpdateAlertRuleParams`, `UpdateNotificationChannelParams`, `UpsertAlertOverrideParams`

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/infra/db/sqlc/`
Expected: no output.

- [ ] **Step 5: Write the failing repository tests**

`internal/infra/db/repository/alert/repo_test.go`:

```go
package alert

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	appA       = dbtest.ID(21)
)

// t0 is a fixed, second-aligned instant.
var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// ids mints predictable UUIDs (dbtest.ID(1000+n)) so ordering is stable.
func ids() func() string {
	var n atomic.Int64
	return func() string { return dbtest.ID(int(1000 + n.Add(1))) }
}

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedApp(t, d, appA, srv1, "nextcloud")
	r, err := New(d.Tx, d.Driver, dbtest.Log, ids())
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func count(t *testing.T, d *db.DB, table, where string, args ...any) string {
	t.Helper()
	return dbtest.QueryString(t, d, "SELECT CAST(COUNT(*) AS TEXT) FROM "+table+" WHERE "+where, args...)
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log, ids()); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestChannelLifecycle(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	ch, err := r.CreateChannel(ctx, alert.Channel{
		OrgID: org1, Name: "Ops", Service: "telegram", Fields: map[string]string{"ui_service": "telegram"},
		IsDefault: true, Enabled: true, URLEncrypted: "sealed-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID == "" || ch.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", ch)
	}
	got, err := r.GetChannel(ctx, org1, ch.ID)
	if err != nil || got.Name != "Ops" || got.URLEncrypted != "sealed-1" || got.Fields["ui_service"] != "telegram" ||
		!got.IsDefault || !got.Enabled || got.LastSentAt != nil {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if _, err := r.GetChannel(ctx, org2, ch.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("other org's channel: %v", err)
	}

	got.Name, got.IsDefault, got.URLEncrypted = "Pager", false, "sealed-2"
	if err := r.UpdateChannel(ctx, got); err != nil {
		t.Fatal(err)
	}
	foreign := got
	foreign.OrgID = org2
	if err := r.UpdateChannel(ctx, foreign); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("update across orgs: %v", err)
	}

	if err := r.RecordDelivery(ctx, ch.ID, t0, "boom"); err != nil {
		t.Fatal(err)
	}
	after, _ := r.GetChannel(ctx, org1, ch.ID)
	if after.LastError != "boom" || after.LastSentAt != nil || after.Name != "Pager" || after.URLEncrypted != "sealed-2" {
		t.Fatalf("after failure = %+v", after)
	}
	if err := r.RecordDelivery(ctx, ch.ID, t0.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	after, _ = r.GetChannel(ctx, org1, ch.ID)
	if after.LastError != "" || after.LastSentAt == nil || !after.LastSentAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("after success = %+v", after)
	}

	list, err := r.ListChannels(ctx, org1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := r.DeleteChannel(ctx, org2, ch.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("delete across orgs: %v", err)
	}
	if err := r.DeleteChannel(ctx, org1, ch.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.ListChannels(ctx, org1); len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
}

func TestRuleLifecycleKeepsChannelEncoding(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	defaults, err := r.CreateRule(ctx, alert.Rule{OrgID: org1, TargetKind: alert.TargetServer, Metric: alert.MetricCPU,
		Threshold: 90, DurationMinutes: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	silent, _ := r.CreateRule(ctx, alert.Rule{OrgID: org1, TargetKind: alert.TargetApp, Metric: alert.MetricDisk,
		Threshold: 50, DurationMinutes: 5, ChannelIDs: []string{}, Enabled: true})
	routed, _ := r.CreateRule(ctx, alert.Rule{OrgID: org1, TargetKind: alert.TargetServer, Metric: alert.MetricCPU,
		Threshold: 95, DurationMinutes: 2, ChannelIDs: []string{"c1", "c2"}, Enabled: false})

	rules, err := r.ListRules(ctx, org1)
	if err != nil || len(rules) != 3 {
		t.Fatalf("list = %+v, %v", rules, err)
	}
	// Server rules first, then metric, then threshold.
	if rules[0].ID != defaults.ID || rules[1].ID != routed.ID || rules[2].ID != silent.ID {
		t.Fatalf("order = %s %s %s", rules[0].Metric, rules[1].Metric, rules[2].Metric)
	}
	if rules[0].ChannelIDs != nil || rules[2].ChannelIDs == nil || len(rules[2].ChannelIDs) != 0 ||
		len(rules[1].ChannelIDs) != 2 || rules[1].Enabled {
		t.Fatalf("channel encoding lost: %+v", rules)
	}

	defaults.Threshold, defaults.ChannelIDs = 80, []string{"c3"}
	if err := r.UpdateRule(ctx, defaults); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetRule(ctx, org1, defaults.ID)
	if err != nil || got.Threshold != 80 || len(got.ChannelIDs) != 1 || got.TargetKind != alert.TargetServer {
		t.Fatalf("updated = %+v, %v", got, err)
	}
	if _, err := r.GetRule(ctx, org2, defaults.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("other org's rule: %v", err)
	}
	if err := r.DeleteRule(ctx, org2, defaults.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("delete across orgs: %v", err)
	}
	if err := r.DeleteRule(ctx, org1, defaults.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOverridesCascadeWithTheirRule(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule, _ := r.CreateRule(ctx, alert.Rule{OrgID: org1, TargetKind: alert.TargetApp, Metric: alert.MetricMemory,
		Threshold: 90, DurationMinutes: 10, Enabled: true})
	th, dur := 75.0, 3
	o := alert.Override{RuleID: rule.ID, ServerID: srv1, AppID: appA, Threshold: &th, DurationMinutes: &dur, ChannelIDs: []string{}}
	if err := r.SaveOverride(ctx, o); err != nil {
		t.Fatal(err)
	}
	o.Disabled, o.Threshold = true, nil
	if err := r.SaveOverride(ctx, o); err != nil { // upsert replaces
		t.Fatal(err)
	}
	got, err := r.TargetOverrides(ctx, alert.Target{ServerID: srv1, AppID: appA})
	if err != nil || len(got) != 1 || !got[0].Disabled || got[0].Threshold != nil ||
		got[0].DurationMinutes == nil || *got[0].DurationMinutes != 3 || got[0].ChannelIDs == nil {
		t.Fatalf("target overrides = %+v, %v", got, err)
	}
	if all, _ := r.ListOverrides(ctx, org1); len(all) != 1 {
		t.Fatalf("org overrides = %+v", all)
	}
	if all, _ := r.ListOverrides(ctx, org2); len(all) != 0 {
		t.Fatalf("other org sees overrides: %+v", all)
	}
	if err := r.DeleteRule(ctx, org1, rule.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, d, "alert_overrides", "rule_id = ?", rule.ID); n != "0" {
		t.Fatalf("overrides left after rule delete: %s", n)
	}
	if err := r.DeleteOverride(ctx, o.Key()); err != nil {
		t.Fatalf("deleting a missing override: %v", err)
	}
}

func TestSeedDefaultsOncePerOrg(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	n, err := r.SeedDefaults(ctx)
	if err != nil || n != 2 {
		t.Fatalf("seeded %d orgs, %v", n, err)
	}
	rules, _ := r.ListRules(ctx, org1)
	if len(rules) != len(alert.DefaultRules()) {
		t.Fatalf("org1 rules = %d", len(rules))
	}
	// Deleting a default is permanent: a second seed adds nothing.
	if err := r.DeleteRule(ctx, org1, rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, err := r.SeedDefaults(ctx); err != nil || n != 0 {
		t.Fatalf("second seed: %d, %v", n, err)
	}
	if got := count(t, d, "alert_rules", "organization_id = ?", org1); got != fmt.Sprint(len(alert.DefaultRules())-1) {
		t.Fatalf("org1 rules after reseed = %s", got)
	}
}

func TestServerOrgID(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if org, err := r.ServerOrgID(ctx, srv2); err != nil || org != org2 {
		t.Fatalf("srv2 org = %q, %v", org, err)
	}
	if _, err := r.ServerOrgID(ctx, dbtest.ID(99)); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("unknown server: %v", err)
	}
}
```

`internal/infra/db/repository/alert/states_test.go`:

```go
package alert

import (
	"fmt"
	"testing"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/infra/db/dbtest"
)

func event(r *Repo, org, ruleID, server, app string, kind alert.EventKind, at time.Time) alert.Event {
	return alert.Event{
		ID: r.newID(), OrgID: org, RuleID: ruleID, ServerID: server, AppID: app, ServerName: "prod-1",
		TargetKind: alert.TargetServer, Metric: alert.MetricCPU, Kind: kind, Value: 93, Threshold: 90,
		Title: "t", Message: "m", CreatedAt: at,
	}
}

func cpuRule(t *testing.T, r *Repo, org string) alert.Rule {
	t.Helper()
	rule, err := r.CreateRule(dbtest.Ctx(t), alert.Rule{OrgID: org, TargetKind: alert.TargetServer,
		Metric: alert.MetricCPU, Threshold: 90, DurationMinutes: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func TestFireIsCompareAndSet(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := cpuRule(t, r, org1)
	k := alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv1}}

	won, err := r.Fire(ctx, k, 93, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0))
	if err != nil || !won {
		t.Fatalf("first fire: won=%v err=%v", won, err)
	}
	// A second replica (or a replay) loses and writes no event.
	won, err = r.Fire(ctx, k, 95, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0.Add(time.Minute)))
	if err != nil || won {
		t.Fatalf("second fire: won=%v err=%v", won, err)
	}
	if n := count(t, d, "alert_events", "rule_id = ?", rule.ID); n != "1" {
		t.Fatalf("events = %s", n)
	}
	states, err := r.FiringStates(ctx)
	if err != nil || len(states) != 1 || states[0].Key != k || states[0].Metric != alert.MetricCPU ||
		states[0].Value != 93 || !states[0].Since.Equal(t0) {
		t.Fatalf("firing = %+v, %v", states, err)
	}
	active, _ := r.ListActive(ctx, org1)
	if len(active) != 1 || active[0].Threshold != 90 || active[0].TargetKind != alert.TargetServer {
		t.Fatalf("active = %+v", active)
	}
	if other, _ := r.ListActive(ctx, org2); len(other) != 0 {
		t.Fatalf("other org sees %+v", other)
	}
	counts, _ := r.CountFiring(ctx)
	if counts[srv1] != 1 || counts[srv2] != 0 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestResolveIsCompareAndSet(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := cpuRule(t, r, org1)
	k := alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv1}}

	// Nothing is firing: resolving is a no-op.
	if won, err := r.Resolve(ctx, k, 10, event(r, org1, rule.ID, srv1, "", alert.EventResolved, t0)); err != nil || won {
		t.Fatalf("resolve while ok: won=%v err=%v", won, err)
	}
	if _, err := r.Fire(ctx, k, 93, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0)); err != nil {
		t.Fatal(err)
	}
	if won, err := r.Resolve(ctx, k, 41, event(r, org1, rule.ID, srv1, "", alert.EventResolved, t0.Add(time.Minute))); err != nil || !won {
		t.Fatalf("resolve: won=%v err=%v", won, err)
	}
	if won, _ := r.Resolve(ctx, k, 40, event(r, org1, rule.ID, srv1, "", alert.EventResolved, t0.Add(2*time.Minute))); won {
		t.Fatal("second resolve won")
	}
	if n := count(t, d, "alert_events", "rule_id = ?", rule.ID); n != "2" {
		t.Fatalf("events = %s", n)
	}
	if states, _ := r.FiringStates(ctx); len(states) != 0 {
		t.Fatalf("still firing: %+v", states)
	}
	// It can fire again after resolving.
	if won, _ := r.Fire(ctx, k, 99, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0.Add(3*time.Minute))); !won {
		t.Fatal("re-fire lost")
	}
	if err := r.ClearState(ctx, k); err != nil {
		t.Fatal(err)
	}
	if states, _ := r.FiringStates(ctx); len(states) != 0 {
		t.Fatalf("cleared state still firing: %+v", states)
	}
}

func TestListEventsFiltersAndPages(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := cpuRule(t, r, org1)
	k := alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv1}}
	// Alternate fire/resolve: 6 events, one per minute, newest last.
	for i := 0; i < 6; i++ {
		at := t0.Add(time.Duration(i) * time.Minute)
		if i%2 == 0 {
			_, _ = r.Fire(ctx, k, 93, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, at))
		} else {
			_, _ = r.Resolve(ctx, k, 41, event(r, org1, rule.ID, srv1, "", alert.EventResolved, at))
		}
	}
	page, err := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, Limit: 4})
	if err != nil || len(page) != 4 || !page[0].CreatedAt.Equal(t0.Add(5*time.Minute)) || page[0].Deliveries == nil {
		t.Fatalf("page 1 = %+v, %v", page, err)
	}
	last := page[len(page)-1]
	rest, _ := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, Limit: 4, BeforeAt: last.CreatedAt, BeforeID: last.ID})
	if len(rest) != 2 || !rest[1].CreatedAt.Equal(t0) {
		t.Fatalf("page 2 = %+v", rest)
	}
	fired, _ := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, Kind: alert.EventFired, Limit: 50})
	if len(fired) != 3 {
		t.Fatalf("fired = %d", len(fired))
	}
	for _, q := range []alert.EventQuery{
		{OrgID: org2, Limit: 50},
		{OrgID: org1, ServerID: srv2, Limit: 50},
		{OrgID: org1, AppID: appA, Limit: 50},
		{OrgID: org1, RuleID: dbtest.ID(77), Limit: 50},
	} {
		if got, _ := r.ListEvents(ctx, q); len(got) != 0 {
			t.Errorf("%+v matched %d", q, len(got))
		}
	}
	if got, _ := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, ServerID: srv1, RuleID: rule.ID, Limit: 50}); len(got) != 6 {
		t.Errorf("server+rule filter = %d", len(got))
	}

	ds := []alert.Delivery{{ChannelID: "c1", ChannelName: "Ops", OK: false, Error: "boom"}}
	if err := r.SetDeliveries(ctx, page[0].ID, ds); err != nil {
		t.Fatal(err)
	}
	again, _ := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, Limit: 1})
	if len(again[0].Deliveries) != 1 || again[0].Deliveries[0].Error != "boom" {
		t.Fatalf("deliveries = %+v", again[0].Deliveries)
	}
}

func TestPruneEventsByAgeAndCap(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := cpuRule(t, r, org1)
	old := t0.Add(-EventRetention - time.Hour)
	ev := event(r, org1, rule.ID, srv1, "", alert.EventFired, old)
	if err := r.insertEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	// EventsPerOrg+3 recent events, one second apart.
	for i := 0; i < EventsPerOrg+3; i++ {
		if err := r.insertEvent(ctx, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	n, err := r.PruneEvents(ctx, t0.Add(time.Hour))
	if err != nil || n != 4 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if got := count(t, d, "alert_events", "organization_id = ?", org1); got != fmt.Sprint(EventsPerOrg) {
		t.Fatalf("kept %s", got)
	}
	// The oldest recent ones went; the newest stayed.
	if got := count(t, d, "alert_events", "created_at < ?", stamp(t0.Add(3*time.Second))); got != "0" {
		t.Fatalf("old recent events kept: %s", got)
	}
}

func TestDirectory(t *testing.T) {
	r, _ := newRepo(t)
	dir, err := r.Directory(dbtest.Ctx(t))
	if err != nil || len(dir.Servers) != 2 || dir.Servers[srv1].OrgID != org1 ||
		dir.Apps[appA].ServerID != srv1 || dir.Apps[appA].Name != "nextcloud" {
		t.Fatalf("directory = %+v, %v", dir, err)
	}
}
```

In `internal/infra/db/repository/timestamps_test.go`:
- add imports `"winterflow/internal/domain/alert"`, `alertrepo "winterflow/internal/infra/db/repository/alert"` and `"winterflow/pkg/util"`;
- insert this block immediately before `columns := timestampColumns(t, d)`:

```go
	alerts := must(alertrepo.New(d.Tx, d.Driver, dbtest.Log, util.GenerateID))
	channel := must(alerts.CreateChannel(ctx, alert.Channel{OrgID: orgID, Name: "Ops", Service: "ntfy", Enabled: true, URLEncrypted: "x"}))
	if err := alerts.RecordDelivery(ctx, channel.ID, at, ""); err != nil {
		t.Fatal(err)
	}
	rule := must(alerts.CreateRule(ctx, alert.Rule{OrgID: orgID, TargetKind: alert.TargetServer, Metric: alert.MetricCPU,
		Threshold: 90, DurationMinutes: 10, Enabled: true}))
	key := alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv.ID}}
	if _, err := alerts.Fire(ctx, key, 93, 90, alert.Event{ID: util.GenerateID(), OrgID: orgID, RuleID: rule.ID,
		ServerID: srv.ID, TargetKind: alert.TargetServer, Metric: alert.MetricCPU, Kind: alert.EventFired, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}

```

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/db/repository/...`
Expected: FAIL to compile (`undefined: New` in package `alert`).

- [ ] **Step 6: Write the repository**

`sqlite.go` and `pgsql.go` are mechanical passthroughs, one method per querier method. Postgres converts params with `pgsqlgen.X(p)` and rows with `sqlitegen.X(row)` struct conversions, which the shapes test guarantees.

`internal/infra/db/repository/alert/repo.go`:

```go
// Package alert implements port.AlertRepository over migration 0012:
// notification channels, alert rules with per-target overrides, the state
// of each (rule, target) and the event history. State transitions are
// compare-and-set, so several API replicas that evaluate the same events
// record each transition (and its event) once.
package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	channelRow       = sqlitegen.NotificationChannel
	ruleRow          = sqlitegen.AlertRule
	overrideRow      = sqlitegen.AlertOverride
	eventRow         = sqlitegen.AlertEvent
	firingRow        = sqlitegen.ListFiringAlertStatesRow
	orgFiringRow     = sqlitegen.ListOrgFiringAlertStatesRow
	countRow         = sqlitegen.CountFiringAlertsByServerRow
	dirServerRow     = sqlitegen.ListAlertDirectoryServersRow
	dirAppRow        = sqlitegen.ListAlertDirectoryAppsRow
	cutoffRow        = sqlitegen.GetAlertEventTrimCutoffRow
	getChannelParams = sqlitegen.GetNotificationChannelParams
	insertChanParams = sqlitegen.InsertNotificationChannelParams
	updateChanParams = sqlitegen.UpdateNotificationChannelParams
	deleteChanParams = sqlitegen.DeleteNotificationChannelParams
	successParams    = sqlitegen.RecordChannelSuccessParams
	failureParams    = sqlitegen.RecordChannelFailureParams
	getRuleParams    = sqlitegen.GetAlertRuleParams
	insertRuleParams = sqlitegen.InsertAlertRuleParams
	updateRuleParams = sqlitegen.UpdateAlertRuleParams
	deleteRuleParams = sqlitegen.DeleteAlertRuleParams
	targetOvrParams  = sqlitegen.ListTargetAlertOverridesParams
	upsertOvrParams  = sqlitegen.UpsertAlertOverrideParams
	deleteOvrParams  = sqlitegen.DeleteAlertOverrideParams
	fireParams       = sqlitegen.FireAlertStateParams
	resolveParams    = sqlitegen.ResolveAlertStateParams
	deleteStateParam = sqlitegen.DeleteAlertStateParams
	insertEventParam = sqlitegen.InsertAlertEventParams
	deliveriesParams = sqlitegen.SetAlertEventDeliveriesParams
	listEventsParams = sqlitegen.ListAlertEventsParams
	cutoffParams     = sqlitegen.GetAlertEventTrimCutoffParams
	deleteUpToParams = sqlitegen.DeleteAlertEventsUpToParams
)

type querier interface {
	ListUnseededOrganizationIDs(ctx context.Context, db backend.DBTX) ([]string, error)
	MarkAlertDefaultsSeeded(ctx context.Context, db backend.DBTX, orgID string) (int64, error)
	GetAlertServerOrganizationID(ctx context.Context, db backend.DBTX, serverID string) (string, error)

	ListNotificationChannels(ctx context.Context, db backend.DBTX, orgID string) ([]channelRow, error)
	GetNotificationChannel(ctx context.Context, db backend.DBTX, p getChannelParams) (channelRow, error)
	InsertNotificationChannel(ctx context.Context, db backend.DBTX, p insertChanParams) error
	UpdateNotificationChannel(ctx context.Context, db backend.DBTX, p updateChanParams) (int64, error)
	DeleteNotificationChannel(ctx context.Context, db backend.DBTX, p deleteChanParams) (int64, error)
	RecordChannelSuccess(ctx context.Context, db backend.DBTX, p successParams) error
	RecordChannelFailure(ctx context.Context, db backend.DBTX, p failureParams) error

	ListAlertRules(ctx context.Context, db backend.DBTX, orgID string) ([]ruleRow, error)
	GetAlertRule(ctx context.Context, db backend.DBTX, p getRuleParams) (ruleRow, error)
	InsertAlertRule(ctx context.Context, db backend.DBTX, p insertRuleParams) error
	UpdateAlertRule(ctx context.Context, db backend.DBTX, p updateRuleParams) (int64, error)
	DeleteAlertRule(ctx context.Context, db backend.DBTX, p deleteRuleParams) (int64, error)

	ListAlertOverrides(ctx context.Context, db backend.DBTX, orgID string) ([]overrideRow, error)
	ListTargetAlertOverrides(ctx context.Context, db backend.DBTX, p targetOvrParams) ([]overrideRow, error)
	UpsertAlertOverride(ctx context.Context, db backend.DBTX, p upsertOvrParams) error
	DeleteAlertOverride(ctx context.Context, db backend.DBTX, p deleteOvrParams) (int64, error)

	FireAlertState(ctx context.Context, db backend.DBTX, p fireParams) (int64, error)
	ResolveAlertState(ctx context.Context, db backend.DBTX, p resolveParams) (int64, error)
	DeleteAlertState(ctx context.Context, db backend.DBTX, p deleteStateParam) error
	ListFiringAlertStates(ctx context.Context, db backend.DBTX) ([]firingRow, error)
	ListOrgFiringAlertStates(ctx context.Context, db backend.DBTX, orgID string) ([]orgFiringRow, error)
	CountFiringAlertsByServer(ctx context.Context, db backend.DBTX) ([]countRow, error)

	InsertAlertEvent(ctx context.Context, db backend.DBTX, p insertEventParam) error
	SetAlertEventDeliveries(ctx context.Context, db backend.DBTX, p deliveriesParams) error
	ListAlertEvents(ctx context.Context, db backend.DBTX, p listEventsParams) ([]eventRow, error)
	DeleteAlertEventsBefore(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error)
	ListOversizedAlertOrganizations(ctx context.Context, db backend.DBTX) ([]string, error)
	GetAlertEventTrimCutoff(ctx context.Context, db backend.DBTX, p cutoffParams) (cutoffRow, error)
	DeleteAlertEventsUpTo(ctx context.Context, db backend.DBTX, p deleteUpToParams) (int64, error)

	ListAlertDirectoryServers(ctx context.Context, db backend.DBTX) ([]dirServerRow, error)
	ListAlertDirectoryApps(ctx context.Context, db backend.DBTX) ([]dirAppRow, error)
}

// Repo is the alerts repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
	// newID mints channel, rule and event ids (util.GenerateID).
	newID func() string
}

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger, newID func() string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log, newID: newID}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log, newID: newID}, nil
	}
	return nil, fmt.Errorf("alert repository: unknown database driver %q", driver)
}

// notFound maps sql.ErrNoRows to alert.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return alert.ErrNotFound
	}
	return err
}

// encodeIDs stores a channel list: nil stays NULL (defaults/inherit), an
// empty list is "[]".
func encodeIDs(ids []string) *string {
	if ids == nil {
		return nil
	}
	b, _ := json.Marshal(ids) // a []string always marshals
	s := string(b)
	return &s
}

// decodeIDs reads a stored channel list; NULL is nil, "[]" an empty list.
func (r *Repo) decodeIDs(s *string) []string {
	if s == nil {
		return nil
	}
	ids := []string{}
	if err := json.Unmarshal([]byte(*s), &ids); err != nil {
		r.log.Warn("alerts: undecodable channel_ids", "error", err)
		return []string{}
	}
	return ids
}

// utc is a stored timestamp as UTC.
func utc(dt types.DateTime) time.Time { return dt.Time().UTC() }

// stamp is t as a stored timestamp (UTC, second precision).
func stamp(t time.Time) types.DateTime { return types.DateTime(t.UTC().Truncate(time.Second)) }
```

`internal/infra/db/repository/alert/sqlite.go`:

```go
package alert

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListUnseededOrganizationIDs(ctx context.Context, db backend.DBTX) ([]string, error) {
	return sqlitegen.New(db).ListUnseededOrganizationIDs(ctx)
}

func (sqliteQuerier) MarkAlertDefaultsSeeded(ctx context.Context, db backend.DBTX, orgID string) (int64, error) {
	return sqlitegen.New(db).MarkAlertDefaultsSeeded(ctx, orgID)
}

func (sqliteQuerier) GetAlertServerOrganizationID(ctx context.Context, db backend.DBTX, serverID string) (string, error) {
	return sqlitegen.New(db).GetAlertServerOrganizationID(ctx, serverID)
}

func (sqliteQuerier) ListNotificationChannels(ctx context.Context, db backend.DBTX, orgID string) ([]channelRow, error) {
	return sqlitegen.New(db).ListNotificationChannels(ctx, orgID)
}

func (sqliteQuerier) GetNotificationChannel(ctx context.Context, db backend.DBTX, p getChannelParams) (channelRow, error) {
	return sqlitegen.New(db).GetNotificationChannel(ctx, p)
}

func (sqliteQuerier) InsertNotificationChannel(ctx context.Context, db backend.DBTX, p insertChanParams) error {
	return sqlitegen.New(db).InsertNotificationChannel(ctx, p)
}

func (sqliteQuerier) UpdateNotificationChannel(ctx context.Context, db backend.DBTX, p updateChanParams) (int64, error) {
	return sqlitegen.New(db).UpdateNotificationChannel(ctx, p)
}

func (sqliteQuerier) DeleteNotificationChannel(ctx context.Context, db backend.DBTX, p deleteChanParams) (int64, error) {
	return sqlitegen.New(db).DeleteNotificationChannel(ctx, p)
}

func (sqliteQuerier) RecordChannelSuccess(ctx context.Context, db backend.DBTX, p successParams) error {
	return sqlitegen.New(db).RecordChannelSuccess(ctx, p)
}

func (sqliteQuerier) RecordChannelFailure(ctx context.Context, db backend.DBTX, p failureParams) error {
	return sqlitegen.New(db).RecordChannelFailure(ctx, p)
}

func (sqliteQuerier) ListAlertRules(ctx context.Context, db backend.DBTX, orgID string) ([]ruleRow, error) {
	return sqlitegen.New(db).ListAlertRules(ctx, orgID)
}

func (sqliteQuerier) GetAlertRule(ctx context.Context, db backend.DBTX, p getRuleParams) (ruleRow, error) {
	return sqlitegen.New(db).GetAlertRule(ctx, p)
}

func (sqliteQuerier) InsertAlertRule(ctx context.Context, db backend.DBTX, p insertRuleParams) error {
	return sqlitegen.New(db).InsertAlertRule(ctx, p)
}

func (sqliteQuerier) UpdateAlertRule(ctx context.Context, db backend.DBTX, p updateRuleParams) (int64, error) {
	return sqlitegen.New(db).UpdateAlertRule(ctx, p)
}

func (sqliteQuerier) DeleteAlertRule(ctx context.Context, db backend.DBTX, p deleteRuleParams) (int64, error) {
	return sqlitegen.New(db).DeleteAlertRule(ctx, p)
}

func (sqliteQuerier) ListAlertOverrides(ctx context.Context, db backend.DBTX, orgID string) ([]overrideRow, error) {
	return sqlitegen.New(db).ListAlertOverrides(ctx, orgID)
}

func (sqliteQuerier) ListTargetAlertOverrides(ctx context.Context, db backend.DBTX, p targetOvrParams) ([]overrideRow, error) {
	return sqlitegen.New(db).ListTargetAlertOverrides(ctx, p)
}

func (sqliteQuerier) UpsertAlertOverride(ctx context.Context, db backend.DBTX, p upsertOvrParams) error {
	return sqlitegen.New(db).UpsertAlertOverride(ctx, p)
}

func (sqliteQuerier) DeleteAlertOverride(ctx context.Context, db backend.DBTX, p deleteOvrParams) (int64, error) {
	return sqlitegen.New(db).DeleteAlertOverride(ctx, p)
}

func (sqliteQuerier) FireAlertState(ctx context.Context, db backend.DBTX, p fireParams) (int64, error) {
	return sqlitegen.New(db).FireAlertState(ctx, p)
}

func (sqliteQuerier) ResolveAlertState(ctx context.Context, db backend.DBTX, p resolveParams) (int64, error) {
	return sqlitegen.New(db).ResolveAlertState(ctx, p)
}

func (sqliteQuerier) DeleteAlertState(ctx context.Context, db backend.DBTX, p deleteStateParam) error {
	return sqlitegen.New(db).DeleteAlertState(ctx, p)
}

func (sqliteQuerier) ListFiringAlertStates(ctx context.Context, db backend.DBTX) ([]firingRow, error) {
	return sqlitegen.New(db).ListFiringAlertStates(ctx)
}

func (sqliteQuerier) ListOrgFiringAlertStates(ctx context.Context, db backend.DBTX, orgID string) ([]orgFiringRow, error) {
	return sqlitegen.New(db).ListOrgFiringAlertStates(ctx, orgID)
}

func (sqliteQuerier) CountFiringAlertsByServer(ctx context.Context, db backend.DBTX) ([]countRow, error) {
	return sqlitegen.New(db).CountFiringAlertsByServer(ctx)
}

func (sqliteQuerier) InsertAlertEvent(ctx context.Context, db backend.DBTX, p insertEventParam) error {
	return sqlitegen.New(db).InsertAlertEvent(ctx, p)
}

func (sqliteQuerier) SetAlertEventDeliveries(ctx context.Context, db backend.DBTX, p deliveriesParams) error {
	return sqlitegen.New(db).SetAlertEventDeliveries(ctx, p)
}

func (sqliteQuerier) ListAlertEvents(ctx context.Context, db backend.DBTX, p listEventsParams) ([]eventRow, error) {
	return sqlitegen.New(db).ListAlertEvents(ctx, p)
}

func (sqliteQuerier) DeleteAlertEventsBefore(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error) {
	return sqlitegen.New(db).DeleteAlertEventsBefore(ctx, before)
}

func (sqliteQuerier) ListOversizedAlertOrganizations(ctx context.Context, db backend.DBTX) ([]string, error) {
	return sqlitegen.New(db).ListOversizedAlertOrganizations(ctx)
}

func (sqliteQuerier) GetAlertEventTrimCutoff(ctx context.Context, db backend.DBTX, p cutoffParams) (cutoffRow, error) {
	return sqlitegen.New(db).GetAlertEventTrimCutoff(ctx, p)
}

func (sqliteQuerier) DeleteAlertEventsUpTo(ctx context.Context, db backend.DBTX, p deleteUpToParams) (int64, error) {
	return sqlitegen.New(db).DeleteAlertEventsUpTo(ctx, p)
}

func (sqliteQuerier) ListAlertDirectoryServers(ctx context.Context, db backend.DBTX) ([]dirServerRow, error) {
	return sqlitegen.New(db).ListAlertDirectoryServers(ctx)
}

func (sqliteQuerier) ListAlertDirectoryApps(ctx context.Context, db backend.DBTX) ([]dirAppRow, error) {
	return sqlitegen.New(db).ListAlertDirectoryApps(ctx)
}
```

`internal/infra/db/repository/alert/pgsql.go`:

```go
package alert

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
	"winterflow/internal/infra/db/types"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListUnseededOrganizationIDs(ctx context.Context, db backend.DBTX) ([]string, error) {
	return pgsqlgen.New(db).ListUnseededOrganizationIDs(ctx)
}

func (pgsqlQuerier) MarkAlertDefaultsSeeded(ctx context.Context, db backend.DBTX, orgID string) (int64, error) {
	return pgsqlgen.New(db).MarkAlertDefaultsSeeded(ctx, orgID)
}

func (pgsqlQuerier) GetAlertServerOrganizationID(ctx context.Context, db backend.DBTX, serverID string) (string, error) {
	return pgsqlgen.New(db).GetAlertServerOrganizationID(ctx, serverID)
}

func (pgsqlQuerier) ListNotificationChannels(ctx context.Context, db backend.DBTX, orgID string) ([]channelRow, error) {
	rows, err := pgsqlgen.New(db).ListNotificationChannels(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]channelRow, len(rows))
	for i, row := range rows {
		out[i] = channelRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) GetNotificationChannel(ctx context.Context, db backend.DBTX, p getChannelParams) (channelRow, error) {
	row, err := pgsqlgen.New(db).GetNotificationChannel(ctx, pgsqlgen.GetNotificationChannelParams(p))
	return channelRow(row), err
}

func (pgsqlQuerier) InsertNotificationChannel(ctx context.Context, db backend.DBTX, p insertChanParams) error {
	return pgsqlgen.New(db).InsertNotificationChannel(ctx, pgsqlgen.InsertNotificationChannelParams(p))
}

func (pgsqlQuerier) UpdateNotificationChannel(ctx context.Context, db backend.DBTX, p updateChanParams) (int64, error) {
	return pgsqlgen.New(db).UpdateNotificationChannel(ctx, pgsqlgen.UpdateNotificationChannelParams(p))
}

func (pgsqlQuerier) DeleteNotificationChannel(ctx context.Context, db backend.DBTX, p deleteChanParams) (int64, error) {
	return pgsqlgen.New(db).DeleteNotificationChannel(ctx, pgsqlgen.DeleteNotificationChannelParams(p))
}

func (pgsqlQuerier) RecordChannelSuccess(ctx context.Context, db backend.DBTX, p successParams) error {
	return pgsqlgen.New(db).RecordChannelSuccess(ctx, pgsqlgen.RecordChannelSuccessParams(p))
}

func (pgsqlQuerier) RecordChannelFailure(ctx context.Context, db backend.DBTX, p failureParams) error {
	return pgsqlgen.New(db).RecordChannelFailure(ctx, pgsqlgen.RecordChannelFailureParams(p))
}

func (pgsqlQuerier) ListAlertRules(ctx context.Context, db backend.DBTX, orgID string) ([]ruleRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertRules(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]ruleRow, len(rows))
	for i, row := range rows {
		out[i] = ruleRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) GetAlertRule(ctx context.Context, db backend.DBTX, p getRuleParams) (ruleRow, error) {
	row, err := pgsqlgen.New(db).GetAlertRule(ctx, pgsqlgen.GetAlertRuleParams(p))
	return ruleRow(row), err
}

func (pgsqlQuerier) InsertAlertRule(ctx context.Context, db backend.DBTX, p insertRuleParams) error {
	return pgsqlgen.New(db).InsertAlertRule(ctx, pgsqlgen.InsertAlertRuleParams(p))
}

func (pgsqlQuerier) UpdateAlertRule(ctx context.Context, db backend.DBTX, p updateRuleParams) (int64, error) {
	return pgsqlgen.New(db).UpdateAlertRule(ctx, pgsqlgen.UpdateAlertRuleParams(p))
}

func (pgsqlQuerier) DeleteAlertRule(ctx context.Context, db backend.DBTX, p deleteRuleParams) (int64, error) {
	return pgsqlgen.New(db).DeleteAlertRule(ctx, pgsqlgen.DeleteAlertRuleParams(p))
}

func (pgsqlQuerier) ListAlertOverrides(ctx context.Context, db backend.DBTX, orgID string) ([]overrideRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertOverrides(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]overrideRow, len(rows))
	for i, row := range rows {
		out[i] = overrideRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListTargetAlertOverrides(ctx context.Context, db backend.DBTX, p targetOvrParams) ([]overrideRow, error) {
	rows, err := pgsqlgen.New(db).ListTargetAlertOverrides(ctx, pgsqlgen.ListTargetAlertOverridesParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]overrideRow, len(rows))
	for i, row := range rows {
		out[i] = overrideRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) UpsertAlertOverride(ctx context.Context, db backend.DBTX, p upsertOvrParams) error {
	return pgsqlgen.New(db).UpsertAlertOverride(ctx, pgsqlgen.UpsertAlertOverrideParams(p))
}

func (pgsqlQuerier) DeleteAlertOverride(ctx context.Context, db backend.DBTX, p deleteOvrParams) (int64, error) {
	return pgsqlgen.New(db).DeleteAlertOverride(ctx, pgsqlgen.DeleteAlertOverrideParams(p))
}

func (pgsqlQuerier) FireAlertState(ctx context.Context, db backend.DBTX, p fireParams) (int64, error) {
	return pgsqlgen.New(db).FireAlertState(ctx, pgsqlgen.FireAlertStateParams(p))
}

func (pgsqlQuerier) ResolveAlertState(ctx context.Context, db backend.DBTX, p resolveParams) (int64, error) {
	return pgsqlgen.New(db).ResolveAlertState(ctx, pgsqlgen.ResolveAlertStateParams(p))
}

func (pgsqlQuerier) DeleteAlertState(ctx context.Context, db backend.DBTX, p deleteStateParam) error {
	return pgsqlgen.New(db).DeleteAlertState(ctx, pgsqlgen.DeleteAlertStateParams(p))
}

func (pgsqlQuerier) ListFiringAlertStates(ctx context.Context, db backend.DBTX) ([]firingRow, error) {
	rows, err := pgsqlgen.New(db).ListFiringAlertStates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]firingRow, len(rows))
	for i, row := range rows {
		out[i] = firingRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListOrgFiringAlertStates(ctx context.Context, db backend.DBTX, orgID string) ([]orgFiringRow, error) {
	rows, err := pgsqlgen.New(db).ListOrgFiringAlertStates(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]orgFiringRow, len(rows))
	for i, row := range rows {
		out[i] = orgFiringRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) CountFiringAlertsByServer(ctx context.Context, db backend.DBTX) ([]countRow, error) {
	rows, err := pgsqlgen.New(db).CountFiringAlertsByServer(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]countRow, len(rows))
	for i, row := range rows {
		out[i] = countRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) InsertAlertEvent(ctx context.Context, db backend.DBTX, p insertEventParam) error {
	return pgsqlgen.New(db).InsertAlertEvent(ctx, pgsqlgen.InsertAlertEventParams(p))
}

func (pgsqlQuerier) SetAlertEventDeliveries(ctx context.Context, db backend.DBTX, p deliveriesParams) error {
	return pgsqlgen.New(db).SetAlertEventDeliveries(ctx, pgsqlgen.SetAlertEventDeliveriesParams(p))
}

func (pgsqlQuerier) ListAlertEvents(ctx context.Context, db backend.DBTX, p listEventsParams) ([]eventRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertEvents(ctx, pgsqlgen.ListAlertEventsParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]eventRow, len(rows))
	for i, row := range rows {
		out[i] = eventRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) DeleteAlertEventsBefore(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error) {
	return pgsqlgen.New(db).DeleteAlertEventsBefore(ctx, before)
}

func (pgsqlQuerier) ListOversizedAlertOrganizations(ctx context.Context, db backend.DBTX) ([]string, error) {
	return pgsqlgen.New(db).ListOversizedAlertOrganizations(ctx)
}

func (pgsqlQuerier) GetAlertEventTrimCutoff(ctx context.Context, db backend.DBTX, p cutoffParams) (cutoffRow, error) {
	row, err := pgsqlgen.New(db).GetAlertEventTrimCutoff(ctx, pgsqlgen.GetAlertEventTrimCutoffParams(p))
	return cutoffRow(row), err
}

func (pgsqlQuerier) DeleteAlertEventsUpTo(ctx context.Context, db backend.DBTX, p deleteUpToParams) (int64, error) {
	return pgsqlgen.New(db).DeleteAlertEventsUpTo(ctx, pgsqlgen.DeleteAlertEventsUpToParams(p))
}

func (pgsqlQuerier) ListAlertDirectoryServers(ctx context.Context, db backend.DBTX) ([]dirServerRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertDirectoryServers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dirServerRow, len(rows))
	for i, row := range rows {
		out[i] = dirServerRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListAlertDirectoryApps(ctx context.Context, db backend.DBTX) ([]dirAppRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertDirectoryApps(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dirAppRow, len(rows))
	for i, row := range rows {
		out[i] = dirAppRow(row)
	}
	return out, nil
}
```

`internal/infra/db/repository/alert/channels.go`:

```go
package alert

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/alert"
)

// ListChannels returns an org's channels, oldest first.
func (r *Repo) ListChannels(ctx context.Context, orgID string) ([]alert.Channel, error) {
	rows, err := r.q.ListNotificationChannels(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]alert.Channel, 0, len(rows))
	for _, row := range rows {
		out = append(out, r.channel(row))
	}
	return out, nil
}

// GetChannel returns one of the org's channels or alert.ErrNotFound.
func (r *Repo) GetChannel(ctx context.Context, orgID, id string) (alert.Channel, error) {
	row, err := r.q.GetNotificationChannel(ctx, r.tx.Querier(ctx), getChannelParams{OrganizationID: orgID, ChannelID: id})
	if err != nil {
		return alert.Channel{}, notFound(err)
	}
	return r.channel(row), nil
}

// CreateChannel stores a new channel, minting its id and creation time.
func (r *Repo) CreateChannel(ctx context.Context, ch alert.Channel) (alert.Channel, error) {
	ch.ID = r.newID()
	ch.CreatedAt = time.Now().UTC().Truncate(time.Second)
	err := r.q.InsertNotificationChannel(ctx, r.tx.Querier(ctx), insertChanParams{
		ChannelID: ch.ID, OrganizationID: ch.OrgID, Name: ch.Name, Service: ch.Service,
		Fields: encodeFields(ch.Fields), UrlEncrypted: ch.URLEncrypted,
		IsDefault: ch.IsDefault, Enabled: ch.Enabled, CreatedAt: stamp(ch.CreatedAt),
	})
	if err != nil {
		return alert.Channel{}, err
	}
	ch.LastError, ch.LastSentAt = "", nil
	return ch, nil
}

// UpdateChannel rewrites a channel's editable fields (name, service,
// fields, URL, default, enabled, last error); alert.ErrNotFound when the
// org has no such channel.
func (r *Repo) UpdateChannel(ctx context.Context, ch alert.Channel) error {
	n, err := r.q.UpdateNotificationChannel(ctx, r.tx.Querier(ctx), updateChanParams{
		Name: ch.Name, Service: ch.Service, Fields: encodeFields(ch.Fields), UrlEncrypted: ch.URLEncrypted,
		IsDefault: ch.IsDefault, Enabled: ch.Enabled, LastError: ch.LastError,
		OrganizationID: ch.OrgID, ChannelID: ch.ID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// DeleteChannel removes one of the org's channels. Rules that still list
// it simply skip it at delivery time.
func (r *Repo) DeleteChannel(ctx context.Context, orgID, id string) error {
	n, err := r.q.DeleteNotificationChannel(ctx, r.tx.Querier(ctx), deleteChanParams{OrganizationID: orgID, ChannelID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// RecordDelivery stores a send's outcome on the channel: success clears
// the last error and stamps last_sent_at; a failure keeps last_sent_at.
func (r *Repo) RecordDelivery(ctx context.Context, channelID string, at time.Time, sendErr string) error {
	if sendErr == "" {
		ts := stamp(at)
		return r.q.RecordChannelSuccess(ctx, r.tx.Querier(ctx), successParams{LastSentAt: &ts, ChannelID: channelID})
	}
	return r.q.RecordChannelFailure(ctx, r.tx.Querier(ctx), failureParams{LastError: sendErr, ChannelID: channelID})
}

func (r *Repo) channel(row channelRow) alert.Channel {
	ch := alert.Channel{
		ID: row.ChannelID, OrgID: row.OrganizationID, Name: row.Name, Service: row.Service,
		Fields: map[string]string{}, IsDefault: row.IsDefault, Enabled: row.Enabled,
		LastError: row.LastError, CreatedAt: utc(row.CreatedAt), URLEncrypted: row.UrlEncrypted,
	}
	if err := json.Unmarshal([]byte(row.Fields), &ch.Fields); err != nil {
		r.log.Warn("alerts: undecodable channel fields", "channel_id", row.ChannelID, "error", err)
		ch.Fields = map[string]string{}
	}
	if row.LastSentAt != nil {
		t := utc(*row.LastSentAt)
		ch.LastSentAt = &t
	}
	return ch
}

func encodeFields(f map[string]string) string {
	if len(f) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(f) // a map[string]string always marshals
	return string(b)
}
```

`internal/infra/db/repository/alert/rules.go`:

```go
package alert

import (
	"context"
	"time"

	"winterflow/internal/domain/alert"
)

// ListRules returns an org's rules: server rules first, then by metric
// and threshold.
func (r *Repo) ListRules(ctx context.Context, orgID string) ([]alert.Rule, error) {
	rows, err := r.q.ListAlertRules(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]alert.Rule, 0, len(rows))
	for _, row := range rows {
		out = append(out, r.rule(row))
	}
	return out, nil
}

// GetRule returns one of the org's rules or alert.ErrNotFound.
func (r *Repo) GetRule(ctx context.Context, orgID, id string) (alert.Rule, error) {
	row, err := r.q.GetAlertRule(ctx, r.tx.Querier(ctx), getRuleParams{OrganizationID: orgID, RuleID: id})
	if err != nil {
		return alert.Rule{}, notFound(err)
	}
	return r.rule(row), nil
}

// CreateRule stores a new rule, minting its id and creation time.
func (r *Repo) CreateRule(ctx context.Context, rule alert.Rule) (alert.Rule, error) {
	rule.ID = r.newID()
	rule.CreatedAt = time.Now().UTC().Truncate(time.Second)
	return rule, r.insertRule(ctx, rule)
}

func (r *Repo) insertRule(ctx context.Context, rule alert.Rule) error {
	return r.q.InsertAlertRule(ctx, r.tx.Querier(ctx), insertRuleParams{
		RuleID: rule.ID, OrganizationID: rule.OrgID, TargetKind: string(rule.TargetKind), Metric: string(rule.Metric),
		Threshold: rule.Threshold, DurationMinutes: int64(rule.DurationMinutes), ChannelIds: encodeIDs(rule.ChannelIDs),
		Enabled: rule.Enabled, CreatedAt: stamp(rule.CreatedAt),
	})
}

// UpdateRule rewrites a rule's threshold, duration, channels and enabled
// flag (kind and metric are fixed); alert.ErrNotFound when the org has no
// such rule.
func (r *Repo) UpdateRule(ctx context.Context, rule alert.Rule) error {
	n, err := r.q.UpdateAlertRule(ctx, r.tx.Querier(ctx), updateRuleParams{
		Threshold: rule.Threshold, DurationMinutes: int64(rule.DurationMinutes), ChannelIds: encodeIDs(rule.ChannelIDs),
		Enabled: rule.Enabled, OrganizationID: rule.OrgID, RuleID: rule.ID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// DeleteRule removes a rule; its overrides and states cascade, its
// history stays.
func (r *Repo) DeleteRule(ctx context.Context, orgID, id string) error {
	n, err := r.q.DeleteAlertRule(ctx, r.tx.Querier(ctx), deleteRuleParams{OrganizationID: orgID, RuleID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// SeedDefaults gives every org that has not had them yet the default rules
// (alert.DefaultRules) and reports how many orgs it seeded. Marking the
// org and inserting its rules share one transaction, and the mark is
// compare-and-set, so concurrent API replicas seed an org once. The flag
// also makes deleting a default permanent.
func (r *Repo) SeedDefaults(ctx context.Context) (int, error) {
	orgs, err := r.q.ListUnseededOrganizationIDs(ctx, r.tx.Querier(ctx))
	if err != nil {
		return 0, err
	}
	seeded := 0
	for _, orgID := range orgs {
		err := r.tx.WithTx(ctx, func(ctx context.Context) error {
			n, err := r.q.MarkAlertDefaultsSeeded(ctx, r.tx.Querier(ctx), orgID)
			if err != nil || n == 0 {
				return err
			}
			now := time.Now().UTC().Truncate(time.Second)
			for _, rule := range alert.DefaultRules() {
				rule.ID, rule.OrgID, rule.CreatedAt = r.newID(), orgID, now
				if err := r.insertRule(ctx, rule); err != nil {
					return err
				}
			}
			seeded++
			return nil
		})
		if err != nil {
			return seeded, err
		}
	}
	return seeded, nil
}

// ServerOrgID returns the org that owns a server, or alert.ErrNotFound.
func (r *Repo) ServerOrgID(ctx context.Context, serverID string) (string, error) {
	org, err := r.q.GetAlertServerOrganizationID(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return "", notFound(err)
	}
	return org, nil
}

// ListOverrides returns every override of the org's rules.
func (r *Repo) ListOverrides(ctx context.Context, orgID string) ([]alert.Override, error) {
	rows, err := r.q.ListAlertOverrides(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	return r.overrides(rows), nil
}

// TargetOverrides returns the overrides of one target, by rule.
func (r *Repo) TargetOverrides(ctx context.Context, t alert.Target) ([]alert.Override, error) {
	rows, err := r.q.ListTargetAlertOverrides(ctx, r.tx.Querier(ctx), targetOvrParams{ServerID: t.ServerID, AppID: t.AppID})
	if err != nil {
		return nil, err
	}
	return r.overrides(rows), nil
}

// SaveOverride creates or replaces a (rule, target) override.
func (r *Repo) SaveOverride(ctx context.Context, o alert.Override) error {
	var dur *int64
	if o.DurationMinutes != nil {
		d := int64(*o.DurationMinutes)
		dur = &d
	}
	return r.q.UpsertAlertOverride(ctx, r.tx.Querier(ctx), upsertOvrParams{
		RuleID: o.RuleID, ServerID: o.ServerID, AppID: o.AppID, Disabled: o.Disabled,
		Threshold: o.Threshold, DurationMinutes: dur, ChannelIds: encodeIDs(o.ChannelIDs),
	})
}

// DeleteOverride removes a (rule, target) override; deleting one that does
// not exist is not an error.
func (r *Repo) DeleteOverride(ctx context.Context, k alert.Key) error {
	_, err := r.q.DeleteAlertOverride(ctx, r.tx.Querier(ctx), deleteOvrParams{RuleID: k.RuleID, ServerID: k.ServerID, AppID: k.AppID})
	return err
}

func (r *Repo) rule(row ruleRow) alert.Rule {
	return alert.Rule{
		ID: row.RuleID, OrgID: row.OrganizationID, TargetKind: alert.TargetKind(row.TargetKind),
		Metric: alert.Metric(row.Metric), Threshold: row.Threshold, DurationMinutes: int(row.DurationMinutes),
		ChannelIDs: r.decodeIDs(row.ChannelIds), Enabled: row.Enabled, CreatedAt: utc(row.CreatedAt),
	}
}

func (r *Repo) overrides(rows []overrideRow) []alert.Override {
	out := make([]alert.Override, 0, len(rows))
	for _, row := range rows {
		o := alert.Override{
			RuleID: row.RuleID, ServerID: row.ServerID, AppID: row.AppID, Disabled: row.Disabled,
			Threshold: row.Threshold, ChannelIDs: r.decodeIDs(row.ChannelIds),
		}
		if row.DurationMinutes != nil {
			d := int(*row.DurationMinutes)
			o.DurationMinutes = &d
		}
		out = append(out, o)
	}
	return out
}
```

`internal/infra/db/repository/alert/states.go`:

```go
package alert

import (
	"context"

	"winterflow/internal/domain/alert"
)

// Fire moves k to firing and, if this call made the transition, appends
// ev in the same transaction. won is false when k was already firing
// (another API replica got there first, or a replay).
func (r *Repo) Fire(ctx context.Context, k alert.Key, value, threshold float64, ev alert.Event) (won bool, err error) {
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.FireAlertState(ctx, r.tx.Querier(ctx), fireParams{
			RuleID: k.RuleID, ServerID: k.ServerID, AppID: k.AppID,
			Since: stamp(ev.CreatedAt), Value: value, Threshold: threshold,
		})
		if err != nil || n == 0 {
			return err
		}
		won = true
		return r.insertEvent(ctx, ev)
	})
	return won, err
}

// Resolve moves k from firing to ok and, if this call made the
// transition, appends ev in the same transaction.
func (r *Repo) Resolve(ctx context.Context, k alert.Key, value float64, ev alert.Event) (won bool, err error) {
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.ResolveAlertState(ctx, r.tx.Querier(ctx), resolveParams{
			Since: stamp(ev.CreatedAt), Value: value, RuleID: k.RuleID, ServerID: k.ServerID, AppID: k.AppID,
		})
		if err != nil || n == 0 {
			return err
		}
		won = true
		return r.insertEvent(ctx, ev)
	})
	return won, err
}

// ClearState forgets k's state without an event (the rule or override was
// disabled while firing).
func (r *Repo) ClearState(ctx context.Context, k alert.Key) error {
	return r.q.DeleteAlertState(ctx, r.tx.Querier(ctx), deleteStateParam{RuleID: k.RuleID, ServerID: k.ServerID, AppID: k.AppID})
}

// FiringStates returns every firing (rule, target) with its rule's kind and
// metric: the evaluator's state after a restart.
func (r *Repo) FiringStates(ctx context.Context) ([]alert.StateRow, error) {
	rows, err := r.q.ListFiringAlertStates(ctx, r.tx.Querier(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]alert.StateRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, alert.StateRow{
			Key:        alert.Key{RuleID: row.RuleID, Target: alert.Target{ServerID: row.ServerID, AppID: row.AppID}},
			TargetKind: alert.TargetKind(row.TargetKind), Metric: alert.Metric(row.Metric), State: alert.StateFiring,
			Since: utc(row.Since), Value: row.Value, Threshold: row.Threshold,
		})
	}
	return out, nil
}

// ListActive returns an org's firing alerts, newest first.
func (r *Repo) ListActive(ctx context.Context, orgID string) ([]alert.Active, error) {
	rows, err := r.q.ListOrgFiringAlertStates(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]alert.Active, 0, len(rows))
	for _, row := range rows {
		out = append(out, alert.Active{
			RuleID: row.RuleID, TargetKind: alert.TargetKind(row.TargetKind), Metric: alert.Metric(row.Metric),
			ServerID: row.ServerID, AppID: row.AppID, Since: utc(row.Since), Value: row.Value, Threshold: row.Threshold,
		})
	}
	return out, nil
}

// CountFiring returns the number of firing alerts per server (servers
// without any are absent).
func (r *Repo) CountFiring(ctx context.Context) (map[string]int, error) {
	rows, err := r.q.CountFiringAlertsByServer(ctx, r.tx.Querier(ctx))
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, row := range rows {
		out[row.ServerID] = int(row.Firing)
	}
	return out, nil
}

// Directory returns every server and app id with its name and owner: the
// evaluator's targets.
func (r *Repo) Directory(ctx context.Context) (alert.Directory, error) {
	servers, err := r.q.ListAlertDirectoryServers(ctx, r.tx.Querier(ctx))
	if err != nil {
		return alert.Directory{}, err
	}
	apps, err := r.q.ListAlertDirectoryApps(ctx, r.tx.Querier(ctx))
	if err != nil {
		return alert.Directory{}, err
	}
	d := alert.Directory{
		Servers: make(map[string]alert.ServerRef, len(servers)),
		Apps:    make(map[string]alert.AppRef, len(apps)),
	}
	for _, s := range servers {
		d.Servers[s.ServerID] = alert.ServerRef{ID: s.ServerID, OrgID: s.OrganizationID, Name: s.Name}
	}
	for _, a := range apps {
		d.Apps[a.AppID] = alert.AppRef{ID: a.AppID, ServerID: a.ServerID, Name: a.Name}
	}
	return d, nil
}
```

`internal/infra/db/repository/alert/events.go`:

```go
package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"winterflow/internal/domain/alert"
)

const (
	// EventRetention is how long history is kept.
	EventRetention = 90 * 24 * time.Hour
	// EventsPerOrg caps an org's history. ListOversizedAlertOrganizations
	// (query/*/alerts.sql) repeats this literal: keep them in step.
	EventsPerOrg = 2000
	// MaxEventsPage bounds one history page.
	MaxEventsPage = 200
)

// farFuture starts the first history page.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

func (r *Repo) insertEvent(ctx context.Context, ev alert.Event) error {
	return r.q.InsertAlertEvent(ctx, r.tx.Querier(ctx), insertEventParam{
		EventID: ev.ID, OrganizationID: ev.OrgID, RuleID: ev.RuleID, ServerID: ev.ServerID, AppID: ev.AppID,
		ServerName: ev.ServerName, AppName: ev.AppName, TargetKind: string(ev.TargetKind), Metric: string(ev.Metric),
		Kind: string(ev.Kind), Value: ev.Value, Threshold: ev.Threshold, Title: ev.Title, Message: ev.Message,
		CreatedAt: stamp(ev.CreatedAt),
	})
}

// ListEvents returns one history page, newest first. A zero BeforeAt
// starts at the newest event; Limit is clamped to 1..MaxEventsPage.
func (r *Repo) ListEvents(ctx context.Context, q alert.EventQuery) ([]alert.Event, error) {
	before := farFuture
	if !q.BeforeAt.IsZero() {
		before = q.BeforeAt
	}
	limit := min(max(q.Limit, 1), MaxEventsPage)
	rows, err := r.q.ListAlertEvents(ctx, r.tx.Querier(ctx), listEventsParams{
		OrganizationID: q.OrgID, ServerID: q.ServerID, AppID: q.AppID, RuleID: q.RuleID, Kind: string(q.Kind),
		BeforeAt: stamp(before), BeforeID: q.BeforeID, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]alert.Event, 0, len(rows))
	for _, row := range rows {
		ev := alert.Event{
			ID: row.EventID, OrgID: row.OrganizationID, RuleID: row.RuleID, ServerID: row.ServerID, AppID: row.AppID,
			ServerName: row.ServerName, AppName: row.AppName, TargetKind: alert.TargetKind(row.TargetKind),
			Metric: alert.Metric(row.Metric), Kind: alert.EventKind(row.Kind), Value: row.Value, Threshold: row.Threshold,
			Title: row.Title, Message: row.Message, Deliveries: []alert.Delivery{}, CreatedAt: utc(row.CreatedAt),
		}
		if err := json.Unmarshal([]byte(row.Deliveries), &ev.Deliveries); err != nil {
			r.log.Warn("alerts: undecodable deliveries", "event_id", row.EventID, "error", err)
			ev.Deliveries = []alert.Delivery{}
		}
		out = append(out, ev)
	}
	return out, nil
}

// SetDeliveries records an event's per-channel outcomes.
func (r *Repo) SetDeliveries(ctx context.Context, eventID string, ds []alert.Delivery) error {
	if ds == nil {
		ds = []alert.Delivery{}
	}
	b, err := json.Marshal(ds)
	if err != nil {
		return err
	}
	return r.q.SetAlertEventDeliveries(ctx, r.tx.Querier(ctx), deliveriesParams{Deliveries: string(b), EventID: eventID})
}

// PruneEvents deletes history older than EventRetention, then trims every
// org past EventsPerOrg to its newest EventsPerOrg; it reports the rows
// deleted.
func (r *Repo) PruneEvents(ctx context.Context, now time.Time) (int64, error) {
	total, err := r.q.DeleteAlertEventsBefore(ctx, r.tx.Querier(ctx), stamp(now.Add(-EventRetention)))
	if err != nil {
		return 0, err
	}
	orgs, err := r.q.ListOversizedAlertOrganizations(ctx, r.tx.Querier(ctx))
	if err != nil {
		return total, err
	}
	for _, orgID := range orgs {
		cut, err := r.q.GetAlertEventTrimCutoff(ctx, r.tx.Querier(ctx), cutoffParams{OrganizationID: orgID, Keep: EventsPerOrg})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return total, err
		}
		n, err := r.q.DeleteAlertEventsUpTo(ctx, r.tx.Querier(ctx), deleteUpToParams{
			OrganizationID: orgID, CutoffAt: cut.CreatedAt, CutoffID: cut.EventID,
		})
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
```

- [ ] **Step 7: Run the tests on both engines**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/infra/db/... && GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/db/...`
Expected: every package `ok` (SQLite).

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp make test-pg TEST_PG_ARGS='-run "Alert|Fire|Resolve|Channel|Rule|Override|Seed|Prune|Directory|Timestamps|Migrat"'`
Expected: `ok` for `internal/infra/db/...`. The contract package has no matching test yet. `TestPruneEventsByAgeAndCap` inserts 2,003 rows and takes about 15 s on Postgres.

- [ ] **Step 8: Commit**

```bash
git add internal/infra/db
git commit -m "feat(alerts): alert tables, queries and repository

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 4: Alert port, tier-1 history read, app-delete cleanup

**Files:**
- Create: `internal/domain/port/alert.go`
- Modify: `internal/infra/db/repository/alert/repo.go` (assert the port)
- Modify: `internal/domain/metrics/rollup.go` (`TargetBucket`), `internal/domain/port/metric.go` (`Tier1Range`), `internal/infra/db/repository/metric/repo.go`
- Modify: `internal/infra/db/repository/app/repo.go`, `sqlite.go`, `pgsql.go`
- Test: `internal/infra/db/repository/metric/repo_test.go`, `internal/infra/db/repository/app/repo_test.go`

**Interfaces:**
- Consumes: everything Task 3 produced.
- Produces:
  - `port.AlertRepository`: exactly the method set of `alertrepo.Repo` listed in Task 3;
  - `type metrics.TargetBucket struct{ ServerID, AppID string; Bucket }`;
  - `port.MetricRepository.Tier1Range(ctx, from, to time.Time) ([]metrics.TargetBucket, error)`, implemented by `metricrepo.Repo`;
  - `AppRepository.DeleteApp`, and the reconcile in `SyncApps`, also delete the app's `alert_overrides`, `alert_states` and `alert_events` in their transaction.

- [ ] **Step 1: Write the failing tests**

Append to `internal/infra/db/repository/metric/repo_test.go`:

```go
func TestTier1RangeReturnsAllTargets(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustInsert(t, ctx, repo, srv1, t0.Add(time.Minute), metrics.Values{"cpu": 1}, map[string]metrics.Values{appA: {"cpu": 2}})
	mustInsert(t, ctx, repo, srv2, t0, metrics.Values{"cpu": 3}, nil)
	mustInsert(t, ctx, repo, srv1, t0.Add(-2*time.Hour), metrics.Values{"cpu": 9}, nil) // before the range
	got, err := repo.Tier1Range(ctx, t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		server, app string
		cpu         float64
	}{{srv1, "", 1}, {srv1, appA, 2}, {srv2, "", 3}}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v", got)
	}
	for i, w := range want {
		if got[i].ServerID != w.server || got[i].AppID != w.app || got[i].Data["cpu"] != w.cpu {
			t.Errorf("row %d = %+v, want %+v", i, got[i], w)
		}
	}
}
```

Append to `internal/infra/db/repository/app/repo_test.go` (its `newRepo`, `org1`, `srv1`, `appA`, `appB` already exist):

```go
// seedAlertData writes a rule plus an override, a state and an event for
// target (server, app) with raw SQL (the alert repository is another
// package).
func seedAlertData(t *testing.T, d *db.DB, orgID, ruleID, serverID, appID string) {
	t.Helper()
	dbtest.Exec(t, d, `INSERT INTO alert_rules (rule_id, organization_id, target_kind, metric, threshold, duration_minutes, enabled, created_at)
		VALUES (?, ?, 'app', 'cpu', 90, 10, TRUE, '2026-10-11 12:00:00') ON CONFLICT DO NOTHING`, ruleID, orgID)
	dbtest.Exec(t, d, `INSERT INTO alert_overrides (rule_id, server_id, app_id, disabled) VALUES (?, ?, ?, TRUE)`, ruleID, serverID, appID)
	dbtest.Exec(t, d, `INSERT INTO alert_states (rule_id, server_id, app_id, state, since) VALUES (?, ?, ?, 'firing', '2026-10-11 12:00:00')`,
		ruleID, serverID, appID)
	dbtest.Exec(t, d, `INSERT INTO alert_events (event_id, organization_id, rule_id, server_id, app_id, target_kind, metric, kind, title, message, created_at)
		VALUES (?, ?, ?, ?, ?, 'app', 'cpu', 'fired', 't', 'm', '2026-10-11 12:00:00')`, appID, orgID, ruleID, serverID, appID) // the app id doubles as a unique event id
}

func TestDeleteAppRemovesItsAlertData(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := dbtest.ID(40)
	for _, id := range []string{appA, appB} {
		if err := repo.SaveApp(ctx, model.App{ID: id, ServerID: srv1, Name: id}); err != nil {
			t.Fatal(err)
		}
		seedAlertData(t, d, org1, rule, srv1, id)
	}
	alertRows := func(appID string) string {
		return dbtest.QueryString(t, d, `SELECT CAST(
			(SELECT COUNT(*) FROM alert_overrides WHERE app_id = ?) +
			(SELECT COUNT(*) FROM alert_states WHERE app_id = ?) +
			(SELECT COUNT(*) FROM alert_events WHERE app_id = ?) AS TEXT)`, appID, appID, appID)
	}
	if err := repo.DeleteApp(ctx, appA); err != nil {
		t.Fatal(err)
	}
	if got := alertRows(appA); got != "0" {
		t.Fatalf("deleted app keeps %s alert rows", got)
	}
	if got := alertRows(appB); got != "3" {
		t.Fatalf("other app's alert rows = %s, want 3", got)
	}
	if err := repo.SyncApps(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if got := alertRows(appB); got != "0" {
		t.Fatalf("reconciled-away app keeps %s alert rows", got)
	}
}
```

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/db/repository/metric/ ./internal/infra/db/repository/app/`
Expected: FAIL (`repo.Tier1Range undefined`; `deleted app keeps 3 alert rows`).

- [ ] **Step 2: Write the port**

`internal/domain/port/alert.go`:

```go
package port

import (
	"context"
	"time"

	"winterflow/internal/domain/alert"
)

// AlertRepository persists alerting (migration 0012): an org's
// notification channels (URL stored encrypted by the caller), its rules
// and per-target overrides, the state of each (rule, target) and the event
// history. Lookups scoped by org return alert.ErrNotFound for another
// org's ids.
type AlertRepository interface {
	ListChannels(ctx context.Context, orgID string) ([]alert.Channel, error)
	GetChannel(ctx context.Context, orgID, id string) (alert.Channel, error)
	// CreateChannel mints the id and creation time.
	CreateChannel(ctx context.Context, ch alert.Channel) (alert.Channel, error)
	UpdateChannel(ctx context.Context, ch alert.Channel) error
	DeleteChannel(ctx context.Context, orgID, id string) error
	// RecordDelivery stores a send outcome on the channel ("" = success).
	RecordDelivery(ctx context.Context, channelID string, at time.Time, sendErr string) error

	ListRules(ctx context.Context, orgID string) ([]alert.Rule, error)
	GetRule(ctx context.Context, orgID, id string) (alert.Rule, error)
	// CreateRule mints the id and creation time.
	CreateRule(ctx context.Context, r alert.Rule) (alert.Rule, error)
	// UpdateRule rewrites threshold, duration, channels and enabled.
	UpdateRule(ctx context.Context, r alert.Rule) error
	DeleteRule(ctx context.Context, orgID, id string) error
	// SeedDefaults gives every not-yet-seeded org the default rules once.
	SeedDefaults(ctx context.Context) (int, error)

	ListOverrides(ctx context.Context, orgID string) ([]alert.Override, error)
	TargetOverrides(ctx context.Context, t alert.Target) ([]alert.Override, error)
	SaveOverride(ctx context.Context, o alert.Override) error
	DeleteOverride(ctx context.Context, k alert.Key) error
	// ServerOrgID is the org owning a server (alert.ErrNotFound if none).
	ServerOrgID(ctx context.Context, serverID string) (string, error)

	// Fire and Resolve are compare-and-set transitions; won reports
	// whether this call made it (and appended ev).
	Fire(ctx context.Context, k alert.Key, value, threshold float64, ev alert.Event) (won bool, err error)
	Resolve(ctx context.Context, k alert.Key, value float64, ev alert.Event) (won bool, err error)
	ClearState(ctx context.Context, k alert.Key) error
	FiringStates(ctx context.Context) ([]alert.StateRow, error)
	ListActive(ctx context.Context, orgID string) ([]alert.Active, error)
	// CountFiring is the number of firing alerts per server id.
	CountFiring(ctx context.Context) (map[string]int, error)

	ListEvents(ctx context.Context, q alert.EventQuery) ([]alert.Event, error)
	SetDeliveries(ctx context.Context, eventID string, ds []alert.Delivery) error
	// PruneEvents applies the 90-day / 2,000-per-org history bounds.
	PruneEvents(ctx context.Context, now time.Time) (int64, error)

	// Directory lists every server and app with names and owners.
	Directory(ctx context.Context) (alert.Directory, error)
}
```

In `internal/infra/db/repository/alert/repo.go`, import `"winterflow/internal/domain/port"` and append:

```go
var _ port.AlertRepository = (*Repo)(nil)
```

- [ ] **Step 3: Add the tier-1 history read**

Append to `internal/domain/metrics/rollup.go`:

```go
// TargetBucket is one stored row with its target: the server (AppID "")
// or one of its apps.
type TargetBucket struct {
	ServerID string
	AppID    string
	Bucket
}
```

In `internal/domain/port/metric.go`, add to `MetricRepository` after `Prune`:

```go
	// Tier1Range returns every target's tier-1 rows in [from, to), ordered
	// by server, app and time: the alert evaluator's warm start.
	Tier1Range(ctx context.Context, from, to time.Time) ([]metrics.TargetBucket, error)
```

In `internal/infra/db/repository/metric/repo.go`, add before `hydrate`:

```go
// Tier1Range returns every target's tier-1 rows in [from, to), ordered by
// server, app and bucket.
func (r *Repo) Tier1Range(ctx context.Context, from, to time.Time) ([]metrics.TargetBucket, error) {
	rows, err := r.q.ListMetricSamplesInRange(ctx, r.tx.Querier(ctx), rangeParams{
		Tier: int64(metrics.Tier1), FromTime: types.DateTime(from.UTC()), ToTime: types.DateTime(to.UTC()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]metrics.TargetBucket, 0, len(rows))
	for _, row := range rows {
		if b, ok := r.hydrate(row); ok {
			out = append(out, metrics.TargetBucket{ServerID: row.ServerID, AppID: row.AppID, Bucket: b})
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Delete an app's alert data with the app**

In `internal/infra/db/repository/app/repo.go`, add to `querier`:

```go
	DeleteAlertOverridesByApp(ctx context.Context, db backend.DBTX, appID string) error
	DeleteAlertStatesByApp(ctx context.Context, db backend.DBTX, appID string) error
	DeleteAlertEventsByApp(ctx context.Context, db backend.DBTX, appID string) error
```

Replace the `DeleteApp` doc comment and `deleteApp` with:

```go
// DeleteApp removes the app row with its monitoring samples and alert
// overrides, states and history in one transaction (no error when it does
// not exist).
```

```go
// deleteApp drops the app's samples and alert data, then the row; ctx must
// carry the caller's transaction. An empty id is a no-op: app_id = ” marks
// the servers' own metric rows and alert targets.
func (r *Repo) deleteApp(ctx context.Context, appID string) error {
	if appID == "" {
		return nil
	}
	q := r.tx.Querier(ctx)
	for _, del := range []func(context.Context, backend.DBTX, string) error{
		r.q.DeleteMetricSamplesByApp,
		r.q.DeleteAlertOverridesByApp,
		r.q.DeleteAlertStatesByApp,
		r.q.DeleteAlertEventsByApp,
	} {
		if err := del(ctx, q, appID); err != nil {
			return err
		}
	}
	return r.q.DeleteApp(ctx, q, appID)
}
```

Append to `internal/infra/db/repository/app/sqlite.go`:

```go
func (sqliteQuerier) DeleteAlertOverridesByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return sqlitegen.New(db).DeleteAlertOverridesByApp(ctx, appID)
}

func (sqliteQuerier) DeleteAlertStatesByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return sqlitegen.New(db).DeleteAlertStatesByApp(ctx, appID)
}

func (sqliteQuerier) DeleteAlertEventsByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return sqlitegen.New(db).DeleteAlertEventsByApp(ctx, appID)
}
```

and to `internal/infra/db/repository/app/pgsql.go` the same three methods on `pgsqlQuerier`, each calling `pgsqlgen.New(db).<Name>(ctx, appID)`.

- [ ] **Step 5: Run the tests on both engines**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/... && GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/db/... ./internal/domain/...`
Expected: all `ok`.

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp make test-pg TEST_PG_ARGS='-run "DeleteApp|Tier1Range"'`
Expected: `ok` for the `app` and `metric` repository packages.

- [ ] **Step 6: Commit**

```bash
git add internal/domain internal/infra/db/repository
git commit -m "feat(alerts): alert port, tier-1 history read, app-delete cleanup

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 5: The evaluator

**Files:**
- Create: `internal/domain/service/alerting/evaluator.go`
- Test: `internal/domain/service/alerting/fakes_test.go` (shared with Task 6), `internal/domain/service/alerting/evaluator_test.go`

**Interfaces:**
- Consumes:
  - package `alert` (Task 1);
  - `metrics.Values`, `metrics.TargetBucket` (Task 4);
  - `logger.Logger`, `util.GenerateID`.
- Produces (package `winterflow/internal/domain/service/alerting`):
  - `type Store interface{ ListRules; ListOverrides; FiringStates; Fire; Resolve; ClearState; Directory }`, signatures as in `port.AlertRepository`;
  - `type History interface{ Tier1Range(ctx, from, to time.Time) ([]metrics.TargetBucket, error) }`;
  - `type Job struct{ EventID, OrgID string; ChannelIDs []string; Title, Body string }`;
  - `type Queue interface{ Enqueue(Job) bool }`;
  - `type Config struct{ Store Store; History History; Queue Queue; Publish func(ctx, serverID string, n alert.Notice); WebURL string; DownGrace time.Duration; Log *logger.Logger; Now func() time.Time; NewID func() string }`;
  - `func NewEvaluator(Config) *Evaluator`;
  - `(*Evaluator) Load(ctx) error`;
  - `ObserveHeartbeat(ctx, serverID)`;
  - `ObserveMetrics(ctx, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values)`;
  - `ObserveAppStatus(ctx, serverID string, problematic map[string]bool)`;
  - `Sweep(ctx)`.

Behaviour (decisions 1 and 3 to 7):
- One mutex guards all state. The events goroutine and the sweep goroutine both call in.
- Every store call has `storeTimeout` (5 s).
- A store error leaves the local state unchanged, so the next evaluation retries.
- The directory is reloaded after `directoryTTL` (60 s), or after 5 s when an event names an unknown server. Each reload prunes buffers, problem clocks, firing entries and `heard` of deleted targets.

- [ ] **Step 1: Write the failing tests**

`internal/domain/service/alerting/fakes_test.go`:

```go
package alerting

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/metrics"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})

// t0 is a minute-aligned instant.
var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

const (
	org = "org-1"
	srv = "srv-1"
	app = "app-1"
)

// fakeStore is an in-memory alert repository with the real
// compare-and-set semantics of Fire and Resolve.
type fakeStore struct {
	mu        sync.Mutex
	rules     []alert.Rule
	overrides []alert.Override
	states    map[alert.Key]alert.State
	events    []alert.Event
	cleared   []alert.Key
	channels  []alert.Channel
	delivered map[string][]alert.Delivery
	records   []string // "channel:error"
	dir       alert.Directory
	fail      error
}

func newStore() *fakeStore {
	return &fakeStore{
		states:    map[alert.Key]alert.State{},
		delivered: map[string][]alert.Delivery{},
		dir: alert.Directory{
			Servers: map[string]alert.ServerRef{srv: {ID: srv, OrgID: org, Name: "prod-1"}},
			Apps:    map[string]alert.AppRef{app: {ID: app, ServerID: srv, Name: "nextcloud"}},
		},
	}
}

func (s *fakeStore) addRule(r alert.Rule) alert.Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.OrgID, r.Enabled = org, true
	if r.ID == "" {
		r.ID = fmt.Sprintf("rule-%d", len(s.rules)+1)
	}
	s.rules = append(s.rules, r)
	return r
}

func (s *fakeStore) ListRules(_ context.Context, orgID string) ([]alert.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []alert.Rule
	for _, r := range s.rules {
		if r.OrgID == orgID {
			out = append(out, r)
		}
	}
	return out, s.fail
}

func (s *fakeStore) ListOverrides(context.Context, string) ([]alert.Override, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]alert.Override(nil), s.overrides...), nil
}

func (s *fakeStore) FiringStates(context.Context) ([]alert.StateRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []alert.StateRow
	for k, st := range s.states {
		if st != alert.StateFiring {
			continue
		}
		for _, r := range s.rules {
			if r.ID == k.RuleID {
				out = append(out, alert.StateRow{Key: k, TargetKind: r.TargetKind, Metric: r.Metric, State: st})
			}
		}
	}
	return out, nil
}

func (s *fakeStore) Fire(_ context.Context, k alert.Key, _, _ float64, ev alert.Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return false, s.fail
	}
	if s.states[k] == alert.StateFiring {
		return false, nil
	}
	s.states[k] = alert.StateFiring
	s.events = append(s.events, ev)
	return true, nil
}

func (s *fakeStore) Resolve(_ context.Context, k alert.Key, _ float64, ev alert.Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return false, s.fail
	}
	if s.states[k] != alert.StateFiring {
		return false, nil
	}
	s.states[k] = alert.StateOK
	s.events = append(s.events, ev)
	return true, nil
}

func (s *fakeStore) ClearState(_ context.Context, k alert.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, k)
	s.cleared = append(s.cleared, k)
	return nil
}

func (s *fakeStore) Directory(context.Context) (alert.Directory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir, nil
}

func (s *fakeStore) ListChannels(_ context.Context, orgID string) ([]alert.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []alert.Channel
	for _, c := range s.channels {
		if c.OrgID == orgID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *fakeStore) RecordDelivery(_ context.Context, channelID string, _ time.Time, sendErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, channelID+":"+sendErr)
	return nil
}

func (s *fakeStore) SetDeliveries(_ context.Context, eventID string, ds []alert.Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivered[eventID] = ds
	return nil
}

func (s *fakeStore) eventKinds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, ev := range s.events {
		out = append(out, string(ev.Kind)+":"+string(ev.Metric)+":"+ev.AppID)
	}
	return out
}

// fakeQueue records jobs.
type fakeQueue struct {
	mu   sync.Mutex
	jobs []Job
}

func (q *fakeQueue) Enqueue(j Job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, j)
	return true
}

func (q *fakeQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

// fakeHistory serves Tier1Range from a fixed slice.
type fakeHistory []metrics.TargetBucket

func (h fakeHistory) Tier1Range(context.Context, time.Time, time.Time) ([]metrics.TargetBucket, error) {
	return h, nil
}

// clock is a settable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// harness is an evaluator over a fake store, queue and SSE recorder.
type harness struct {
	store   *fakeStore
	queue   *fakeQueue
	clock   *clock
	notices []alert.Notice
	ev      *Evaluator
}

func newHarness(store *fakeStore) *harness {
	h := &harness{store: store, queue: &fakeQueue{}, clock: &clock{t: t0}}
	n := 0
	h.ev = NewEvaluator(Config{
		Store: store, Queue: h.queue, WebURL: "https://wf.example", DownGrace: 90 * time.Second, Log: testLog,
		Now:     h.clock.now,
		NewID:   func() string { n++; return fmt.Sprintf("ev-%p-%d", h, n) },
		Publish: func(_ context.Context, _ string, notice alert.Notice) { h.notices = append(h.notices, notice) },
	})
	return h
}

// sample feeds one minute of host CPU (and optionally app values) at the
// clock's time, then advances the clock a minute.
func (h *harness) sample(cpu float64, apps map[string]metrics.Values) {
	h.ev.ObserveMetrics(context.Background(), srv, h.clock.t, metrics.Values{"cpu": cpu, "mem_used": 1e9, "mem_total": 8e9}, apps)
	h.clock.t = h.clock.t.Add(time.Minute)
}

var errStore = errors.New("db down")
```

`internal/domain/service/alerting/evaluator_test.go`:

```go
package alerting

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/metrics"
)

func cpuRule(s *fakeStore, threshold float64, minutes int) alert.Rule {
	return s.addRule(alert.Rule{TargetKind: alert.TargetServer, Metric: alert.MetricCPU, Threshold: threshold, DurationMinutes: minutes})
}

func TestThresholdFiresAndResolves(t *testing.T) {
	store := newStore()
	rule := cpuRule(store, 90, 10)
	h := newHarness(store)
	for i := 0; i < 7; i++ {
		h.sample(95, nil)
	}
	if len(store.events) != 0 {
		t.Fatalf("fired on 7 of 10 samples: %v", store.eventKinds())
	}
	h.sample(95, nil) // 8 of 10: enough coverage
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:cpu:"}) {
		t.Fatalf("events = %v", got)
	}
	ev := store.events[0]
	if ev.Title != "🔥 prod-1 · CPU 95% > 90% for 10m" || ev.ServerName != "prod-1" || ev.Threshold != 90 ||
		!strings.Contains(ev.Message, "https://wf.example/server/srv-1/monitoring") {
		t.Fatalf("event = %+v", ev)
	}
	for i := 0; i < 3; i++ { // keeps firing, no repeats
		h.sample(95, nil)
	}
	h.sample(50, nil) // last 10: 9×95 + 50 → 90.5, still above
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
	h.sample(50, nil) // 8×95 + 2×50 → 86
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:cpu:", "resolved:cpu:"}) {
		t.Fatalf("events = %v", got)
	}
	if h.queue.len() != 2 || len(h.notices) != 2 || h.notices[1].Kind != alert.EventResolved || h.notices[1].RuleID != rule.ID {
		t.Fatalf("jobs=%d notices=%+v", h.queue.len(), h.notices)
	}
	if h.queue.jobs[0].ChannelIDs != nil || h.queue.jobs[0].OrgID != org {
		t.Fatalf("job = %+v", h.queue.jobs[0])
	}
}

func TestThresholdKeepsStateWithoutCoverage(t *testing.T) {
	store := newStore()
	cpuRule(store, 90, 5)
	h := newHarness(store)
	for i := 0; i < 5; i++ {
		h.sample(99, nil)
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
	// The server goes quiet for 20 minutes, then one low sample: one point
	// in a 5-minute window is not a decision either way.
	h.clock.t = h.clock.t.Add(20 * time.Minute)
	h.sample(10, nil)
	if len(store.events) != 1 {
		t.Fatalf("decided on a sparse window: %v", store.eventKinds())
	}
	// A sample without the metric (first one after a restart has no CPU)
	// does not count either.
	h.ev.ObserveMetrics(context.Background(), srv, h.clock.t, metrics.Values{"mem_used": 1}, nil)
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
}

func TestOverrideChangesThresholdAndDisables(t *testing.T) {
	store := newStore()
	rule := cpuRule(store, 90, 2)
	high := 99.0
	store.overrides = []alert.Override{{RuleID: rule.ID, ServerID: srv, Threshold: &high, ChannelIDs: []string{"ch-9"}}}
	h := newHarness(store)
	h.sample(95, nil)
	h.sample(95, nil)
	if len(store.events) != 0 {
		t.Fatalf("override threshold ignored: %v", store.eventKinds())
	}
	h.sample(100, nil)
	h.sample(100, nil)
	if len(store.events) != 1 || store.events[0].Threshold != 99 || h.queue.jobs[0].ChannelIDs[0] != "ch-9" {
		t.Fatalf("events=%v jobs=%+v", store.eventKinds(), h.queue.jobs)
	}
	// Disabling while firing clears the state silently.
	store.overrides[0].Disabled = true
	h.sample(100, nil)
	if len(store.events) != 1 || len(store.cleared) != 1 || h.queue.len() != 1 {
		t.Fatalf("events=%v cleared=%v jobs=%d", store.eventKinds(), store.cleared, h.queue.len())
	}
}

func TestAppMetricsUseHostMemoryAndNames(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetApp, Metric: alert.MetricMemory, Threshold: 50, DurationMinutes: 1})
	h := newHarness(store)
	// No limit: 6 GB of the host's 8 GB = 75 %. The unknown app is ignored.
	h.sample(10, map[string]metrics.Values{app: {"mem_used": 6e9}, "ghost": {"mem_used": 8e9}})
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:memory:app-1"}) {
		t.Fatalf("events = %v", got)
	}
	if ev := store.events[0]; ev.AppName != "nextcloud" || !strings.HasPrefix(ev.Title, "🔥 prod-1 / nextcloud · Memory 75%") ||
		!strings.Contains(ev.Message, "/server/srv-1/app/app-1?tab=metrics") {
		t.Fatalf("event = %+v", ev)
	}
}

func TestDownFiresAfterGraceAndResolvesOnEvent(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetServer, Metric: alert.MetricDown, DurationMinutes: 5})
	h := newHarness(store)
	ctx := context.Background()
	h.ev.ObserveHeartbeat(ctx, srv)
	h.clock.t = t0.Add(4*time.Minute + 59*time.Second)
	h.ev.Sweep(ctx)
	if len(store.events) != 0 {
		t.Fatalf("fired early: %v", store.eventKinds())
	}
	h.clock.t = t0.Add(5 * time.Minute)
	h.ev.Sweep(ctx)
	h.ev.Sweep(ctx) // no repeat
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:down:"}) || store.events[0].Title != "🔥 prod-1 is down for 5m" {
		t.Fatalf("events = %v (%q)", got, store.events[0].Title)
	}
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: false})
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:down:", "resolved:down:"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestDownWaitsForGraceOnShortRules(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetServer, Metric: alert.MetricDown, DurationMinutes: 1})
	h := newHarness(store)
	h.ev.ObserveHeartbeat(context.Background(), srv)
	h.clock.t = t0.Add(80 * time.Second) // past 1 min, inside the 90 s grace
	h.ev.Sweep(context.Background())
	if len(store.events) != 0 {
		t.Fatalf("fired inside the liveness grace: %v", store.eventKinds())
	}
	h.clock.t = t0.Add(90 * time.Second)
	h.ev.Sweep(context.Background())
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
}

func TestDownCountsFromStartWhenNeverHeard(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetServer, Metric: alert.MetricDown, DurationMinutes: 5})
	h := newHarness(store)
	h.clock.t = t0.Add(10 * time.Minute)
	if err := h.ev.Load(context.Background()); err != nil { // the API starts at t0+10m
		t.Fatal(err)
	}
	h.clock.t = t0.Add(14 * time.Minute)
	h.ev.Sweep(context.Background())
	if len(store.events) != 0 {
		t.Fatalf("fired from before the start: %v", store.eventKinds())
	}
	h.clock.t = t0.Add(15 * time.Minute)
	h.ev.Sweep(context.Background())
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
}

func TestAppDownFiresAfterDuration(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetApp, Metric: alert.MetricDown, DurationMinutes: 5})
	h := newHarness(store)
	ctx := context.Background()
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	h.clock.t = t0.Add(3 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	if len(store.events) != 0 {
		t.Fatalf("fired early: %v", store.eventKinds())
	}
	h.clock.t = t0.Add(5 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:down:app-1"}) {
		t.Fatalf("events = %v", got)
	}
	// Stopped (not problematic) resolves; a fresh problem restarts the clock.
	h.clock.t = t0.Add(6 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: false})
	h.clock.t = t0.Add(7 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	h.clock.t = t0.Add(11 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:down:app-1", "resolved:down:app-1"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestTwoEvaluatorsOneEvent(t *testing.T) {
	store := newStore()
	cpuRule(store, 90, 1)
	a, b := newHarness(store), newHarness(store)
	a.sample(95, nil)
	b.sample(95, nil)
	if len(store.events) != 1 || a.queue.len()+b.queue.len() != 1 {
		t.Fatalf("events=%d jobs=%d+%d", len(store.events), a.queue.len(), b.queue.len())
	}
	// Each replica tells its own browsers.
	if len(a.notices) != 1 || len(b.notices) != 1 {
		t.Fatalf("notices = %d, %d", len(a.notices), len(b.notices))
	}
}

func TestLoadRestoresFiringAndBuffers(t *testing.T) {
	store := newStore()
	rule := cpuRule(store, 90, 10)
	store.states[alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv}}] = alert.StateFiring
	var hist fakeHistory
	for i := 0; i < 9; i++ {
		at := t0.Add(time.Duration(i-9) * time.Minute)
		hist = append(hist,
			metrics.TargetBucket{ServerID: srv, Bucket: metrics.Bucket{Start: at, Samples: 1, Data: metrics.Values{"cpu": 95, "mem_total": 8e9}}},
			metrics.TargetBucket{ServerID: srv, AppID: app, Bucket: metrics.Bucket{Start: at, Samples: 1, Data: metrics.Values{"mem_used": 6e9}}},
		)
	}
	h := newHarness(store)
	h.ev.cfg.History = hist
	if err := h.ev.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.sample(95, nil) // already firing: no repeat
	if len(store.events) != 0 {
		t.Fatalf("re-fired after restart: %v", store.eventKinds())
	}
	if got := h.ev.buffers[alert.Target{ServerID: srv, AppID: app}]; got == nil || got.Len() != 9 {
		t.Fatalf("app buffer not refilled: %+v", got)
	}
	avg, ok := h.ev.buffers[alert.Target{ServerID: srv, AppID: app}].Average(alert.MetricMemory, 5*time.Minute)
	if !ok || avg != 75 {
		t.Fatalf("app memory from host total = %v, %v", avg, ok)
	}
	for i := 0; i < 8; i++ {
		h.sample(10, nil)
	}
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"resolved:cpu:"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestStoreErrorRetriesNextSample(t *testing.T) {
	store := newStore()
	cpuRule(store, 90, 1)
	h := newHarness(store)
	store.fail = errStore
	h.sample(95, nil)
	store.fail = nil
	h.sample(95, nil)
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
}

func TestUnknownTargetsAreIgnoredAndPruned(t *testing.T) {
	store := newStore()
	cpuRule(store, 90, 1)
	h := newHarness(store)
	h.ev.ObserveMetrics(context.Background(), "srv-ghost", t0, metrics.Values{"cpu": 99}, nil)
	if len(store.events) != 0 || h.ev.buffers[alert.Target{ServerID: "srv-ghost"}] != nil {
		t.Fatal("an unknown server was evaluated")
	}
	h.sample(10, map[string]metrics.Values{app: {"cpu": 1}})
	if h.ev.buffers[alert.Target{ServerID: srv, AppID: app}] == nil {
		t.Fatal("known app not buffered")
	}
	// The app is deleted; the next directory reload drops its buffer.
	store.mu.Lock()
	store.dir.Apps = map[string]alert.AppRef{}
	store.mu.Unlock()
	h.clock.t = h.clock.t.Add(directoryTTL)
	h.sample(10, nil)
	if h.ev.buffers[alert.Target{ServerID: srv, AppID: app}] != nil {
		t.Fatal("deleted app's buffer kept")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/domain/service/alerting/`
Expected: FAIL to compile (`undefined: NewEvaluator`, `undefined: Job`, …).

- [ ] **Step 3: Write the evaluator**

`internal/domain/service/alerting/evaluator.go`:

```go
// Package alerting evaluates the org-wide alert rules against what the API
// hears from agents (metrics samples, app statuses, liveness) and delivers
// the resulting notifications through the org's channels. Spec:
// monitoring-and-alerts §3 "Evaluation" and "Delivery".
package alerting

import (
	"context"
	"sort"
	"sync"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/metrics"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

const (
	// storeTimeout bounds every evaluator and notifier DB call.
	storeTimeout = 5 * time.Second
	// directoryTTL is how long the target directory (servers, apps and
	// their names) is reused before it is reloaded.
	directoryTTL = time.Minute
	// directoryRetry is the least gap between reloads forced by an event
	// from a server the directory does not know yet.
	directoryRetry = 5 * time.Second
	// historyWindow is how far back Load refills the buffers.
	historyWindow = alert.BufferSize * time.Minute
)

// Store is the slice of the alert repository the evaluator needs.
type Store interface {
	ListRules(ctx context.Context, orgID string) ([]alert.Rule, error)
	ListOverrides(ctx context.Context, orgID string) ([]alert.Override, error)
	FiringStates(ctx context.Context) ([]alert.StateRow, error)
	Fire(ctx context.Context, k alert.Key, value, threshold float64, ev alert.Event) (bool, error)
	Resolve(ctx context.Context, k alert.Key, value float64, ev alert.Event) (bool, error)
	ClearState(ctx context.Context, k alert.Key) error
	Directory(ctx context.Context) (alert.Directory, error)
}

// History is the tier-1 sample read that warms the buffers at startup.
type History interface {
	Tier1Range(ctx context.Context, from, to time.Time) ([]metrics.TargetBucket, error)
}

// Job is one notification for the notifier. ChannelIDs follow
// alert.RouteChannels (nil = the org's default channels).
type Job struct {
	EventID    string
	OrgID      string
	ChannelIDs []string
	Title      string
	Body       string
}

// Queue accepts notification jobs without blocking.
type Queue interface {
	Enqueue(Job) bool
}

// Config wires an Evaluator.
type Config struct {
	Store Store
	// History warms the buffers in Load; nil (metrics off) starts empty.
	History History
	// Queue receives the notification of every transition this evaluator
	// recorded; nil sends nothing.
	Queue Queue
	// Publish pushes a transition to the org's browsers (SSE); nil skips.
	Publish func(ctx context.Context, serverID string, n alert.Notice)
	// WebURL builds the links in notifications ("" = no link).
	WebURL string
	// DownGrace is the least silence before a server counts as down: the
	// liveness TTL, so one or two missed heartbeats never fire.
	DownGrace time.Duration
	Log       *logger.Logger
	// Now and NewID default to time.Now and util.GenerateID.
	Now   func() time.Time
	NewID func() string
}

// Evaluator holds the per-target buffers and states. All methods are safe
// for concurrent use: the events consumer and the 15 s sweep call it from
// different goroutines.
type Evaluator struct {
	cfg Config

	mu      sync.Mutex
	started time.Time
	buffers map[alert.Target]*alert.Buffer
	firing  map[alert.Key]alert.Metric
	heard   map[string]time.Time
	problem map[alert.Target]time.Time
	dir     alert.Directory
	dirAt   time.Time
}

// NewEvaluator builds an evaluator; call Load before feeding it events.
func NewEvaluator(cfg Config) *Evaluator {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = util.GenerateID
	}
	return &Evaluator{
		cfg:     cfg,
		started: cfg.Now(),
		buffers: map[alert.Target]*alert.Buffer{},
		firing:  map[alert.Key]alert.Metric{},
		heard:   map[string]time.Time{},
		problem: map[alert.Target]time.Time{},
	}
}

// Load restores the firing states and refills the buffers from the last
// hour of tier-1 samples, so a restart neither re-fires nor waits a full
// window before deciding. Servers count as heard from now.
func (e *Evaluator) Load(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	e.started = now
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	states, err := e.cfg.Store.FiringStates(sctx)
	if err != nil {
		return err
	}
	for _, s := range states {
		e.firing[s.Key] = s.Metric
	}
	if e.cfg.History == nil {
		return nil
	}
	rows, err := e.cfg.History.Tier1Range(sctx, now.Add(-historyWindow), now.Add(time.Minute))
	if err != nil {
		return err
	}
	// Rows come ordered by server, app ("" first) and time, so a server's
	// host memory is known before its apps' rows need it.
	hostMem := map[string]map[time.Time]float64{}
	for _, row := range rows {
		t := alert.Target{ServerID: row.ServerID, AppID: row.AppID}
		if row.AppID == "" {
			if hostMem[row.ServerID] == nil {
				hostMem[row.ServerID] = map[time.Time]float64{}
			}
			hostMem[row.ServerID][row.Start] = row.Data["mem_total"]
			e.buffer(t).Add(alert.Point{At: row.Start, Values: alert.ServerValues(row.Data)})
			continue
		}
		e.buffer(t).Add(alert.Point{At: row.Start, Values: alert.AppValues(row.Data, hostMem[row.ServerID][row.Start])})
	}
	return nil
}

// ObserveHeartbeat records that a server reported (liveness, capabilities)
// and resolves its down alerts.
func (e *Evaluator) ObserveHeartbeat(ctx context.Context, serverID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	dir := e.directory(ctx, now, serverID)
	e.hear(ctx, dir, serverID, now)
}

// ObserveMetrics feeds one server.metrics sample (at = the sample's time)
// and evaluates the threshold rules of the server and its apps.
func (e *Evaluator) ObserveMetrics(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	dir := e.directory(ctx, now, serverID)
	e.hear(ctx, dir, serverID, now)
	srv, ok := dir.Servers[serverID]
	if !ok {
		return
	}
	targets := []alert.Target{{ServerID: serverID}}
	e.buffer(targets[0]).Add(alert.Point{At: at, Values: alert.ServerValues(server)})
	hostMem := server["mem_total"]
	appIDs := make([]string, 0, len(apps))
	for id := range apps {
		if a, known := dir.Apps[id]; known && a.ServerID == serverID {
			appIDs = append(appIDs, id)
		}
	}
	sort.Strings(appIDs)
	for _, id := range appIDs {
		t := alert.Target{ServerID: serverID, AppID: id}
		e.buffer(t).Add(alert.Point{At: at, Values: alert.AppValues(apps[id], hostMem)})
		targets = append(targets, t)
	}
	rules, overrides, ok := e.rules(ctx, srv.OrgID)
	if !ok {
		return
	}
	for _, t := range targets {
		e.evalThresholds(ctx, dir, srv, t, rules, overrides, now)
	}
}

// ObserveAppStatus feeds one apps.status report: problematic[appID] is
// whether the app is problematic (crashed, restart-looping, unhealthy).
// An app fires once it stayed problematic for the rule's duration and
// resolves on the first report that is not problematic.
func (e *Evaluator) ObserveAppStatus(ctx context.Context, serverID string, problematic map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	dir := e.directory(ctx, now, serverID)
	e.hear(ctx, dir, serverID, now)
	srv, ok := dir.Servers[serverID]
	if !ok {
		return
	}
	appIDs := make([]string, 0, len(problematic))
	for id := range problematic {
		if a, known := dir.Apps[id]; known && a.ServerID == serverID {
			appIDs = append(appIDs, id)
		}
	}
	sort.Strings(appIDs)
	for _, id := range appIDs {
		t := alert.Target{ServerID: serverID, AppID: id}
		if problematic[id] {
			if _, tracked := e.problem[t]; !tracked {
				e.problem[t] = now
			}
		} else {
			delete(e.problem, t)
		}
	}
	rules, overrides, ok := e.rules(ctx, srv.OrgID)
	if !ok {
		return
	}
	for _, id := range appIDs {
		t := alert.Target{ServerID: serverID, AppID: id}
		for _, r := range rules {
			if r.TargetKind != alert.TargetApp || r.Metric != alert.MetricDown {
				continue
			}
			k := alert.Key{RuleID: r.ID, Target: t}
			s := alert.Effective(r, overrides[k])
			if !s.Enabled {
				e.clear(ctx, k)
				continue
			}
			since, bad := e.problem[t]
			switch {
			case !bad:
				e.transition(ctx, dir, srv, r, s, k, false, 0, now)
			case now.Sub(since) >= s.Duration:
				e.transition(ctx, dir, srv, r, s, k, true, now.Sub(since).Minutes(), now)
			}
		}
	}
}

// Sweep fires the down rules of servers that have been silent for the
// rule's duration (at least DownGrace). A server never heard since the API
// started counts from the start, not from a stale last_seen. App rules
// need samples or status reports, which a down server does not send, so
// they are naturally suppressed while it is down.
func (e *Evaluator) Sweep(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	dir := e.directory(ctx, now, "")
	ids := make([]string, 0, len(dir.Servers))
	for id := range dir.Servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	type orgRules struct {
		rules     []alert.Rule
		overrides map[alert.Key]*alert.Override
		ok        bool
	}
	byOrg := map[string]orgRules{}
	for _, id := range ids {
		srv := dir.Servers[id]
		or, seen := byOrg[srv.OrgID]
		if !seen {
			or.rules, or.overrides, or.ok = e.rules(ctx, srv.OrgID)
			byOrg[srv.OrgID] = or
		}
		if !or.ok {
			continue
		}
		last, heard := e.heard[id]
		if !heard {
			last = e.started
		}
		silent := now.Sub(last)
		for _, r := range or.rules {
			if r.TargetKind != alert.TargetServer || r.Metric != alert.MetricDown {
				continue
			}
			k := alert.Key{RuleID: r.ID, Target: alert.Target{ServerID: id}}
			s := alert.Effective(r, or.overrides[k])
			if !s.Enabled {
				e.clear(ctx, k)
				continue
			}
			if silent >= max(s.Duration, e.cfg.DownGrace) {
				e.transition(ctx, dir, srv, r, s, k, true, silent.Minutes(), now)
			}
		}
	}
}

// hear marks serverID as heard now and resolves its firing down alerts.
func (e *Evaluator) hear(ctx context.Context, dir alert.Directory, serverID string, now time.Time) {
	e.heard[serverID] = now
	var down []alert.Key
	for k, m := range e.firing {
		if m == alert.MetricDown && k.ServerID == serverID && k.AppID == "" {
			down = append(down, k)
		}
	}
	srv, known := dir.Servers[serverID]
	if len(down) == 0 || !known {
		return
	}
	rules, overrides, ok := e.rules(ctx, srv.OrgID)
	if !ok {
		return
	}
	byID := make(map[string]alert.Rule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
	}
	for _, k := range down {
		r, exists := byID[k.RuleID]
		if !exists {
			delete(e.firing, k) // the rule is gone; its state cascaded
			continue
		}
		e.transition(ctx, dir, srv, r, alert.Effective(r, overrides[k]), k, false, 0, now)
	}
}

// evalThresholds decides the threshold rules of one target from its buffer.
func (e *Evaluator) evalThresholds(ctx context.Context, dir alert.Directory, srv alert.ServerRef, t alert.Target, rules []alert.Rule, overrides map[alert.Key]*alert.Override, now time.Time) {
	buf := e.buffers[t]
	if buf == nil {
		return
	}
	for _, r := range rules {
		if r.TargetKind != t.Kind() || r.Metric == alert.MetricDown {
			continue
		}
		k := alert.Key{RuleID: r.ID, Target: t}
		s := alert.Effective(r, overrides[k])
		if !s.Enabled {
			e.clear(ctx, k)
			continue
		}
		avg, ok := buf.Average(r.Metric, s.Duration)
		if !ok {
			continue // not enough data: keep the current state
		}
		e.transition(ctx, dir, srv, r, s, k, avg > s.Threshold, avg, now)
	}
}

// transition moves k to firing (or ok) when it is not already there:
// records it compare-and-set (event included), publishes the SSE notice
// and, if this evaluator's write made the transition, queues the
// notification. A store error leaves the local state unchanged so the next
// evaluation retries.
func (e *Evaluator) transition(ctx context.Context, dir alert.Directory, srv alert.ServerRef, r alert.Rule, s alert.Settings, k alert.Key, firing bool, value float64, now time.Time) {
	if _, was := e.firing[k]; was == firing {
		return
	}
	info, _ := alert.Lookup(r.TargetKind, r.Metric)
	names := alert.Names{Server: srv.Name}
	if k.AppID != "" {
		names.App = k.AppID
		if a, ok := dir.Apps[k.AppID]; ok && a.Name != "" {
			names.App = a.Name
		}
	}
	kind := alert.EventResolved
	if firing {
		kind = alert.EventFired
	}
	msg := alert.Compose(alert.Transition{
		Kind: kind, Info: info, Names: names, Value: value, Threshold: s.Threshold,
		Duration: s.Duration, Link: alert.Link(e.cfg.WebURL, k.Target),
	})
	ev := alert.Event{
		ID: e.cfg.NewID(), OrgID: srv.OrgID, RuleID: r.ID, ServerID: k.ServerID, AppID: k.AppID,
		ServerName: names.Server, AppName: names.App, TargetKind: r.TargetKind, Metric: r.Metric,
		Kind: kind, Value: value, Threshold: s.Threshold, Title: msg.Title, Message: msg.Body, CreatedAt: now,
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	var won bool
	var err error
	if firing {
		won, err = e.cfg.Store.Fire(sctx, k, value, s.Threshold, ev)
	} else {
		won, err = e.cfg.Store.Resolve(sctx, k, value, ev)
	}
	if err != nil {
		e.cfg.Log.Warn("alerts: record transition failed", "rule_id", k.RuleID, "server_id", k.ServerID, "app_id", k.AppID, "error", err)
		return
	}
	if firing {
		e.firing[k] = r.Metric
	} else {
		delete(e.firing, k)
	}
	if e.cfg.Publish != nil {
		e.cfg.Publish(sctx, k.ServerID, alert.Notice{
			Kind: kind, RuleID: r.ID, TargetKind: r.TargetKind, Metric: r.Metric, ServerID: k.ServerID,
			AppID: k.AppID, Value: value, Threshold: s.Threshold, Title: msg.Title,
		})
	}
	if won && e.cfg.Queue != nil {
		e.cfg.Queue.Enqueue(Job{EventID: ev.ID, OrgID: srv.OrgID, ChannelIDs: s.ChannelIDs, Title: msg.Title, Body: msg.Body})
	}
}

// clear drops a firing state silently: its rule or override was disabled.
func (e *Evaluator) clear(ctx context.Context, k alert.Key) {
	if _, firing := e.firing[k]; !firing {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := e.cfg.Store.ClearState(sctx, k); err != nil {
		e.cfg.Log.Warn("alerts: clear state failed", "rule_id", k.RuleID, "error", err)
		return
	}
	delete(e.firing, k)
}

// rules loads an org's rules and overrides (keyed by rule and target).
func (e *Evaluator) rules(ctx context.Context, orgID string) ([]alert.Rule, map[alert.Key]*alert.Override, bool) {
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	rules, err := e.cfg.Store.ListRules(sctx, orgID)
	if err != nil {
		e.cfg.Log.Warn("alerts: load rules failed", "org_id", orgID, "error", err)
		return nil, nil, false
	}
	list, err := e.cfg.Store.ListOverrides(sctx, orgID)
	if err != nil {
		e.cfg.Log.Warn("alerts: load overrides failed", "org_id", orgID, "error", err)
		return nil, nil, false
	}
	overrides := make(map[alert.Key]*alert.Override, len(list))
	for i := range list {
		overrides[list[i].Key()] = &list[i]
	}
	return rules, overrides, true
}

// directory returns the target directory, reloading it when it is older
// than directoryTTL, or sooner when serverID is unknown. A failed reload
// keeps the previous one. Every reload prunes the per-target state of
// servers and apps that no longer exist.
func (e *Evaluator) directory(ctx context.Context, now time.Time, serverID string) alert.Directory {
	_, known := e.dir.Servers[serverID]
	fresh := e.dir.Servers != nil && now.Sub(e.dirAt) < directoryTTL
	if fresh && (serverID == "" || known || now.Sub(e.dirAt) < directoryRetry) {
		return e.dir
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	d, err := e.cfg.Store.Directory(sctx)
	if err != nil {
		e.cfg.Log.Warn("alerts: load directory failed", "error", err)
		return e.dir
	}
	e.dir, e.dirAt = d, now
	exists := func(t alert.Target) bool {
		if _, ok := d.Servers[t.ServerID]; !ok {
			return false
		}
		if t.AppID == "" {
			return true
		}
		a, ok := d.Apps[t.AppID]
		return ok && a.ServerID == t.ServerID
	}
	for t := range e.buffers {
		if !exists(t) {
			delete(e.buffers, t)
		}
	}
	for t := range e.problem {
		if !exists(t) {
			delete(e.problem, t)
		}
	}
	for k := range e.firing {
		if !exists(k.Target) {
			delete(e.firing, k)
		}
	}
	for id := range e.heard {
		if _, ok := d.Servers[id]; !ok {
			delete(e.heard, id)
		}
	}
	return d
}

// buffer returns t's buffer, creating it.
func (e *Evaluator) buffer(t alert.Target) *alert.Buffer {
	b := e.buffers[t]
	if b == nil {
		b = &alert.Buffer{}
		e.buffers[t] = b
	}
	return b
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/domain/service/alerting/ && GOTMPDIR=$HOME/.cache/wf-gotmp go test -race ./internal/domain/service/alerting/`
Expected: `ok`. Notes:
- `fakes_test.go` already contains the notifier's fake methods (`ListChannels`, `RecordDelivery`, `SetDeliveries`). They compile without Task 6.
- Mutation-checked: setting `alert.Coverage` to 0.5 fails `TestThresholdFiresAndResolves`; dropping `DownGrace` from the sweep fails `TestDownWaitsForGraceOnShortRules`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/service/alerting
git commit -m "feat(alerts): evaluator for threshold, down and app-down rules

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 6: The notifier

**Files:**
- Create: `internal/domain/service/alerting/notifier.go`
- Test: `internal/domain/service/alerting/notifier_test.go`

**Interfaces:**
- Consumes:
  - `Job` and the shared fakes (Task 5);
  - `alert.RouteChannels`, `alert.Channel`, `alert.Delivery` (Task 1).
- Produces:
  - interfaces:
    - `type Sender interface{ Send(ctx, rawURL, title, body string) error }`, implemented by `*notify.Shoutrrr`;
    - `type Opener interface{ Open(sealed string) (string, error) }`, implemented by `*secretbox.Box`;
    - `type ChannelStore interface{ ListChannels; RecordDelivery; SetDeliveries }`;
  - constants `QueueSize = 256` and `SendTimeout = 10 * time.Second`;
  - `func NewNotifier(store ChannelStore, sender Sender, box Opener, log *logger.Logger) *Notifier`. Pass `box` as a nil **interface** when `JWT_SECRET` is unset; never pass a typed nil `*secretbox.Box`;
  - methods:
    - `(*Notifier) Enqueue(Job) bool` (implements `Queue`);
    - `Run(ctx)`;
    - `SendTo(ctx, alert.Channel, title, body string) string`: decrypts, sends, records on the channel, and returns the error text (`""` on success);
    - `SendURL(ctx, rawURL, title, body string) string`: draft test, nothing recorded.

- [ ] **Step 1: Write the failing tests**

`internal/domain/service/alerting/notifier_test.go`:

```go
package alerting

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/alert"
)

// fakeSender records sends; "fail://" URLs fail and "hang://" URLs block
// until their context ends.
type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (s *fakeSender) Send(ctx context.Context, rawURL, title, _ string) error {
	s.mu.Lock()
	s.sent = append(s.sent, rawURL+"|"+title)
	s.mu.Unlock()
	switch {
	case strings.HasPrefix(rawURL, "fail://"):
		return errors.New("service said no")
	case strings.HasPrefix(rawURL, "hang://"):
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// plainBox "decrypts" by dropping a "sealed:" prefix; anything else is
// corrupt.
type plainBox struct{}

func (plainBox) Open(sealed string) (string, error) {
	if url, ok := strings.CutPrefix(sealed, "sealed:"); ok {
		return url, nil
	}
	return "", errors.New("cannot decrypt the stored URL (was JWT_SECRET changed?); re-enter it")
}

func channel(id string, def, enabled bool, url string) alert.Channel {
	return alert.Channel{ID: id, OrgID: org, Name: "ch " + id, IsDefault: def, Enabled: enabled, URLEncrypted: url}
}

func TestDeliverSendsToRoutedChannels(t *testing.T) {
	store := newStore()
	store.channels = []alert.Channel{
		channel("a", true, true, "sealed:ok://a"),
		channel("b", true, false, "sealed:ok://b"),
		channel("c", false, true, "sealed:fail://c"),
		{ID: "x", OrgID: "other-org", IsDefault: true, Enabled: true, URLEncrypted: "sealed:ok://x"},
	}
	sender := &fakeSender{}
	n := NewNotifier(store, sender, plainBox{}, testLog)
	ctx := context.Background()

	n.deliver(ctx, Job{EventID: "e1", OrgID: org, Title: "T"})
	n.deliver(ctx, Job{EventID: "e2", OrgID: org, ChannelIDs: []string{"c", "gone"}, Title: "T"})
	n.deliver(ctx, Job{EventID: "e3", OrgID: org, ChannelIDs: []string{}, Title: "T"})

	if want := []string{"ok://a|T", "fail://c|T"}; !reflect.DeepEqual(sender.sent, want) {
		t.Fatalf("sent = %v", sender.sent)
	}
	if d := store.delivered["e1"]; len(d) != 1 || !d[0].OK || d[0].ChannelName != "ch a" {
		t.Fatalf("e1 = %+v", d)
	}
	if d := store.delivered["e2"]; len(d) != 1 || d[0].OK || d[0].Error != "service said no" {
		t.Fatalf("e2 = %+v", d)
	}
	if d, ok := store.delivered["e3"]; !ok || d == nil || len(d) != 0 {
		t.Fatalf("e3 = %#v (want an empty, non-nil list)", d)
	}
	if want := []string{"a:", "c:service said no"}; !reflect.DeepEqual(store.records, want) {
		t.Fatalf("channel records = %v", store.records)
	}
}

func TestDeliverRecordsDecryptFailure(t *testing.T) {
	store := newStore()
	store.channels = []alert.Channel{channel("a", true, true, "garbage")}
	sender := &fakeSender{}
	NewNotifier(store, sender, plainBox{}, testLog).deliver(context.Background(), Job{EventID: "e1", OrgID: org})
	if len(sender.sent) != 0 {
		t.Fatalf("sent with an undecryptable URL: %v", sender.sent)
	}
	if d := store.delivered["e1"]; len(d) != 1 || !strings.Contains(d[0].Error, "JWT_SECRET") {
		t.Fatalf("deliveries = %+v", d)
	}
}

func TestDeliverWithoutEncryptionKey(t *testing.T) {
	store := newStore()
	store.channels = []alert.Channel{channel("a", true, true, "sealed:ok://a")}
	sender := &fakeSender{}
	NewNotifier(store, sender, nil, testLog).deliver(context.Background(), Job{EventID: "e1", OrgID: org})
	if len(sender.sent) != 0 || store.delivered["e1"][0].Error != encryptionUnavailable {
		t.Fatalf("sent=%v deliveries=%+v", sender.sent, store.delivered["e1"])
	}
}

func TestSendTimesOut(t *testing.T) {
	store := newStore()
	n := NewNotifier(store, &fakeSender{}, plainBox{}, testLog)
	n.timeout = 50 * time.Millisecond
	start := time.Now()
	got := n.SendTo(context.Background(), channel("h", true, true, "sealed:hang://h"), "T", "B")
	if got == "" || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%q after %s", got, time.Since(start))
	}
	if got := n.SendURL(context.Background(), "hang://draft", "T", "B"); got == "" {
		t.Fatal("draft send did not time out")
	}
}

func TestEnqueueDropsWhenFull(t *testing.T) {
	n := NewNotifier(newStore(), &fakeSender{}, plainBox{}, testLog)
	for i := 0; i < QueueSize; i++ {
		if !n.Enqueue(Job{EventID: "e"}) {
			t.Fatalf("job %d dropped early", i)
		}
	}
	if n.Enqueue(Job{EventID: "overflow"}) {
		t.Fatal("a full queue accepted a job")
	}
}

func TestRunDrainsTheQueue(t *testing.T) {
	store := newStore()
	store.channels = []alert.Channel{channel("a", true, true, "sealed:ok://a")}
	n := NewNotifier(store, &fakeSender{}, plainBox{}, testLog)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	n.Enqueue(Job{EventID: "e1", OrgID: org})
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.mu.Lock()
		_, ok := store.delivered["e1"]
		store.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job not delivered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/domain/service/alerting/`
Expected: FAIL to compile (`undefined: NewNotifier`).

- [ ] **Step 3: Write the notifier**

`internal/domain/service/alerting/notifier.go`:

```go
package alerting

import (
	"context"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/pkg/logger"
)

const (
	// QueueSize bounds the notifications waiting for the sender; a full
	// queue drops (and logs) new ones instead of blocking evaluation.
	QueueSize = 256
	// SendTimeout bounds one channel send.
	SendTimeout = 10 * time.Second
	// maxErrorLen bounds a stored delivery error.
	maxErrorLen = 500
	// encryptionUnavailable is the delivery error without JWT_SECRET.
	encryptionUnavailable = "channel encryption is unavailable: set JWT_SECRET"
)

// Sender delivers one notification to a channel URL (notify.Shoutrrr).
// Its errors must already be free of the URL's secrets.
type Sender interface {
	Send(ctx context.Context, rawURL, title, body string) error
}

// Opener decrypts a stored channel URL (secretbox.Box).
type Opener interface {
	Open(sealed string) (string, error)
}

// ChannelStore is the slice of the alert repository the notifier needs.
type ChannelStore interface {
	ListChannels(ctx context.Context, orgID string) ([]alert.Channel, error)
	RecordDelivery(ctx context.Context, channelID string, at time.Time, sendErr string) error
	SetDeliveries(ctx context.Context, eventID string, ds []alert.Delivery) error
}

// Notifier delivers queued notifications one at a time from a single
// goroutine (Run), so a slow channel delays the queue instead of piling up
// goroutines. Every send has SendTimeout; there are no retries.
type Notifier struct {
	store   ChannelStore
	sender  Sender
	box     Opener
	log     *logger.Logger
	queue   chan Job
	timeout time.Duration
	now     func() time.Time
}

// NewNotifier builds a notifier. A nil box (no JWT_SECRET) records every
// delivery as failed instead of sending.
func NewNotifier(store ChannelStore, sender Sender, box Opener, log *logger.Logger) *Notifier {
	return &Notifier{
		store: store, sender: sender, box: box, log: log,
		queue: make(chan Job, QueueSize), timeout: SendTimeout, now: time.Now,
	}
}

// Enqueue queues a job without blocking; it reports false (and logs) when
// the queue is full.
func (n *Notifier) Enqueue(j Job) bool {
	select {
	case n.queue <- j:
		return true
	default:
		n.log.Warn("alerts: notification queue full, dropping", "event_id", j.EventID)
		return false
	}
}

// Run delivers queued jobs until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-n.queue:
			n.deliver(ctx, j)
		}
	}
}

// deliver sends j to its routed channels and records the outcomes on the
// event and on each channel.
func (n *Notifier) deliver(ctx context.Context, j Job) {
	lctx, cancel := context.WithTimeout(ctx, storeTimeout)
	channels, err := n.store.ListChannels(lctx, j.OrgID)
	cancel()
	if err != nil {
		n.log.Warn("alerts: load channels failed", "org_id", j.OrgID, "error", err)
		return
	}
	ds := []alert.Delivery{}
	for _, ch := range alert.RouteChannels(channels, j.ChannelIDs) {
		errText := n.SendTo(ctx, ch, j.Title, j.Body)
		ds = append(ds, alert.Delivery{ChannelID: ch.ID, ChannelName: ch.Name, OK: errText == "", Error: errText})
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := n.store.SetDeliveries(sctx, j.EventID, ds); err != nil {
		n.log.Warn("alerts: record deliveries failed", "event_id", j.EventID, "error", err)
	}
}

// SendTo decrypts the channel's URL, sends, and records the outcome on the
// channel. It returns the error text ("" on success); the text never holds
// the URL.
func (n *Notifier) SendTo(ctx context.Context, ch alert.Channel, title, body string) string {
	errText := n.send(ctx, ch, title, body)
	rctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := n.store.RecordDelivery(rctx, ch.ID, n.now(), errText); err != nil {
		n.log.Warn("alerts: record channel delivery failed", "channel_id", ch.ID, "error", err)
	}
	return errText
}

// SendURL sends to a draft URL (the Test button before saving); nothing
// is recorded.
func (n *Notifier) SendURL(ctx context.Context, rawURL, title, body string) string {
	sctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	return errorText(n.sender.Send(sctx, rawURL, title, body))
}

func (n *Notifier) send(ctx context.Context, ch alert.Channel, title, body string) string {
	if n.box == nil {
		return encryptionUnavailable
	}
	rawURL, err := n.box.Open(ch.URLEncrypted)
	if err != nil {
		return errorText(err)
	}
	sctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	return errorText(n.sender.Send(sctx, rawURL, title, body))
}

// errorText is err's message, bounded; "" for nil.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > maxErrorLen {
		s = s[:maxErrorLen] + "…"
	}
	return s
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/domain/service/alerting/ && GOTMPDIR=$HOME/.cache/wf-gotmp go test -race ./internal/domain/service/alerting/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/service/alerting/notifier.go internal/domain/service/alerting/notifier_test.go
git commit -m "feat(alerts): notifier with a bounded queue and per-send timeouts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 7: Wire alerts into the API (events sink, sweep, WireCore, SSE)

**Files:**
- Create: `internal/infra/bootstrap/alerts.go`
- Test: `internal/infra/bootstrap/alerts_test.go`
- Modify:
  - `internal/infra/bootstrap/events.go`, `core.go`, `deps.go`;
  - `internal/domain/model/notification.go`;
  - `internal/app/web/handler/notification/routes.go` and `routes_test.go`;
  - `.env.example`;
  - `api/openapi.yaml` (regenerated), `internal/app/web/testdata/routes.golden` (regenerated).

**Interfaces:**
- Consumes:
  - `alerting.NewEvaluator`, `Config`, `NewNotifier`, `SendTimeout`, `History`, `Opener` (Tasks 5 and 6);
  - `alertrepo.New` (Task 3);
  - `notify.NewShoutrrr`, `secretbox.New`, `cfg.AllowsPrivateAlertTargets` (Task 2);
  - `metricrepo.Repo.Tier1Range` (Task 4).
- Produces:
  - `model.NotificationAlert NotificationType = "alert"`, whose SSE payload is `alert.Notice`;
  - new `bootstrap.Deps` fields:
    - `AlertRepository port.AlertRepository`;
    - `AlertNotifier *alerting.Notifier`;
    - `ChannelBox *secretbox.Box` (nil without `JWT_SECRET`).
  - the events sink calls:
    - `ObserveHeartbeat` on `server.online` and `capabilities`, and on `server.metrics` when metrics are off;
    - `ObserveAppStatus` on every `apps.status`;
    - `ObserveMetrics` after storing a sample;
    - `Sweep` at the end of every 15 s sweep;
  - `runAlertMaintenance`: seed defaults and prune history at startup and every 10 min.

- [ ] **Step 1: Write the failing tests**

`internal/infra/bootstrap/alerts_test.go`:

```go
package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// fakeObserver records what the sink tells the alert evaluator.
type fakeObserver struct {
	mu    sync.Mutex
	calls []string
	apps  map[string]bool
	vals  map[string]metrics.Values
}

func (o *fakeObserver) add(c string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, c)
}

func (o *fakeObserver) ObserveHeartbeat(_ context.Context, id string) { o.add("heartbeat:" + id) }
func (o *fakeObserver) ObserveMetrics(_ context.Context, id string, _ time.Time, _ metrics.Values, apps map[string]metrics.Values) {
	o.mu.Lock()
	o.vals = apps
	o.mu.Unlock()
	o.add("metrics:" + id)
}
func (o *fakeObserver) ObserveAppStatus(_ context.Context, id string, problematic map[string]bool) {
	o.mu.Lock()
	o.apps = problematic
	o.mu.Unlock()
	o.add("status:" + id)
}
func (o *fakeObserver) Sweep(context.Context) { o.add("sweep") }

func TestSinkDrivesTheAlertEvaluator(t *testing.T) {
	rec := &recorder{}
	sink, cache := newTestSink(rec, []string{"alice"})
	obs := &fakeObserver{}
	sink.alerts = obs
	sink.metrics = &fakeMetricsStore{}
	ctx := context.Background()

	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerOnline})
	caps, _ := json.Marshal(capabilitiesEvent{})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventCapabilities, Payload: caps})
	status, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{
		{AppID: "a1", StatusCode: command.ContainerStatusProblematic},
		{AppID: "a2", StatusCode: command.ContainerStatusStopped},
	}})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventAppsStatus, Payload: status})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventAppsStatus, Payload: status}) // unchanged: still observed
	cpu := 5.0
	sample, _ := json.Marshal(metrics.Sample{TS: time.Now().Unix(), Server: metrics.ServerSample{CPU: &cpu},
		Apps: []metrics.AppSample{{AppID: "a1", CPU: &cpu}}})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerMetrics, Payload: sample})
	sink.sweep(ctx, cache, time.Now())

	want := []string{"heartbeat:srv-1", "heartbeat:srv-1", "status:srv-1", "status:srv-1", "metrics:srv-1", "sweep"}
	if !reflect.DeepEqual(obs.calls, want) {
		t.Fatalf("calls = %v", obs.calls)
	}
	if !reflect.DeepEqual(obs.apps, map[string]bool{"a1": true, "a2": false}) || obs.vals["a1"]["cpu"] != 5 {
		t.Fatalf("apps=%v vals=%v", obs.apps, obs.vals)
	}
}

func TestSinkWithMetricsOffStillHearsTheServer(t *testing.T) {
	sink, _ := newTestSink(&recorder{}, nil)
	obs := &fakeObserver{}
	sink.alerts = obs
	sink.handle(context.Background(), bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerMetrics, Payload: []byte(`{}`)})
	if !reflect.DeepEqual(obs.calls, []string{"heartbeat:srv-1"}) {
		t.Fatalf("calls = %v", obs.calls)
	}
}

// fakeAlertMaintainer counts maintenance calls.
type fakeAlertMaintainer struct {
	seeds, prunes int
	err           error
}

func (m *fakeAlertMaintainer) SeedDefaults(context.Context) (int, error) { m.seeds++; return 1, m.err }
func (m *fakeAlertMaintainer) PruneEvents(context.Context, time.Time) (int64, error) {
	m.prunes++
	return 3, m.err
}

func TestAlertMaintenanceTickSeedsAndPrunes(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := &fakeAlertMaintainer{}
	alertMaintenanceTick(context.Background(), m, time.Now(), log)
	m.err = errors.New("db down") // logged, never fatal
	alertMaintenanceTick(context.Background(), m, time.Now(), log)
	if m.seeds != 2 || m.prunes != 2 {
		t.Fatalf("seeds=%d prunes=%d", m.seeds, m.prunes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runAlertMaintenance(ctx, m, time.Hour, log); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance loop ignored cancel")
	}
}

// End to end through WireCore: the default rules are seeded at startup and
// the alert deps are wired; without JWT_SECRET there is no channel box.
func TestWireCoreWiresAlerts(t *testing.T) {
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "wf.sqlite"))
	t.Setenv("JWT_SECRET", "")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	deps, _ := WireCore(ctx, membus.NewBus(log), conn, cfg, log)
	if deps.AlertRepository == nil || deps.AlertNotifier == nil || deps.ChannelBox != nil {
		t.Fatalf("alert deps: repo=%v notifier=%v box=%v", deps.AlertRepository, deps.AlertNotifier, deps.ChannelBox)
	}
}
```

In `internal/app/web/handler/notification/routes_test.go`:
- import `"winterflow/internal/domain/alert"`;
- add `"alert": alert.Notice{},` to the `want` map.

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/bootstrap/ ./internal/app/web/handler/notification/`
Expected: FAIL to compile (`sink.alerts undefined`, `undefined: alertMaintenanceTick`).

- [ ] **Step 2: Add the SSE notification type**

Apply to `internal/domain/model/notification.go` and `internal/app/web/handler/notification/routes.go`:

```diff
diff --git a/internal/app/web/handler/notification/routes.go b/internal/app/web/handler/notification/routes.go
index 1a1df27..2e374f2 100644
--- a/internal/app/web/handler/notification/routes.go
+++ b/internal/app/web/handler/notification/routes.go
@@ -4,6 +4,7 @@ import (
 	"net/http"
 
 	"winterflow/internal/app/web/api"
+	"winterflow/internal/domain/alert"
 	"winterflow/internal/domain/command"
 	"winterflow/internal/domain/model"
 )
@@ -15,7 +16,7 @@ const StreamDescription = "Server-sent events, each `data: <Notification JSON>`.
 	"Subscribe BEFORE dispatching a 202 operation: results are best-effort, not buffered or replayed. " +
 	"A result produced while no stream is open, or while this stream is not being read, is dropped. " +
 	"An operation_result is delivered only to the open streams of the user who dispatched it; " +
-	"match it on ref == the 202's data.request_id. server_status and apps_status events go to every " +
+	"match it on ref == the 202's data.request_id. server_status, apps_status and alert events go to every " +
 	"member of the server's organization and interleave with results; ignore types you do not handle."
 
 // Routes is the SSE stream's slice of the route table. operation_result
@@ -31,6 +32,7 @@ func (h *Handler) Routes(api.Guards) []api.Route {
 			{Type: string(model.NotificationOperationResult)},
 			{Type: string(model.NotificationServerStatus), Payload: model.ServerStatusPayload{}},
 			{Type: string(model.NotificationAppsStatus), Payload: command.AppsStatusPayload{}},
+			{Type: string(model.NotificationAlert), Payload: alert.Notice{}},
 		},
 	}}
 }
diff --git a/internal/domain/model/notification.go b/internal/domain/model/notification.go
index c278f73..a9f631c 100644
--- a/internal/domain/model/notification.go
+++ b/internal/domain/model/notification.go
@@ -14,6 +14,9 @@ const (
 	// NotificationAppsStatus is an unsolicited container-status report for a
 	// server's apps (Ref is empty; Payload is command.AppsStatusPayload).
 	NotificationAppsStatus NotificationType = "apps_status"
+	// NotificationAlert is an alert transition (fired/resolved) on one of
+	// the org's servers or apps (Ref is empty; Payload is alert.Notice).
+	NotificationAlert NotificationType = "alert"
 )
 
 // ServerStatusPayload is the payload of a server_status notification.
```

- [ ] **Step 3: Drive the evaluator from the events sink**

Apply to `internal/infra/bootstrap/events.go`:

```diff
diff --git a/internal/infra/bootstrap/events.go b/internal/infra/bootstrap/events.go
index fbb1ee4..27d3650 100644
--- a/internal/infra/bootstrap/events.go
+++ b/internal/infra/bootstrap/events.go
@@ -5,6 +5,7 @@ import (
 	"encoding/json"
 	"time"
 
+	"winterflow/internal/domain/alert"
 	"winterflow/internal/domain/command"
 	"winterflow/internal/domain/metrics"
 	"winterflow/internal/domain/model"
@@ -75,6 +76,11 @@ func (f *statusFanout) serverStatus(ctx context.Context, serverID string, livene
 	})
 }
 
+// alert pushes an alert transition to the server's org.
+func (f *statusFanout) alert(ctx context.Context, serverID string, n alert.Notice) {
+	f.send(ctx, serverID, model.Notification{Type: model.NotificationAlert, Payload: n, Timestamp: time.Now()})
+}
+
 func (f *statusFanout) appsStatus(ctx context.Context, serverID string, apps []command.AppStatus) {
 	f.send(ctx, serverID, model.Notification{
 		Type:      model.NotificationAppsStatus,
@@ -95,6 +101,9 @@ type eventSink struct {
 	// sample for the dashboard.
 	metrics metricsStore
 	latest  *metrics.LatestStore
+	// alerts, when set, evaluates the alert rules on every event and on
+	// the sweep.
+	alerts alertObserver
 	// sync, when set, fills the API's per-server indexes from the agent: it
 	// starts when a server comes online (an unknown->online transition, or a
 	// capabilities event: the agent (re)registered) and is nudged by every
@@ -102,6 +111,15 @@ type eventSink struct {
 	sync serverSync
 }
 
+// alertObserver is the alert evaluator as the event sink drives it
+// (alerting.Evaluator).
+type alertObserver interface {
+	ObserveHeartbeat(ctx context.Context, serverID string)
+	ObserveMetrics(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values)
+	ObserveAppStatus(ctx context.Context, serverID string, problematic map[string]bool)
+	Sweep(ctx context.Context)
+}
+
 // serverSync is the per-server index fill the event sink drives.
 type serverSync interface {
 	online(serverID string)
@@ -120,6 +138,9 @@ func (s *eventSink) handle(ctx context.Context, ev bus.EventMessage) {
 		if transition {
 			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
 		}
+		if s.alerts != nil {
+			s.alerts.ObserveHeartbeat(ctx, ev.ServerID)
+		}
 	case bus.EventCapabilities:
 		var c capabilitiesEvent
 		if err := json.Unmarshal(ev.Payload, &c); err != nil {
@@ -133,6 +154,9 @@ func (s *eventSink) handle(ctx context.Context, ev bus.EventMessage) {
 		if transition {
 			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
 		}
+		if s.alerts != nil {
+			s.alerts.ObserveHeartbeat(ctx, ev.ServerID)
+		}
 	case bus.EventAppsStatus:
 		var body command.GetAppsStatusResponse
 		if err := json.Unmarshal(ev.Payload, &body); err != nil {
@@ -149,6 +173,11 @@ func (s *eventSink) handle(ctx context.Context, ev bus.EventMessage) {
 		if transition {
 			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
 		}
+		// Every report counts (time passes for app-down rules), not only
+		// changed ones.
+		if s.alerts != nil {
+			s.alerts.ObserveAppStatus(ctx, ev.ServerID, problematicApps(body.Apps))
+		}
 	case bus.EventServerMetrics:
 		s.storeMetrics(ctx, ev, now)
 		return
@@ -171,6 +200,11 @@ func (s *eventSink) handle(ctx context.Context, ev bus.EventMessage) {
 // minute's sample is independent. Metrics never touch liveness or SSE.
 func (s *eventSink) storeMetrics(ctx context.Context, ev bus.EventMessage, now time.Time) {
 	if s.metrics == nil {
+		// Metrics are off: nothing to store or evaluate, but the event
+		// still proves the server is up.
+		if s.alerts != nil {
+			s.alerts.ObserveHeartbeat(ctx, ev.ServerID)
+		}
 		return
 	}
 	var sample metrics.Sample
@@ -197,6 +231,18 @@ func (s *eventSink) storeMetrics(ctx context.Context, ev bus.EventMessage, now t
 	if s.latest != nil {
 		s.latest.Set(ev.ServerID, metrics.Latest{At: at, Server: sample.Server})
 	}
+	if s.alerts != nil {
+		s.alerts.ObserveMetrics(ctx, ev.ServerID, at, sample.Server.Values(), apps)
+	}
+}
+
+// problematicApps maps an apps.status report to app id -> problematic.
+func problematicApps(apps []command.AppStatus) map[string]bool {
+	out := make(map[string]bool, len(apps))
+	for _, a := range apps {
+		out[a.AppID] = a.StatusCode == command.ContainerStatusProblematic
+	}
+	return out
 }
 
 // sweep flips servers whose liveness expired to unknown and pushes the
@@ -205,13 +251,16 @@ func (s *eventSink) sweep(ctx context.Context, cache *status.Cache, now time.Tim
 	for _, serverID := range cache.ExpireStale(now) {
 		s.fan.serverStatus(ctx, serverID, status.LivenessUnknown)
 	}
+	if s.alerts != nil {
+		s.alerts.Sweep(ctx)
+	}
 }
 
 // startEventsSubscriber drains the region's events queue (agent-initiated:
 // liveness, capabilities, status), updating the in-memory status cache and the
 // DB, and pushing transitions over SSE. It also runs the expiry sweeper. It is
 // the API's consumer of events:<region>.
-func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache, serverRepo port.ServerRepository, nm port.NotificationManager, cfg *config.ServerConfig, log *logger.Logger, indexSync serverSync, ms metricsStore, latest *metrics.LatestStore) {
+func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache, serverRepo port.ServerRepository, nm port.NotificationManager, cfg *config.ServerConfig, log *logger.Logger, indexSync serverSync, ms metricsStore, latest *metrics.LatestStore, alerts alertObserver) {
 	sink := &eventSink{
 		cache:   cache,
 		store:   serverRepo,
@@ -220,6 +269,7 @@ func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache,
 		sync:    indexSync,
 		metrics: ms,
 		latest:  latest,
+		alerts:  alerts,
 	}
 	bus.SubscribeJSON(ctx, b, cfg.GetBusEventsQueue(), log, func(ev bus.EventMessage) {
 		sink.handle(ctx, ev)
```

- [ ] **Step 4: Build the alerting pieces in WireCore**

`internal/infra/bootstrap/alerts.go`:

```go
package bootstrap

import (
	"context"
	"time"

	"winterflow/internal/domain/service/alerting"
	alertrepo "winterflow/internal/infra/db/repository/alert"
	"winterflow/internal/infra/notify"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/secretbox"
)

const (
	// alertMaintenanceInterval is how often default rules are seeded for
	// new orgs and the history is pruned.
	alertMaintenanceInterval = 10 * time.Minute
	// alertMaintenanceTimeout bounds one maintenance tick.
	alertMaintenanceTimeout = 2 * time.Minute
	// alertLoadTimeout bounds the evaluator's warm start at boot.
	alertLoadTimeout = 30 * time.Second
)

// alerts is the alerting wiring WireCore hands to the events sink and Deps.
type alerts struct {
	evaluator *alerting.Evaluator
	notifier  *alerting.Notifier
	// box encrypts channel URLs; nil without JWT_SECRET.
	box *secretbox.Box
}

// wireAlerts builds the channel box, the Shoutrrr sender, the notifier
// (draining on its own goroutine) and the evaluator, warms the evaluator
// from the stored states and the last hour of samples, and starts the
// maintenance loop. history is nil when metrics are off.
func wireAlerts(ctx context.Context, repo *alertrepo.Repo, history alerting.History, fan *statusFanout, cfg *config.ServerConfig, log *logger.Logger) alerts {
	box, err := secretbox.New(cfg.GetJwtSecret())
	if err != nil {
		log.Warn("notification channels unavailable: JWT_SECRET is not set")
		box = nil
	}
	// A nil *Box must not become a non-nil Opener.
	var opener alerting.Opener
	if box != nil {
		opener = box
	}
	notifier := alerting.NewNotifier(repo, notify.NewShoutrrr(alerting.SendTimeout, cfg.AllowsPrivateAlertTargets()), opener, log)
	go notifier.Run(ctx)

	evaluator := alerting.NewEvaluator(alerting.Config{
		Store:     repo,
		History:   history,
		Queue:     notifier,
		Publish:   fan.alert,
		WebURL:    cfg.GetWebURL(),
		DownGrace: statusTTL,
		Log:       log,
	})
	lctx, cancel := context.WithTimeout(ctx, alertLoadTimeout)
	if err := evaluator.Load(lctx); err != nil {
		log.Warn("alerts: warm start failed; buffers start empty", "error", err)
	}
	cancel()

	go runAlertMaintenance(ctx, repo, alertMaintenanceInterval, log)
	return alerts{evaluator: evaluator, notifier: notifier, box: box}
}

// alertMaintainer is the slice of the alert repository the loop needs.
type alertMaintainer interface {
	SeedDefaults(ctx context.Context) (int, error)
	PruneEvents(ctx context.Context, now time.Time) (int64, error)
}

// alertMaintenanceTick seeds default rules for orgs that have none yet and
// prunes the history (90 days, 2,000 per org).
func alertMaintenanceTick(ctx context.Context, repo alertMaintainer, now time.Time, log *logger.Logger) {
	ctx, cancel := context.WithTimeout(ctx, alertMaintenanceTimeout)
	defer cancel()
	if n, err := repo.SeedDefaults(ctx); err != nil {
		log.Warn("alerts: seed default rules failed", "error", err)
	} else if n > 0 {
		log.Debug("alerts: seeded default rules", "orgs", n)
	}
	if n, err := repo.PruneEvents(ctx, now); err != nil {
		log.Warn("alerts: prune history failed", "error", err)
	} else if n > 0 {
		log.Debug("alerts: pruned history", "rows", n)
	}
}

// runAlertMaintenance ticks immediately (seeding orgs at startup) and then
// every interval until ctx is done.
func runAlertMaintenance(ctx context.Context, repo alertMaintainer, interval time.Duration, log *logger.Logger) {
	alertMaintenanceTick(ctx, repo, time.Now(), log)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			alertMaintenanceTick(ctx, repo, now, log)
		}
	}
}

// The evaluator is what the events sink drives.
var _ alertObserver = (*alerting.Evaluator)(nil)
```

Apply to `internal/infra/bootstrap/core.go` and `internal/infra/bootstrap/deps.go`:

```diff
diff --git a/internal/infra/bootstrap/core.go b/internal/infra/bootstrap/core.go
index 8b7f2a7..0506d64 100644
--- a/internal/infra/bootstrap/core.go
+++ b/internal/infra/bootstrap/core.go
@@ -4,9 +4,11 @@ import (
 	"context"
 
 	"winterflow/internal/domain/metrics"
+	"winterflow/internal/domain/service/alerting"
 	notificationsvc "winterflow/internal/domain/service/notification"
 	"winterflow/internal/domain/service/status"
 	"winterflow/internal/infra/db"
+	alertrepo "winterflow/internal/infra/db/repository/alert"
 	apprepo "winterflow/internal/infra/db/repository/app"
 	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
 	metricrepo "winterflow/internal/infra/db/repository/metric"
@@ -17,6 +19,7 @@ import (
 	"winterflow/internal/infra/transport/dispatch"
 	"winterflow/pkg/config"
 	"winterflow/pkg/logger"
+	"winterflow/pkg/util"
 )
 
 // WireCore builds the wiring both topologies share: DB-backed repositories and
@@ -42,13 +45,19 @@ func WireCore(ctx context.Context, b bus.Bus, conn *db.DB, cfg *config.ServerCon
 	// METRICS_ENABLED=false; the chart routes then serve empty series.
 	latest := metrics.NewLatestStore()
 	var ms metricsStore
+	var history alerting.History
 	if cfg.IsMetricsEnabled() {
 		ms = r.metric
+		history = r.metric
 		go runMetricsMaintenance(ctx, r.metric, latest, metricsMaintenanceInterval, log)
 	}
 
+	// Alerts: the evaluator rides the events sink (samples, statuses,
+	// liveness sweep); the notifier sends through the org's channels.
+	al := wireAlerts(ctx, r.alert, history, newStatusFanout(r.server, nm, log), cfg, log)
+
 	startResponseSubscriber(ctx, b, dispatcher, cfg, log)
-	startEventsSubscriber(ctx, b, statusCache, r.server, nm, cfg, log, newDomainSync(ctx, dispatcher, r.serverDomain, log), ms, latest)
+	startEventsSubscriber(ctx, b, statusCache, r.server, nm, cfg, log, newDomainSync(ctx, dispatcher, r.serverDomain, log), ms, latest, al.evaluator)
 
 	return &Deps{
 		Log:                    log,
@@ -63,10 +72,13 @@ func WireCore(ctx context.Context, b bus.Bus, conn *db.DB, cfg *config.ServerCon
 		StatusCache:            statusCache,
 		MetricRepository:       r.metric,
 		LatestMetrics:          latest,
+		AlertRepository:        r.alert,
+		AlertNotifier:          al.notifier,
+		ChannelBox:             al.box,
 	}, r.server
 }
 
-// repos are the six DB repositories, built for the connection's driver.
+// repos are the DB repositories, built for the connection's driver.
 type repos struct {
 	user         *userrepo.Repo
 	server       *serverrepo.Repo
@@ -74,6 +86,7 @@ type repos struct {
 	appDomain    *appdomainrepo.Repo
 	serverDomain *serverdomainrepo.Repo
 	metric       *metricrepo.Repo
+	alert        *alertrepo.Repo
 }
 
 func newRepos(conn *db.DB, log *logger.Logger) (repos, error) {
@@ -96,6 +109,9 @@ func newRepos(conn *db.DB, log *logger.Logger) (repos, error) {
 	if r.serverDomain, err = serverdomainrepo.New(conn.Tx, conn.Driver, log); err != nil {
 		return r, err
 	}
-	r.metric, err = metricrepo.New(conn.Tx, conn.Driver, log)
+	if r.metric, err = metricrepo.New(conn.Tx, conn.Driver, log); err != nil {
+		return r, err
+	}
+	r.alert, err = alertrepo.New(conn.Tx, conn.Driver, log, util.GenerateID)
 	return r, err
 }
diff --git a/internal/infra/bootstrap/deps.go b/internal/infra/bootstrap/deps.go
index 5af9a96..65d62a5 100644
--- a/internal/infra/bootstrap/deps.go
+++ b/internal/infra/bootstrap/deps.go
@@ -3,9 +3,11 @@ package bootstrap
 import (
 	"winterflow/internal/domain/metrics"
 	"winterflow/internal/domain/port"
+	"winterflow/internal/domain/service/alerting"
 	"winterflow/internal/domain/service/status"
 	"winterflow/pkg/config"
 	"winterflow/pkg/logger"
+	"winterflow/pkg/secretbox"
 )
 
 // Deps holds the fully-constructed dependencies a web server needs to run.
@@ -32,4 +34,11 @@ type Deps struct {
 	// each server's newest host sample for the dashboard.
 	MetricRepository port.MetricRepository
 	LatestMetrics    *metrics.LatestStore
+	// AlertRepository stores channels, rules, overrides, states and
+	// history; AlertNotifier sends (the Test button uses it directly).
+	AlertRepository port.AlertRepository
+	AlertNotifier   *alerting.Notifier
+	// ChannelBox encrypts channel URLs; nil when JWT_SECRET is unset
+	// (saving or testing a channel then answers 400).
+	ChannelBox *secretbox.Box
 }
```

- [ ] **Step 5: Document the env vars**

Apply to `.env.example`:

```diff
diff --git a/.env.example b/.env.example
index d2bbf24..8b6c789 100644
--- a/.env.example
+++ b/.env.example
@@ -24,7 +24,8 @@ API_PORT=8080
 
 # Public URL the web UI is served from. Also decides cookie security: an
 # https:// value marks auth cookies Secure (plain-http deployments would
-# silently drop them and every request would 401).
+# silently drop them and every request would 401). Alert notifications link
+# to <WEB_URL>/server/<id>/monitoring (or the app's Metrics tab).
 WEB_URL=http://localhost:8080
 
 # Comma-separated CORS allowlist for browser requests. Default "*".
@@ -32,6 +33,10 @@ WEB_URL=http://localhost:8080
 
 # HMAC secret signing session JWTs. REQUIRED; generate a long random value
 # (e.g. `openssl rand -hex 32`). Rotating it invalidates all sessions.
+# The API also derives from it the key that encrypts notification channel
+# URLs at rest (Org -> Alerts -> Channels): after a rotation every channel
+# fails with "cannot decrypt" until its URL is re-entered. Unset, channels
+# cannot be saved or tested.
 # scripts/install.sh generates one automatically.
 JWT_SECRET=change_me_openssl_rand_hex_32
 
```

- [ ] **Step 6: Regenerate the API description and run the tests**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp make openapi
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web -run TestRoutesGolden -update
GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./... && GOTMPDIR=$HOME/.cache/wf-gotmp go test -race ./internal/infra/bootstrap/ ./internal/app/web/...
```

Expected:
- `api/openapi.yaml` gains the `Notice` schema, an `alert` branch on `Notification`, and the reworded SSE description;
- every package `ok`;
- the contract suite still passes unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/bootstrap internal/domain/model internal/app/web/handler/notification .env.example api/openapi.yaml internal/app/web/testdata
git commit -m "feat(alerts): evaluate rules on agent events and send notifications

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 8: HTTP API for channels and rules

**Files:**
- Create: `internal/app/web/handler/alerts/handler.go`, `channels.go`, `rules.go`, `routes.go`
- Modify: `internal/app/web/routes.go`
- Test: `internal/app/web/contract/alerts_test.go`, `internal/app/web/contract/matrix_test.go` (probes)
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes:
  - `bootstrap.Deps.AlertRepository`, `AlertNotifier`, `ChannelBox` (Task 7);
  - `notify.Service` (Task 2);
  - `port.AlertRepository` (Task 4);
  - `alert.Rule.Normalize/Validate` (Task 1).
- Produces (package `winterflow/internal/app/web/handler/alerts`, imported as `halerts` in `routes.go`):
  - `type Deps struct{ Logger; Users OrgResolver; Servers webutil.ServerAccess; Apps AppLookup; Alerts port.AlertRepository; Box Sealer; Tester Tester }` and `NewHandler(*Deps) *Handler`;
  - request types: `SaveChannelRequest`, `TestChannelRequest`, `SaveRuleRequest`, `IDRequest`;
  - response type `TestChannelResponse{OK bool; Error string}`;
  - routes:
    - `GET /api/v1/alerts/get-channels` → `[]alert.Channel`;
    - `POST /api/v1/alerts/save-channel` → `alert.Channel`;
    - `POST /api/v1/alerts/delete-channel`;
    - `POST /api/v1/alerts/test-channel` → `TestChannelResponse`;
    - `GET /api/v1/alerts/get-rules` → `[]alert.Rule` (seeds defaults first);
    - `POST /api/v1/alerts/save-rule` → `alert.Rule`;
    - `POST /api/v1/alerts/delete-rule`;
  - `(*Handler).requireTarget`, the target authorization Task 9 uses: `RequireServerAccess`, then the server's org must equal the caller's, then the app must be on that server.

- [ ] **Step 1: Write the failing contract tests**

`internal/app/web/contract/alerts_test.go`:

```go
package contract_test

import (
	"net/http"
	"strings"
	"testing"

	"winterflow/internal/app/web/handler/alerts"
	"winterflow/internal/domain/alert"
)

// deadHook is a generic webhook on a closed local port: saving works, a
// send fails fast. Its path doubles as a secret the API must never echo.
const deadHook = "generic://127.0.0.1:9/hook-SECRETPATH?disabletls=yes"

// TestAlertChannelsNeverReturnURL: a channel's URL is stored encrypted and
// never comes back, not even inside a failed test's error.
func TestAlertChannelsNeverReturnURL(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	var ch alert.Channel
	owner.post(t, "/api/v1/alerts/save-channel", map[string]any{
		"name": "Ops", "url": deadHook, "fields": map[string]string{"ui_service": "webhook"},
		"is_default": true, "enabled": true,
	}).expect(t, http.StatusOK).data(t, &ch)
	if ch.ID == "" || ch.Service != "generic" || ch.Fields["ui_service"] != "webhook" || !ch.IsDefault {
		t.Fatalf("channel = %+v", ch)
	}
	list := owner.get(t, "/api/v1/alerts/get-channels", nil).expect(t, http.StatusOK)
	if strings.Contains(string(list.Body), "SECRETPATH") || strings.Contains(string(list.Body), "127.0.0.1") {
		t.Fatalf("URL leaked: %s", list.Body)
	}

	var res alerts.TestChannelResponse
	tested := owner.post(t, "/api/v1/alerts/test-channel", map[string]any{"id": ch.ID}).expect(t, http.StatusOK)
	tested.data(t, &res)
	if res.OK || res.Error == "" || strings.Contains(string(tested.Body), "SECRETPATH") {
		t.Fatalf("test result = %s", tested.Body)
	}
	var stored []alert.Channel
	owner.get(t, "/api/v1/alerts/get-channels", nil).expect(t, http.StatusOK).data(t, &stored)
	if len(stored) != 1 || stored[0].LastError == "" || strings.Contains(stored[0].LastError, "SECRETPATH") {
		t.Fatalf("last error = %+v", stored)
	}
	owner.post(t, "/api/v1/alerts/test-channel", map[string]any{"url": deadHook}).expect(t, http.StatusOK)
	owner.post(t, "/api/v1/alerts/test-channel", map[string]any{"url": "bogus://x"}).expect(t, http.StatusBadRequest)

	// An update without a url keeps the destination.
	var renamed alert.Channel
	owner.post(t, "/api/v1/alerts/save-channel", map[string]any{
		"id": ch.ID, "name": "Pager", "url": "", "fields": map[string]string{"ui_service": "ignored"},
		"is_default": false, "enabled": true,
	}).expect(t, http.StatusOK).data(t, &renamed)
	if renamed.Name != "Pager" || renamed.Service != "generic" || renamed.Fields["ui_service"] != "webhook" || renamed.IsDefault {
		t.Fatalf("renamed = %+v", renamed)
	}
	owner.post(t, "/api/v1/alerts/save-channel", map[string]any{"name": "No URL", "url": ""}).expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/alerts/delete-channel", map[string]any{"id": ch.ID}).expect(t, http.StatusOK)
	owner.post(t, "/api/v1/alerts/delete-channel", map[string]any{"id": ch.ID}).expect(t, http.StatusBadRequest)
}

// TestAlertRuleRejectsForeignChannel: rules and overrides may only route
// to the org's own channels; kind and metric are fixed after creation.
func TestAlertRuleRejectsForeignChannel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	var defaults []alert.Rule
	owner.get(t, "/api/v1/alerts/get-rules", nil).expect(t, http.StatusOK).data(t, &defaults)
	if len(defaults) != len(alert.DefaultRules()) {
		t.Fatalf("defaults = %+v", defaults)
	}
	unknown := "00000000-0000-0000-0000-0000000000c1"
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"target_kind": "server", "metric": "cpu", "threshold": 80, "duration_minutes": 5,
		"channel_ids": []string{unknown}, "enabled": true,
	}).expect(t, http.StatusBadRequest)

	var rule alert.Rule
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"target_kind": "app", "metric": "disk", "threshold": 50, "duration_minutes": 5,
		"channel_ids": []string{}, "enabled": true,
	}).expect(t, http.StatusOK).data(t, &rule)
	if rule.ID == "" || rule.ChannelIDs == nil || len(rule.ChannelIDs) != 0 {
		t.Fatalf("rule = %+v", rule)
	}
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"id": rule.ID, "target_kind": "app", "metric": "memory", "threshold": 50, "duration_minutes": 5, "enabled": true,
	}).expect(t, http.StatusBadRequest)
	var updated alert.Rule
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"id": rule.ID, "target_kind": "app", "metric": "disk", "threshold": 75, "duration_minutes": 5,
		"channel_ids": nil, "enabled": false,
	}).expect(t, http.StatusOK).data(t, &updated)
	if updated.Threshold != 75 || updated.Enabled || updated.ChannelIDs != nil {
		t.Fatalf("updated = %+v", updated)
	}
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"target_kind": "server", "metric": "cpu", "threshold": 120, "duration_minutes": 5,
	}).expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/alerts/delete-rule", map[string]any{"id": rule.ID}).expect(t, http.StatusOK)
	owner.post(t, "/api/v1/alerts/delete-rule", map[string]any{"id": rule.ID}).expect(t, http.StatusBadRequest)
}
```

Add these probes to the `probes` map in `internal/app/web/contract/matrix_test.go`, before the `"POST /api/v1/org/create-user"` entry. Then run `gofmt -w internal/app/web/contract/matrix_test.go`, which realigns the map.

```go
	"POST /api/v1/alerts/save-channel":   {malformed: post("/api/v1/alerts/save-channel", map[string]any{"name": "", "url": "ntfy://ntfy.sh/t"})},
	"POST /api/v1/alerts/delete-channel": {malformed: post("/api/v1/alerts/delete-channel", map[string]any{"id": ""})},
	"POST /api/v1/alerts/test-channel":   {malformed: post("/api/v1/alerts/test-channel", map[string]any{})},
	"POST /api/v1/alerts/save-rule":      {malformed: post("/api/v1/alerts/save-rule", map[string]any{"target_kind": "server", "metric": "cpu", "threshold": 0, "duration_minutes": 5})},
	"POST /api/v1/alerts/delete-rule":    {malformed: post("/api/v1/alerts/delete-rule", map[string]any{"id": ""})},
```

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web/contract/ -run 'TestAlert|TestProbesCoverTable'`
Expected: FAIL. The new routes answer 404, and `TestProbesCoverTable` reports probes that name no route.

- [ ] **Step 2: Write the handler**

`internal/app/web/handler/alerts/handler.go`:

```go
// Package alerts serves /api/v1/alerts/*: the caller's organization's
// notification channels (Shoutrrr URLs stored encrypted and never
// returned), alert rules, per-target overrides, the history and the firing
// alerts. Every member of the org may do all of it; server-addressed calls
// also check that the server belongs to the caller's org.
package alerts

import (
	"context"
	"errors"
	"net/http"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// OrgResolver resolves the caller's organization (port.UserService).
type OrgResolver interface {
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
}

// AppLookup finds an app (port.AppRepository).
type AppLookup interface {
	GetApp(ctx context.Context, appID string) (model.App, error)
}

// Sealer encrypts a channel URL (secretbox.Box).
type Sealer interface {
	Seal(plain string) (string, error)
}

// Tester sends test notifications (alerting.Notifier): to a saved channel
// (outcome recorded on it) or to a draft URL. Both return the error text,
// "" on success.
type Tester interface {
	SendTo(ctx context.Context, ch alert.Channel, title, body string) string
	SendURL(ctx context.Context, rawURL, title, body string) string
}

// Deps are the handler's collaborators (all from bootstrap.Deps). Box is
// nil without JWT_SECRET: saving and testing channels then answer 400.
type Deps struct {
	Logger  *logger.Logger
	Users   OrgResolver
	Servers webutil.ServerAccess
	Apps    AppLookup
	Alerts  port.AlertRepository
	Box     Sealer
	Tester  Tester
}

// Handler serves /api/v1/alerts/*.
type Handler struct {
	log    *logger.Logger
	users  OrgResolver
	access webutil.ServerAccess
	apps   AppLookup
	store  port.AlertRepository
	box    Sealer
	tester Tester
	now    func() time.Time
}

// NewHandler builds the handler.
func NewHandler(d *Deps) *Handler {
	return &Handler{
		log: d.Logger, users: d.Users, access: d.Servers, apps: d.Apps,
		store: d.Alerts, box: d.Box, tester: d.Tester, now: time.Now,
	}
}

// errNoBox is the answer to channel writes without JWT_SECRET.
const errNoBox = "channel encryption is unavailable: set JWT_SECRET"

// callerOrg resolves the authenticated user and their organization,
// writing the error response itself on failure.
func (h *Handler) callerOrg(w http.ResponseWriter, r *http.Request) (userID, orgID string, ok bool) {
	userID, ok = webutil.RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	orgID, err := h.users.PrimaryOrganizationID(r.Context(), userID)
	if err != nil {
		h.log.Error("alerts: resolve caller org", "error", err, "user_id", userID)
		webutil.Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	return userID, orgID, true
}

// requireTarget authorizes a (server, app) target: the server must be the
// caller's (403) and belong to the org whose alerts are being edited (403);
// an app must exist on that server (400).
func (h *Handler) requireTarget(w http.ResponseWriter, r *http.Request, userID, orgID string, t alert.Target) bool {
	if t.ServerID == "" {
		webutil.Error(w, "server_id is required", nil)
		return false
	}
	if !webutil.RequireServerAccess(w, r, h.access, userID, t.ServerID) {
		return false
	}
	owner, err := h.store.ServerOrgID(r.Context(), t.ServerID)
	if err != nil || owner != orgID {
		webutil.Forbidden(w)
		return false
	}
	if t.AppID == "" {
		return true
	}
	a, err := h.apps.GetApp(r.Context(), t.AppID)
	if err != nil || a.ServerID != t.ServerID {
		webutil.Error(w, "unknown app", nil)
		return false
	}
	return true
}

// requireChannels checks every id names one of the org's channels.
func (h *Handler) requireChannels(w http.ResponseWriter, r *http.Request, orgID string, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	chans, err := h.store.ListChannels(r.Context(), orgID)
	if err != nil {
		h.fail(w, "load channels", err)
		return false
	}
	known := make(map[string]bool, len(chans))
	for _, c := range chans {
		known[c.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			webutil.Error(w, "unknown channel "+id, nil)
			return false
		}
	}
	return true
}

// fail logs an unexpected store error and answers a generic 400; not
// found becomes its own message.
func (h *Handler) fail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, alert.ErrNotFound) {
		webutil.Error(w, what+": not found", nil)
		return
	}
	h.log.Error("alerts: "+what, "error", err)
	webutil.Error(w, "failed to "+what, nil)
}

// IDRequest addresses one channel or rule.
type IDRequest struct {
	ID string `json:"id"`
}
```

`internal/app/web/handler/alerts/channels.go`:

```go
package alerts

import (
	"net/http"
	"strings"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
	"winterflow/internal/infra/notify"
)

// Limits of a channel's name and of its non-secret form fields.
const (
	maxNameLen    = 64
	maxFields     = 16
	maxFieldKey   = 32
	maxFieldValue = 256
)

// SaveChannelRequest is the body of POST /api/v1/alerts/save-channel. An
// empty id creates. On update an empty url keeps the stored destination
// (url, service and fields); only name, default and enabled change.
type SaveChannelRequest struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Fields    map[string]string `json:"fields"`
	IsDefault bool              `json:"is_default"`
	Enabled   bool              `json:"enabled"`
}

// TestChannelRequest is the body of POST /api/v1/alerts/test-channel:
// exactly one of a saved channel id or a draft url.
type TestChannelRequest struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// TestChannelResponse is the outcome of a test send (200 either way).
type TestChannelResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

const (
	testTitle = "🔔 Winterflow test"
	testBody  = "This is a test notification from Winterflow. If you can read it, the channel works."
)

// GetChannels serves GET /api/v1/alerts/get-channels.
func (h *Handler) GetChannels(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	chans, err := h.store.ListChannels(r.Context(), orgID)
	if err != nil {
		h.fail(w, "load channels", err)
		return
	}
	webutil.Success(w, "ok", chans)
}

// SaveChannel serves POST /api/v1/alerts/save-channel.
func (h *Handler) SaveChannel(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[SaveChannelRequest](w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxNameLen {
		webutil.Error(w, "name is required (max 64 chars)", nil)
		return
	}
	if msg := checkFields(req.Fields); msg != "" {
		webutil.Error(w, msg, nil)
		return
	}
	if h.box == nil {
		webutil.Error(w, errNoBox, nil)
		return
	}
	ch := alert.Channel{OrgID: orgID, Name: name, IsDefault: req.IsDefault, Enabled: req.Enabled}
	if req.ID != "" {
		stored, err := h.store.GetChannel(r.Context(), orgID, req.ID)
		if err != nil {
			h.fail(w, "load channel", err)
			return
		}
		ch.ID, ch.CreatedAt, ch.LastSentAt = stored.ID, stored.CreatedAt, stored.LastSentAt
		ch.Service, ch.Fields, ch.URLEncrypted, ch.LastError = stored.Service, stored.Fields, stored.URLEncrypted, stored.LastError
	} else if req.URL == "" {
		webutil.Error(w, "url is required", nil)
		return
	}
	if req.URL != "" {
		service, err := notify.Service(req.URL)
		if err != nil {
			webutil.Error(w, err.Error(), nil)
			return
		}
		sealed, err := h.box.Seal(req.URL)
		if err != nil {
			h.fail(w, "encrypt channel url", err)
			return
		}
		ch.Service, ch.URLEncrypted, ch.Fields, ch.LastError = service, sealed, req.Fields, ""
	}
	if ch.Fields == nil {
		ch.Fields = map[string]string{}
	}
	if ch.ID == "" {
		created, err := h.store.CreateChannel(r.Context(), ch)
		if err != nil {
			h.fail(w, "save channel", err)
			return
		}
		webutil.Success(w, "ok", created)
		return
	}
	if err := h.store.UpdateChannel(r.Context(), ch); err != nil {
		h.fail(w, "save channel", err)
		return
	}
	webutil.Success(w, "ok", ch)
}

// DeleteChannel serves POST /api/v1/alerts/delete-channel.
func (h *Handler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[IDRequest](w, r)
	if !ok {
		return
	}
	if req.ID == "" {
		webutil.Error(w, "id is required", nil)
		return
	}
	if err := h.store.DeleteChannel(r.Context(), orgID, req.ID); err != nil {
		h.fail(w, "delete channel", err)
		return
	}
	webutil.Success(w, "ok", nil)
}

// TestChannel serves POST /api/v1/alerts/test-channel: sends a test
// notification now (10 s deadline) and reports the outcome.
func (h *Handler) TestChannel(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[TestChannelRequest](w, r)
	if !ok {
		return
	}
	if (req.ID == "") == (req.URL == "") {
		webutil.Error(w, "send exactly one of id or url", nil)
		return
	}
	var errText string
	if req.URL != "" {
		if _, err := notify.Service(req.URL); err != nil {
			webutil.Error(w, err.Error(), nil)
			return
		}
		errText = h.tester.SendURL(r.Context(), req.URL, testTitle, testBody)
	} else {
		if h.box == nil {
			webutil.Error(w, errNoBox, nil)
			return
		}
		ch, err := h.store.GetChannel(r.Context(), orgID, req.ID)
		if err != nil {
			h.fail(w, "load channel", err)
			return
		}
		errText = h.tester.SendTo(r.Context(), ch, testTitle, testBody)
	}
	webutil.Success(w, "ok", TestChannelResponse{OK: errText == "", Error: errText})
}

// checkFields bounds the non-secret form fields; "" when they are fine.
func checkFields(f map[string]string) string {
	if len(f) > maxFields {
		return "too many fields (max 16)"
	}
	for k, v := range f {
		if k == "" || len(k) > maxFieldKey || len(v) > maxFieldValue {
			return "field names are 1-32 chars and values at most 256"
		}
	}
	return ""
}
```

`internal/app/web/handler/alerts/rules.go`:

```go
package alerts

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
)

// SaveRuleRequest is the body of POST /api/v1/alerts/save-rule. An empty
// id creates; on update target_kind and metric must match the stored
// rule. channel_ids: null = the org's default channels, [] = none
// (History only), else those channels.
type SaveRuleRequest struct {
	ID              string           `json:"id"`
	TargetKind      alert.TargetKind `json:"target_kind" enum:"server,app"`
	Metric          alert.Metric     `json:"metric" enum:"down,cpu,memory,disk,load1,load5,load15,bandwidth,temperature"`
	Threshold       float64          `json:"threshold"`
	DurationMinutes int              `json:"duration_minutes"`
	ChannelIDs      []string         `json:"channel_ids"`
	Enabled         bool             `json:"enabled"`
}

// GetRules serves GET /api/v1/alerts/get-rules. It seeds the default
// rules of an org that has not had them yet first, so a new org sees them
// at once.
func (h *Handler) GetRules(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	if _, err := h.store.SeedDefaults(r.Context()); err != nil {
		h.log.Warn("alerts: seed default rules", "error", err)
	}
	rules, err := h.store.ListRules(r.Context(), orgID)
	if err != nil {
		h.fail(w, "load rules", err)
		return
	}
	webutil.Success(w, "ok", rules)
}

// SaveRule serves POST /api/v1/alerts/save-rule.
func (h *Handler) SaveRule(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[SaveRuleRequest](w, r)
	if !ok {
		return
	}
	rule := alert.Rule{
		ID: req.ID, OrgID: orgID, TargetKind: req.TargetKind, Metric: req.Metric, Threshold: req.Threshold,
		DurationMinutes: req.DurationMinutes, ChannelIDs: req.ChannelIDs, Enabled: req.Enabled,
	}
	rule.Normalize()
	if err := rule.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	if !h.requireChannels(w, r, orgID, rule.ChannelIDs) {
		return
	}
	if rule.ID == "" {
		created, err := h.store.CreateRule(r.Context(), rule)
		if err != nil {
			h.fail(w, "save rule", err)
			return
		}
		webutil.Success(w, "ok", created)
		return
	}
	stored, err := h.store.GetRule(r.Context(), orgID, rule.ID)
	if err != nil {
		h.fail(w, "load rule", err)
		return
	}
	if stored.TargetKind != rule.TargetKind || stored.Metric != rule.Metric {
		webutil.Error(w, "a rule's target kind and metric cannot change; create a new rule", nil)
		return
	}
	if err := h.store.UpdateRule(r.Context(), rule); err != nil {
		h.fail(w, "save rule", err)
		return
	}
	rule.CreatedAt = stored.CreatedAt
	webutil.Success(w, "ok", rule)
}

// DeleteRule serves POST /api/v1/alerts/delete-rule. Its overrides and
// state go with it; its history stays.
func (h *Handler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[IDRequest](w, r)
	if !ok {
		return
	}
	if req.ID == "" {
		webutil.Error(w, "id is required", nil)
		return
	}
	if err := h.store.DeleteRule(r.Context(), orgID, req.ID); err != nil {
		h.fail(w, "delete rule", err)
		return
	}
	webutil.Success(w, "ok", nil)
}
```

`internal/app/web/handler/alerts/routes.go`:

```go
package alerts

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/alert"
)

// Routes is the alerts slice of the route table. Everything acts on the
// caller's org; target-addressed routes also check the server is the
// caller's (403).
func (h *Handler) Routes(api.Guards) []api.Route {
	bad := []int{http.StatusBadRequest}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-channels", Handler: h.GetChannels, Auth: api.User,
			Summary: "List the org's notification channels", Tag: "alerts",
			Description: "Channel URLs carry secrets and are never returned: `service` is the URL scheme and " +
				"`fields` the non-secret form values the client stored with it.",
			Response: []alert.Channel{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/save-channel", Handler: h.SaveChannel, Auth: api.User,
			Summary: "Create or update a notification channel", Tag: "alerts",
			Description: "`url` is a Shoutrrr URL (telegram://, discord://, ntfy://, smtp://, generic://, …), " +
				"stored encrypted. An empty `id` creates. On update an empty `url` keeps the stored destination " +
				"(url, service and fields); only name, default and enabled change.",
			Request: SaveChannelRequest{}, Response: alert.Channel{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/delete-channel", Handler: h.DeleteChannel, Auth: api.User,
			Summary: "Delete a notification channel", Tag: "alerts",
			Request: IDRequest{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/test-channel", Handler: h.TestChannel, Auth: api.User,
			Summary: "Send a test notification", Tag: "alerts",
			Description: "Send exactly one of `id` (a saved channel; the outcome is recorded on it) or `url` " +
				"(a draft). Answers 200 with `ok` false and the error when the service refuses.",
			Request: TestChannelRequest{}, Response: TestChannelResponse{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-rules", Handler: h.GetRules, Auth: api.User,
			Summary: "List the org's alert rules", Tag: "alerts",
			Response: []alert.Rule{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/save-rule", Handler: h.SaveRule, Auth: api.User,
			Summary: "Create or update an alert rule", Tag: "alerts",
			Description: "Fires when the metric's average over `duration_minutes` (1-60) is above `threshold` " +
				"(down rules: when the server or app is down that long). `channel_ids`: null = the org's default " +
				"channels, [] = none (History only). `target_kind` and `metric` cannot change on update.",
			Request: SaveRuleRequest{}, Response: alert.Rule{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/delete-rule", Handler: h.DeleteRule, Auth: api.User,
			Summary: "Delete an alert rule", Tag: "alerts",
			Request: IDRequest{}, Errors: bad,
		},
	}
}
```

- [ ] **Step 3: Register the routes**

In `internal/app/web/routes.go`:
- import `halerts "winterflow/internal/app/web/handler/alerts"`;
- add `alertsAPI.Routes(g),` after `monitoringAPI.Routes(g),` in the table loop;
- build the handler just before `usersAPI := …`:

```go
	// Typed nils must not become non-nil interfaces: without JWT_SECRET
	// there is no box, and the spec-only route table has no notifier.
	var box halerts.Sealer
	if d.ChannelBox != nil {
		box = d.ChannelBox
	}
	var tester halerts.Tester
	if d.AlertNotifier != nil {
		tester = d.AlertNotifier
	}
	alertsAPI := halerts.NewHandler(&halerts.Deps{
		Logger:  d.Log,
		Users:   d.UserService,
		Servers: d.ServerRepository,
		Apps:    d.AppRepository,
		Alerts:  d.AlertRepository,
		Box:     box,
		Tester:  tester,
	})
```

- [ ] **Step 4: Regenerate and run the suite**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp make openapi
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web -run TestRoutesGolden -update
GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/app/web/...
WF_CONTRACT_GUARD=require GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web/...
```

Expected: every package `ok`. The guard is satisfied: each new route answers a call below 400 in `TestAlertChannelsNeverReturnURL` or `TestAlertRuleRejectsForeignChannel`. This task's state was verified on its own, on top of Task 7.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web api/openapi.yaml
git commit -m "feat(alerts): API for notification channels and alert rules

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 9: HTTP API for overrides, history and firing alerts; overview badge count

**Files:**
- Create: `internal/app/web/handler/alerts/overrides.go`, `internal/app/web/handler/alerts/history.go`
- Modify:
  - `internal/app/web/handler/alerts/routes.go`;
  - `internal/app/web/handler/monitoring/handler.go`, `overview.go`;
  - `internal/app/web/routes.go`.
- Test:
  - `internal/app/web/contract/alerts_test.go`, `matrix_test.go`;
  - `internal/app/web/handler/monitoring/handler_test.go`.
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: `(*Handler).requireTarget`, `callerOrg`, `fail`, `requireChannels` (Task 8); `port.AlertRepository.CountFiring/ListActive/ListEvents/Directory` (Task 4).
- Produces:
  - query types `TargetQuery`, `EventsQuery`, `ActiveQuery`;
  - `DeleteOverrideRequest`;
  - `EventsResponse{Events []alert.Event; NextCursor string}`;
  - `ActiveAlert{alert.Active; ServerName, AppName string}`;
  - routes:
    - `GET /api/v1/alerts/get-overrides?server_id=&app_id=` → `[]alert.Override`;
    - `POST /api/v1/alerts/save-override` (body `alert.Override`) → `alert.Override`;
    - `POST /api/v1/alerts/delete-override`;
    - `GET /api/v1/alerts/get-events?server_id=&app_id=&rule_id=&kind=&cursor=&limit=` → `EventsResponse`. The cursor is `"<unix>_<event id>"`, and `next_cursor` is set when the page is full;
    - `GET /api/v1/alerts/get-active?server_id=&app_id=` → `[]ActiveAlert`;
  - `monitoring.ServerOverview.AlertsFiring int` (`json:"alerts_firing"`) and `monitoring.Deps.Alerts FiringCounter`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/web/contract/alerts_test.go`. Add the imports `"encoding/json"`, `"net/url"`, `"time"`, `"winterflow/internal/app/web/handler/monitoring"`, `"winterflow/internal/domain/metrics"`, `"winterflow/internal/domain/model"` and `"winterflow/internal/infra/transport/bus"`.

```go
// TestAlertOverrides: per-target overrides for the server and one app.
func TestAlertOverrides(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	appID := "00000000-0000-0000-0000-0000000000a1"
	if err := e.deps.AppRepository.SaveApp(e.dbCtx(t), model.App{ID: appID, ServerID: sid, Name: "nextcloud"}); err != nil {
		t.Fatal(err)
	}
	var rules []alert.Rule
	owner.get(t, "/api/v1/alerts/get-rules", nil).expect(t, http.StatusOK).data(t, &rules)
	var cpu, appDown alert.Rule
	for _, r := range rules {
		switch {
		case r.TargetKind == alert.TargetServer && r.Metric == alert.MetricCPU:
			cpu = r
		case r.TargetKind == alert.TargetApp && r.Metric == alert.MetricDown:
			appDown = r
		}
	}

	var saved alert.Override
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{
		"rule_id": cpu.ID, "server_id": sid, "app_id": "", "disabled": false, "threshold": 95,
		"duration_minutes": nil, "channel_ids": nil,
	}).expect(t, http.StatusOK).data(t, &saved)
	if saved.Threshold == nil || *saved.Threshold != 95 {
		t.Fatalf("saved = %+v", saved)
	}
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{
		"rule_id": appDown.ID, "server_id": sid, "app_id": appID, "disabled": true,
	}).expect(t, http.StatusOK)
	// The app rule needs an app target; the down rule takes no threshold;
	// an app of no server is unknown.
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{"rule_id": appDown.ID, "server_id": sid, "app_id": ""}).
		expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{"rule_id": appDown.ID, "server_id": sid, "app_id": appID, "threshold": 3}).
		expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{"rule_id": appDown.ID, "server_id": sid, "app_id": anApp}).
		expect(t, http.StatusBadRequest)

	var list []alert.Override
	owner.get(t, "/api/v1/alerts/get-overrides", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &list)
	if len(list) != 1 || list[0].RuleID != cpu.ID {
		t.Fatalf("server overrides = %+v", list)
	}
	owner.get(t, "/api/v1/alerts/get-overrides", url.Values{"server_id": {sid}, "app_id": {appID}}).
		expect(t, http.StatusOK).data(t, &list)
	if len(list) != 1 || !list[0].Disabled {
		t.Fatalf("app overrides = %+v", list)
	}
	owner.post(t, "/api/v1/alerts/delete-override", map[string]any{"rule_id": cpu.ID, "server_id": sid, "app_id": ""}).
		expect(t, http.StatusOK)
	owner.get(t, "/api/v1/alerts/get-overrides", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &list)
	if len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
}

// TestAlertFiresFromMetrics: a sample above a 1-minute rule fires; the
// alert shows in the history, the firing list and the overview badge.
func TestAlertFiresFromMetrics(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	var rule alert.Rule
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"target_kind": "server", "metric": "cpu", "threshold": 50, "duration_minutes": 1,
		"channel_ids": []string{}, "enabled": true,
	}).expect(t, http.StatusOK).data(t, &rule)

	cpu := 99.0
	body, err := json.Marshal(metrics.Sample{TS: time.Now().Unix(), Server: metrics.ServerSample{CPU: &cpu}})
	if err != nil {
		t.Fatal(err)
	}
	e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventServerMetrics, Payload: body})

	var active []alerts.ActiveAlert
	eventually(t, func() bool {
		owner.get(t, "/api/v1/alerts/get-active", nil).expect(t, http.StatusOK).data(t, &active)
		return len(active) == 1
	})
	if a := active[0]; a.RuleID != rule.ID || a.Value != 99 || a.Threshold != 50 || a.ServerName == "" {
		t.Fatalf("active = %+v", a)
	}
	owner.get(t, "/api/v1/alerts/get-active", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &active)
	if len(active) != 1 {
		t.Fatalf("server filter = %+v", active)
	}
	owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/alerts/get-active", Query: url.Values{"app_id": {anApp}}, Invalid: true}).
		expect(t, http.StatusBadRequest)

	var page alerts.EventsResponse
	owner.get(t, "/api/v1/alerts/get-events", url.Values{"server_id": {sid}, "limit": {"1"}}).
		expect(t, http.StatusOK).data(t, &page)
	if len(page.Events) != 1 || page.Events[0].Kind != alert.EventFired || !strings.HasPrefix(page.Events[0].Title, "🔥") ||
		page.NextCursor == "" || page.Events[0].Deliveries == nil {
		t.Fatalf("history = %+v", page)
	}
	owner.get(t, "/api/v1/alerts/get-events", url.Values{"cursor": {page.NextCursor}}).expect(t, http.StatusOK).data(t, &page)
	if len(page.Events) != 0 || page.NextCursor != "" {
		t.Fatalf("second page = %+v", page)
	}
	owner.get(t, "/api/v1/alerts/get-events", url.Values{"kind": {"resolved"}}).expect(t, http.StatusOK).data(t, &page)
	if len(page.Events) != 0 {
		t.Fatalf("resolved filter = %+v", page)
	}
	owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/alerts/get-events", Query: url.Values{"cursor": {"nope"}}, Invalid: true}).
		expect(t, http.StatusBadRequest)

	var ov monitoring.OverviewResponse
	owner.get(t, "/api/v1/metrics/overview", nil).expect(t, http.StatusOK).data(t, &ov)
	if len(ov.Servers) != 1 || ov.Servers[0].AlertsFiring != 1 {
		t.Fatalf("overview = %+v", ov)
	}
}
```

Add these probes to `matrix_test.go`, next to the Task 8 alert probes, then run `gofmt -w`:

```go
	"GET /api/v1/alerts/get-overrides": {valid: func(s string) call { return get("/api/v1/alerts/get-overrides", sq(s)) }, malformed: bad("GET", "/api/v1/alerts/get-overrides")},
	"POST /api/v1/alerts/save-override": {valid: func(s string) call {
		return post("/api/v1/alerts/save-override", map[string]any{"rule_id": "00000000-0000-0000-0000-000000000099", "server_id": s, "app_id": "", "disabled": true})
	}, malformed: bad("POST", "/api/v1/alerts/save-override")},
	"POST /api/v1/alerts/delete-override": {valid: func(s string) call {
		return post("/api/v1/alerts/delete-override", map[string]any{"rule_id": "00000000-0000-0000-0000-000000000099", "server_id": s, "app_id": ""})
	}, malformed: bad("POST", "/api/v1/alerts/delete-override")},
	"GET /api/v1/alerts/get-events": {valid: func(s string) call { return get("/api/v1/alerts/get-events", sq(s)) },
		malformed: call{Method: http.MethodGet, Path: "/api/v1/alerts/get-events", Query: url.Values{"kind": {"bogus"}}, Invalid: true}},
	"GET /api/v1/alerts/get-active": {valid: func(s string) call { return get("/api/v1/alerts/get-active", sq(s)) },
		malformed: call{Method: http.MethodGet, Path: "/api/v1/alerts/get-active", Query: url.Values{"app_id": {anApp}}, Invalid: true}},
```

`TestForeignServer` now checks all five against another org's server, expecting 403. Its positive control expects the caller's own server not to answer 401 or 403. For example, `save-override` with an unknown rule answers 400.

Append to `internal/app/web/handler/monitoring/handler_test.go`:

```go
// fakeCounter serves CountFiring.
type fakeCounter struct {
	counts map[string]int
	err    error
}

func (f fakeCounter) CountFiring(context.Context) (map[string]int, error) { return f.counts, f.err }

// The overview carries each server's firing-alert count; a count failure
// is logged and shows zero.
func TestOverviewCountsFiringAlerts(t *testing.T) {
	h, _, _ := newHandler(&fakeRepo{}, fakeServers{})
	h.alerts = fakeCounter{counts: map[string]int{"srv-1": 2, "srv-other": 5}}
	if s := decode[OverviewResponse](t, get(h.Overview, "/x", "u1")).Servers[0]; s.AlertsFiring != 2 {
		t.Fatalf("alerts_firing = %d", s.AlertsFiring)
	}
	h.alerts = fakeCounter{err: errors.New("db down")}
	w := get(h.Overview, "/x", "u1")
	if w.Code != http.StatusOK || decode[OverviewResponse](t, w).Servers[0].AlertsFiring != 0 {
		t.Fatalf("count error: %d %s", w.Code, w.Body.String())
	}
}
```

Run: `GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web/contract/ ./internal/app/web/handler/monitoring/ -run 'TestAlert|TestProbesCoverTable|TestOverview'`
Expected: FAIL (404s, unknown probe routes, `h.alerts undefined`).

- [ ] **Step 2: Write overrides and history**

`internal/app/web/handler/alerts/overrides.go`:

```go
package alerts

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
)

// TargetQuery addresses a server (app_id empty) or one of its apps.
type TargetQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Target server id."`
	AppID    string `query:"app_id" description:"Target app id; empty for the server itself."`
}

// DeleteOverrideRequest is the body of POST /api/v1/alerts/delete-override.
type DeleteOverrideRequest struct {
	RuleID   string `json:"rule_id"`
	ServerID string `json:"server_id"`
	AppID    string `json:"app_id"`
}

// GetOverrides serves GET /api/v1/alerts/get-overrides: one target's
// overrides, by rule.
func (h *Handler) GetOverrides(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	t := alert.Target{ServerID: q.Get("server_id"), AppID: q.Get("app_id")}
	if !h.requireTarget(w, r, userID, orgID, t) {
		return
	}
	list, err := h.store.TargetOverrides(r.Context(), t)
	if err != nil {
		h.fail(w, "load overrides", err)
		return
	}
	webutil.Success(w, "ok", list)
}

// SaveOverride serves POST /api/v1/alerts/save-override: create or replace
// the override of one rule for one target.
func (h *Handler) SaveOverride(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	o, ok := webutil.DecodeBody[alert.Override](w, r)
	if !ok {
		return
	}
	if !h.requireTarget(w, r, userID, orgID, alert.Target{ServerID: o.ServerID, AppID: o.AppID}) {
		return
	}
	rule, err := h.store.GetRule(r.Context(), orgID, o.RuleID)
	if err != nil {
		h.fail(w, "load rule", err)
		return
	}
	if err := o.Validate(rule); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	if !h.requireChannels(w, r, orgID, o.ChannelIDs) {
		return
	}
	if err := h.store.SaveOverride(r.Context(), o); err != nil {
		h.fail(w, "save override", err)
		return
	}
	webutil.Success(w, "ok", o)
}

// DeleteOverride serves POST /api/v1/alerts/delete-override: the target
// goes back to the rule's settings.
func (h *Handler) DeleteOverride(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[DeleteOverrideRequest](w, r)
	if !ok {
		return
	}
	t := alert.Target{ServerID: req.ServerID, AppID: req.AppID}
	if !h.requireTarget(w, r, userID, orgID, t) {
		return
	}
	if req.RuleID == "" {
		webutil.Error(w, "rule_id is required", nil)
		return
	}
	if err := h.store.DeleteOverride(r.Context(), alert.Key{RuleID: req.RuleID, Target: t}); err != nil {
		h.fail(w, "delete override", err)
		return
	}
	webutil.Success(w, "ok", nil)
}
```

`internal/app/web/handler/alerts/history.go`:

```go
package alerts

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
)

const (
	defaultEventsPage = 50
	maxEventsPage     = 200
)

// EventsQuery filters and pages the history.
type EventsQuery struct {
	ServerID string `query:"server_id" description:"Only this server's events (and its apps')."`
	AppID    string `query:"app_id" description:"Only this app's events; needs server_id."`
	RuleID   string `query:"rule_id" description:"Only this rule's events."`
	Kind     string `query:"kind" enum:"fired,resolved" description:"Only fired or only resolved events."`
	Cursor   string `query:"cursor" description:"next_cursor of the previous page."`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" description:"Page size (default 50)."`
}

// EventsResponse is one history page, newest first. next_cursor is empty
// on the last page.
type EventsResponse struct {
	Events     []alert.Event `json:"events"`
	NextCursor string        `json:"next_cursor"`
}

// ActiveQuery narrows the firing alerts to a server or one of its apps.
type ActiveQuery struct {
	ServerID string `query:"server_id" description:"Only this server's alerts (and its apps')."`
	AppID    string `query:"app_id" description:"Only this app's alerts; needs server_id."`
}

// ActiveAlert is a firing alert with its target's names.
type ActiveAlert struct {
	alert.Active
	ServerName string `json:"server_name"`
	AppName    string `json:"app_name"`
}

// scope reads the optional server_id/app_id filter and authorizes it.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request, userID, orgID string) (alert.Target, bool) {
	q := r.URL.Query()
	t := alert.Target{ServerID: q.Get("server_id"), AppID: q.Get("app_id")}
	if t.ServerID == "" {
		if t.AppID != "" {
			webutil.Error(w, "app_id needs server_id", nil)
			return t, false
		}
		return t, true
	}
	return t, h.requireTarget(w, r, userID, orgID, t)
}

// GetEvents serves GET /api/v1/alerts/get-events.
func (h *Handler) GetEvents(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	t, ok := h.scope(w, r, userID, orgID)
	if !ok {
		return
	}
	q := r.URL.Query()
	query := alert.EventQuery{OrgID: orgID, ServerID: t.ServerID, AppID: t.AppID, RuleID: q.Get("rule_id"), Limit: defaultEventsPage}
	switch kind := alert.EventKind(q.Get("kind")); kind {
	case "", alert.EventFired, alert.EventResolved:
		query.Kind = kind
	default:
		webutil.Error(w, "kind must be fired or resolved", nil)
		return
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxEventsPage {
			webutil.Error(w, "limit must be 1 to 200", nil)
			return
		}
		query.Limit = n
	}
	if c := q.Get("cursor"); c != "" {
		at, id, err := parseCursor(c)
		if err != nil {
			webutil.Error(w, "invalid cursor", nil)
			return
		}
		query.BeforeAt, query.BeforeID = at, id
	}
	events, err := h.store.ListEvents(r.Context(), query)
	if err != nil {
		h.fail(w, "load history", err)
		return
	}
	resp := EventsResponse{Events: events}
	if n := len(events); n == query.Limit {
		resp.NextCursor = formatCursor(events[n-1])
	}
	webutil.Success(w, "ok", resp)
}

// GetActive serves GET /api/v1/alerts/get-active: the org's firing
// alerts, newest first, optionally for one server or app.
func (h *Handler) GetActive(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	t, ok := h.scope(w, r, userID, orgID)
	if !ok {
		return
	}
	active, err := h.store.ListActive(r.Context(), orgID)
	if err != nil {
		h.fail(w, "load firing alerts", err)
		return
	}
	dir, err := h.store.Directory(r.Context())
	if err != nil {
		h.fail(w, "load names", err)
		return
	}
	out := []ActiveAlert{}
	for _, a := range active {
		if (t.ServerID != "" && a.ServerID != t.ServerID) || (t.AppID != "" && a.AppID != t.AppID) {
			continue
		}
		item := ActiveAlert{Active: a, ServerName: dir.Servers[a.ServerID].Name}
		if a.AppID != "" {
			item.AppName = dir.Apps[a.AppID].Name
		}
		out = append(out, item)
	}
	webutil.Success(w, "ok", out)
}

// A cursor is "<unix seconds>_<event id>" of the last event of a page.
func formatCursor(ev alert.Event) string {
	return fmt.Sprintf("%d_%s", ev.CreatedAt.Unix(), ev.ID)
}

func parseCursor(s string) (time.Time, string, error) {
	secs, id, found := strings.Cut(s, "_")
	n, err := strconv.ParseInt(secs, 10, 64)
	if !found || err != nil || id == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor %q", s)
	}
	return time.Unix(n, 0).UTC(), id, nil
}
```

- [ ] **Step 3: Add the five routes**

In `internal/app/web/handler/alerts/routes.go`:
- add `target := []int{http.StatusBadRequest, http.StatusForbidden}` after `bad := …`;
- append these entries to the returned slice, after `delete-rule`:

```go
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-overrides", Handler: h.GetOverrides, Auth: api.User,
			Summary: "A server's or app's rule overrides", Tag: "alerts",
			Query: TargetQuery{}, Response: []alert.Override{}, Errors: target,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/save-override", Handler: h.SaveOverride, Auth: api.User,
			Summary: "Override a rule for one server or app", Tag: "alerts",
			Description: "Null fields inherit the rule's; `channel_ids` null inherits, [] silences.",
			Request:     alert.Override{}, Response: alert.Override{}, Errors: target,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/delete-override", Handler: h.DeleteOverride, Auth: api.User,
			Summary: "Remove a rule override", Tag: "alerts",
			Request: DeleteOverrideRequest{}, Errors: target,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-events", Handler: h.GetEvents, Auth: api.User,
			Summary: "Alert history, newest first", Tag: "alerts",
			Query: EventsQuery{}, Response: EventsResponse{}, Errors: target,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-active", Handler: h.GetActive, Auth: api.User,
			Summary: "Firing alerts", Tag: "alerts",
			Query: ActiveQuery{}, Response: []ActiveAlert{}, Errors: target,
		},
```

- [ ] **Step 4: Count firing alerts on the dashboard overview**

Apply to the monitoring handler, then add `Alerts: d.AlertRepository,` to the `monitoring.Deps{…}` literal in `internal/app/web/routes.go`:

```diff
diff --git a/internal/app/web/handler/monitoring/handler.go b/internal/app/web/handler/monitoring/handler.go
index 625ad1a..5c6dbad 100644
--- a/internal/app/web/handler/monitoring/handler.go
+++ b/internal/app/web/handler/monitoring/handler.go
@@ -4,6 +4,7 @@
 package monitoring
 
 import (
+	"context"
 	"time"
 
 	"winterflow/internal/domain/metrics"
@@ -12,12 +13,18 @@ import (
 	"winterflow/pkg/logger"
 )
 
+// FiringCounter counts firing alerts per server (port.AlertRepository).
+type FiringCounter interface {
+	CountFiring(ctx context.Context) (map[string]int, error)
+}
+
 // Handler serves /api/v1/metrics/*.
 type Handler struct {
 	servers  port.ServerRepository
 	repo     port.MetricRepository
 	latest   *metrics.LatestStore
 	status   *status.Cache
+	alerts   FiringCounter
 	log      *logger.Logger
 	disabled bool
 	now      func() time.Time
@@ -33,6 +40,8 @@ type Deps struct {
 	// Disabled mirrors METRICS_ENABLED=false: the API stores nothing then,
 	// so the routes answer 200 with empty series and an empty overview.
 	Disabled bool
+	// Alerts feeds the overview's firing-alert badges; nil counts none.
+	Alerts FiringCounter
 }
 
 // NewHandler builds the handler.
@@ -42,6 +51,7 @@ func NewHandler(d *Deps) *Handler {
 		repo:     d.Metrics,
 		latest:   d.Latest,
 		status:   d.StatusCache,
+		alerts:   d.Alerts,
 		log:      d.Logger,
 		disabled: d.Disabled,
 		now:      time.Now,
diff --git a/internal/app/web/handler/monitoring/overview.go b/internal/app/web/handler/monitoring/overview.go
index 6597b9a..b9d1b43 100644
--- a/internal/app/web/handler/monitoring/overview.go
+++ b/internal/app/web/handler/monitoring/overview.go
@@ -29,6 +29,8 @@ type ServerOverview struct {
 	Latest    *metrics.Latest `json:"latest"`
 	Sparkline []metrics.Point `json:"sparkline"`
 	Apps      AppCounts       `json:"apps"`
+	// AlertsFiring counts the server's (and its apps') firing alerts.
+	AlertsFiring int `json:"alerts_firing"`
 }
 
 // OverviewResponse is the data of GET /api/v1/metrics/overview.
@@ -66,13 +68,20 @@ func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
 		return
 	}
 	now := h.now()
+	firing := map[string]int{}
+	if h.alerts != nil {
+		if firing, err = h.alerts.CountFiring(r.Context()); err != nil {
+			h.log.Error("metrics: overview alert counts", "error", err)
+		}
+	}
 	out := make([]ServerOverview, 0, len(servers))
 	for _, s := range servers {
 		o := ServerOverview{
-			ServerID:  s.ID,
-			Liveness:  h.status.ServerLiveness(s.ID, now),
-			Sparkline: []metrics.Point{},
-			Apps:      countApps(h.status.AppStatuses(s.ID, now)),
+			ServerID:     s.ID,
+			Liveness:     h.status.ServerLiveness(s.ID, now),
+			Sparkline:    []metrics.Point{},
+			Apps:         countApps(h.status.AppStatuses(s.ID, now)),
+			AlertsFiring: firing[s.ID],
 		}
 		if h.disabled {
 			// Nothing is stored: keep liveness and app counts, skip the
```

- [ ] **Step 5: Regenerate and run the suite on both engines**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp make openapi
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web -run TestRoutesGolden -update
GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./... 
WF_CONTRACT_GUARD=require GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web/...
GOTMPDIR=$HOME/.cache/wf-gotmp make test-pg
```

Expected: every package `ok` on SQLite and on Postgres 16. `TestAlertFiresFromMetrics` shows the whole pipeline working: a `server.metrics` event, the evaluator, a CAS state with its event, the firing list, and the overview badge.

- [ ] **Step 6: Commit**

```bash
git add internal/app/web api/openapi.yaml
git commit -m "feat(alerts): API for overrides, history and firing alerts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 10: Web: alerting model, Shoutrrr URL builders and API hooks

**Files:**
- Create: `web/src/lib/alerts.ts`, `web/src/hooks/use-alerts.ts`, `web/src/test/fake-api.ts`
- Test: `web/src/lib/alerts.test.ts`

**Interfaces:**
- Consumes:
  - the JSON shapes of Tasks 8 and 9;
  - `usePolled` from `@/hooks/use-metrics`;
  - `NotificationsContext` from `@/context/notifications-context-base`;
  - `readApiError` from `@/lib/api`.
- Produces:
  - `@/lib/alerts`:
    - types `TargetKind`, `AlertMetric`, `AlertUnit`, `MetricInfo`, `ALERT_METRICS`, `metricInfo(kind, metric)`, `hasThreshold(metric)`;
    - API types `Channel`, `Rule`, `Override`, `Delivery`, `AlertEvent`, `ActiveAlert`, `AlertNotice`, and `ALERT_NOTIFICATION = "alert"`;
    - wording: `formatAlertValue`, `conditionText`, `ruleSummary`, `routingText`, `Routing`, `routingOf`, `effective`, `sinceText`, `activeText`, `alertTargetPath`;
    - services: `ServiceId`, `FieldSpec`, `ServiceSpec`, `SERVICES` (telegram, discord, slack, ntfy, gotify, pushover, email, webhook, raw), `serviceSpec`, `UI_SERVICE_FIELD = "ui_service"`, `channelService`, `buildChannel(id, values) → {url, fields}`, `channelSummary`.
  - `@/hooks/use-alerts`:
    - `alertsGet<T>(path, params?)` and `alertsPost<T>(path, body)` for `/api/v1/alerts/<path>`;
    - `useActiveAlerts(serverId?, appId?)`: polled every 60 s, refreshed on SSE `alert`, polling only outside a `NotificationsProvider`. `data` is always an array.
  - `@/test/fake-api`: `fakeAPI(data)` stubs `fetch` by path, where a value may be a function of the body and an `Error` answers 400. It returns `{calls, fetchMock, posted(path)}`.

Every service URL format was checked against Shoutrrr v0.18 (`docs/services/*`, and `CreateSender` accepting the built URL):
- `telegram://<token>@telegram?chats=…`
- `discord://<token>@<webhookid>`
- `slack://hook:<T>-<B>-<X>@webhook`
- `ntfy://[user:pass@]host/topic[?scheme=http]`
- `gotify://host[/path]/<token>[?disabletls=yes]`
- `pushover://shoutrrr:<token>@<user>/?devices=…`
- `smtp://[user:pass@]host:port/?fromaddress=…&toaddresses=…[&auth=None]`
- `generic://host/path?template=json[&disabletls=yes]`

- [ ] **Step 1: Write the failing test**

`web/src/lib/alerts.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import {
  ALERT_METRICS,
  buildChannel,
  channelService,
  channelSummary,
  effective,
  formatAlertValue,
  routingText,
  ruleSummary,
  SERVICES,
  type Channel,
  type Rule,
} from "@/lib/alerts";

const rule: Rule = {
  id: "r1",
  target_kind: "server",
  metric: "cpu",
  threshold: 90,
  duration_minutes: 10,
  channel_ids: null,
  enabled: true,
  created_at: "2026-10-11T12:00:00Z",
};

describe("alert wording", () => {
  it("mirrors the Go catalog", () => {
    expect(ALERT_METRICS).toHaveLength(13);
  });

  it("summarises rules", () => {
    expect(ruleSummary(rule)).toBe("CPU above 90% for 10 min");
    expect(ruleSummary({ ...rule, metric: "down", duration_minutes: 5 })).toBe("Server down for 5 min");
    expect(ruleSummary({ ...rule, target_kind: "app", metric: "disk", threshold: 50 })).toBe("Disk above 50.0 GB for 10 min");
    expect(ruleSummary({ ...rule, metric: "load5", threshold: 4 })).toBe("Load 5m above 4.00 for 10 min");
  });

  it("formats values like the notifications", () => {
    expect(formatAlertValue("%", 93.4)).toBe("93%");
    expect(formatAlertValue("MB/s", 12.44)).toBe("12.4 MB/s");
    expect(formatAlertValue("°C", 70.6)).toBe("71 °C");
  });

  it("words routing", () => {
    const channels = [{ id: "c1", name: "Ops" }] as Channel[];
    expect(routingText(null, channels)).toBe("Default channels");
    expect(routingText([], channels)).toBe("History only");
    expect(routingText(["c1", "gone"], channels)).toBe("Ops, deleted channel");
  });

  it("merges overrides like the API", () => {
    expect(effective(rule, undefined)).toEqual({ enabled: true, threshold: 90, duration_minutes: 10, channel_ids: null });
    const o = { rule_id: "r1", server_id: "s", app_id: "", disabled: false, threshold: 95, duration_minutes: null, channel_ids: [] };
    expect(effective(rule, o)).toEqual({ enabled: true, threshold: 95, duration_minutes: 10, channel_ids: [] });
    expect(effective(rule, { ...o, disabled: true }).enabled).toBe(false);
  });
});

describe("channel URL builders", () => {
  it("builds every service's Shoutrrr URL", () => {
    const cases: [string, Record<string, string>, string][] = [
      ["telegram", { token: "123456:ABC-def_9", chats: "-100123, @ops " }, "telegram://123456:ABC-def_9@telegram?chats=-100123,@ops"],
      ["discord", { webhook: "https://discord.com/api/webhooks/42/tok-EN_x" }, "discord://tok-EN_x@42"],
      ["slack", { webhook: "https://hooks.slack.com/services/T1/B2/xyz" }, "slack://hook:T1-B2-xyz@webhook"],
      ["ntfy", { server: "ntfy.sh", topic: "my alerts" }, "ntfy://ntfy.sh/my%20alerts"],
      ["ntfy", { server: "http://ntfy.lan:8080/", topic: "t", username: "me", password: "p@ss" }, "ntfy://me:p%40ss@ntfy.lan:8080/t?scheme=http"],
      ["gotify", { server: "https://gotify.example.com", token: "AbC.123" }, "gotify://gotify.example.com/AbC.123"],
      ["pushover", { token: "tok", user: "usr", devices: "phone, tablet" }, "pushover://shoutrrr:tok@usr/?devices=phone,tablet"],
      [
        "email",
        { host: "smtp.example.com", port: "587", username: "me", password: "pw", from: "wf@example.com", to: "a@example.com, b@example.com" },
        "smtp://me:pw@smtp.example.com:587/?fromaddress=wf%40example.com&toaddresses=a%40example.com%2Cb%40example.com",
      ],
      ["email", { host: "mail.lan", port: "25", from: "wf@lan", to: "ops@lan" }, "smtp://mail.lan:25/?fromaddress=wf%40lan&toaddresses=ops%40lan&auth=None"],
      ["webhook", { url: "https://hooks.example.com/a/b?k=v" }, "generic://hooks.example.com/a/b?template=json&k=v"],
      ["webhook", { url: "http://10.0.0.2:9000/hook" }, "generic://10.0.0.2:9000/hook?template=json&disabletls=yes"],
      ["raw", { url: "matrix://u:p@host/?rooms=!r" }, "matrix://u:p@host/?rooms=!r"],
    ];
    for (const [id, values, want] of cases) {
      expect(buildChannel(id as never, values).url, id).toBe(want);
    }
  });

  it("keeps only non-secret fields, tagged with the form", () => {
    const { fields } = buildChannel("ntfy", { server: "ntfy.sh", topic: "secret-topic", username: "me", password: "pw" });
    expect(fields).toEqual({ ui_service: "ntfy", server: "ntfy.sh", username: "me" });
  });

  it("explains bad input", () => {
    expect(() => buildChannel("telegram", { token: "nope", chats: "1" })).toThrow(/123456789:ABC/);
    expect(() => buildChannel("telegram", { token: "1:a" })).toThrow("Chats is required");
    expect(() => buildChannel("discord", { webhook: "https://example.com/x" })).toThrow(/Discord webhook/);
    expect(() => buildChannel("email", { host: "h", port: "x", from: "a", to: "b" })).toThrow(/Port/);
    expect(() => buildChannel("webhook", { url: "ftp://x" })).toThrow(/https/);
    expect(() => buildChannel("ntfy", { server: "ntfy.sh" })).toThrow("Topic is required");
  });

  it("recognises a stored channel's form", () => {
    expect(channelService({ service: "generic", fields: {} })).toBe("webhook");
    expect(channelService({ service: "smtp", fields: null })).toBe("email");
    expect(channelService({ service: "matrix", fields: {} })).toBe("raw");
    expect(channelService({ service: "generic", fields: { ui_service: "raw" } })).toBe("raw");
    expect(channelSummary({ service: "ntfy", fields: { ui_service: "ntfy", server: "ntfy.sh" } })).toBe("ntfy · ntfy.sh");
    expect(SERVICES.map((s) => s.id)).toEqual(["telegram", "discord", "slack", "ntfy", "gotify", "pushover", "email", "webhook", "raw"]);
  });
});
```

Run: `pnpm --dir web exec vitest run src/lib/alerts.test.ts`
Expected: FAIL (`Cannot find module '@/lib/alerts'`).

- [ ] **Step 2: Write the model, the hooks and the fetch fake**

`web/src/lib/alerts.ts`:

```ts
// Alerting model and pure helpers: the metric catalog (mirrors
// internal/domain/alert/catalog.go), API types, rule and value wording,
// and the notification services the channel form turns into Shoutrrr URLs.

export type TargetKind = "server" | "app";

export type AlertMetric =
  | "down"
  | "cpu"
  | "memory"
  | "disk"
  | "load1"
  | "load5"
  | "load15"
  | "bandwidth"
  | "temperature";

export type AlertUnit = "" | "%" | "load" | "MB/s" | "°C" | "GB";

export type MetricInfo = {
  kind: TargetKind;
  metric: AlertMetric;
  label: string;
  unit: AlertUnit;
  // max bounds a threshold (0 = unbounded); thresholds are > 0.
  max: number;
  // suggested threshold for a new rule.
  suggested: number;
};

export const ALERT_METRICS: MetricInfo[] = [
  { kind: "server", metric: "down", label: "Down", unit: "", max: 0, suggested: 0 },
  { kind: "server", metric: "cpu", label: "CPU", unit: "%", max: 100, suggested: 90 },
  { kind: "server", metric: "memory", label: "Memory", unit: "%", max: 100, suggested: 90 },
  { kind: "server", metric: "disk", label: "Disk", unit: "%", max: 100, suggested: 90 },
  { kind: "server", metric: "load1", label: "Load 1m", unit: "load", max: 0, suggested: 4 },
  { kind: "server", metric: "load5", label: "Load 5m", unit: "load", max: 0, suggested: 4 },
  { kind: "server", metric: "load15", label: "Load 15m", unit: "load", max: 0, suggested: 4 },
  { kind: "server", metric: "bandwidth", label: "Bandwidth", unit: "MB/s", max: 0, suggested: 50 },
  { kind: "server", metric: "temperature", label: "Temperature", unit: "°C", max: 150, suggested: 80 },
  { kind: "app", metric: "down", label: "Down", unit: "", max: 0, suggested: 0 },
  { kind: "app", metric: "cpu", label: "CPU", unit: "%", max: 100, suggested: 80 },
  { kind: "app", metric: "memory", label: "Memory", unit: "%", max: 100, suggested: 90 },
  { kind: "app", metric: "disk", label: "Disk", unit: "GB", max: 0, suggested: 50 },
];

export function metricInfo(kind: TargetKind, metric: AlertMetric): MetricInfo | undefined {
  return ALERT_METRICS.find((m) => m.kind === kind && m.metric === metric);
}

export function hasThreshold(metric: AlertMetric): boolean {
  return metric !== "down";
}

// API types (internal/domain/alert, handler/alerts).

export type Channel = {
  id: string;
  name: string;
  service: string;
  fields: Record<string, string> | null;
  is_default: boolean;
  enabled: boolean;
  last_error: string;
  last_sent_at: string | null;
  created_at: string;
};

// channel_ids: null = the org's default channels, [] = none (History only).
export type Rule = {
  id: string;
  target_kind: TargetKind;
  metric: AlertMetric;
  threshold: number;
  duration_minutes: number;
  channel_ids: string[] | null;
  enabled: boolean;
  created_at: string;
};

// Null fields inherit the rule's; channel_ids null inherits, [] silences.
export type Override = {
  rule_id: string;
  server_id: string;
  app_id: string;
  disabled: boolean;
  threshold: number | null;
  duration_minutes: number | null;
  channel_ids: string[] | null;
};

export type Delivery = { channel_id: string; channel_name: string; ok: boolean; error: string };

export type AlertEvent = {
  id: string;
  rule_id: string;
  server_id: string;
  app_id: string;
  server_name: string;
  app_name: string;
  target_kind: TargetKind;
  metric: AlertMetric;
  kind: "fired" | "resolved";
  value: number;
  threshold: number;
  title: string;
  message: string;
  deliveries: Delivery[] | null;
  created_at: string;
};

export type ActiveAlert = {
  rule_id: string;
  target_kind: TargetKind;
  metric: AlertMetric;
  server_id: string;
  app_id: string;
  since: string;
  value: number;
  threshold: number;
  server_name: string;
  app_name: string;
};

// AlertNotice is the payload of an SSE "alert" notification.
export type AlertNotice = {
  kind: "fired" | "resolved";
  rule_id: string;
  target_kind: TargetKind;
  metric: AlertMetric;
  server_id: string;
  app_id: string;
  value: number;
  threshold: number;
  title: string;
};

export const ALERT_NOTIFICATION = "alert";

// formatAlertValue renders a value in its metric's unit, as the
// notifications do ("93%", "2.31", "12.4 MB/s", "71 °C", "14.2 GB").
export function formatAlertValue(unit: AlertUnit, v: number): string {
  switch (unit) {
    case "%":
      return `${Math.round(v)}%`;
    case "load":
      return v.toFixed(2);
    case "MB/s":
      return `${v.toFixed(1)} MB/s`;
    case "°C":
      return `${Math.round(v)} °C`;
    case "GB":
      return `${v.toFixed(1)} GB`;
    default:
      return String(v);
  }
}

// conditionText words a rule's condition: "above 90% for 10 min" /
// "down for 5 min".
export function conditionText(metric: AlertMetric, unit: AlertUnit, threshold: number, minutes: number): string {
  const span = `for ${minutes} min`;
  return hasThreshold(metric) ? `above ${formatAlertValue(unit, threshold)} ${span}` : `down ${span}`;
}

// ruleSummary is a one-line description: "CPU above 90% for 10 min".
export function ruleSummary(rule: Pick<Rule, "target_kind" | "metric" | "threshold" | "duration_minutes">): string {
  const info = metricInfo(rule.target_kind, rule.metric);
  if (!info) return `${rule.metric} (unknown)`;
  if (!hasThreshold(rule.metric)) {
    return `${rule.target_kind === "app" ? "App" : "Server"} ${conditionText(rule.metric, info.unit, 0, rule.duration_minutes)}`;
  }
  return `${info.label} ${conditionText(rule.metric, info.unit, rule.threshold, rule.duration_minutes)}`;
}

// routingText words where a rule's notifications go.
export function routingText(ids: string[] | null, channels: Channel[]): string {
  if (ids === null) return "Default channels";
  if (ids.length === 0) return "History only";
  const names = ids.map((id) => channels.find((c) => c.id === id)?.name ?? "deleted channel");
  return names.join(", ");
}

export type Routing = "default" | "specific" | "none";

// routingOf names how a channel list routes: null = defaults, [] = none.
export function routingOf(ids: string[] | null): Routing {
  if (ids === null) return "default";
  return ids.length === 0 ? "none" : "specific";
}

// effective merges a rule with a target's override, as the API does.
export function effective(rule: Rule, o: Override | undefined) {
  return {
    enabled: rule.enabled && !o?.disabled,
    threshold: o?.threshold ?? rule.threshold,
    duration_minutes: o?.duration_minutes ?? rule.duration_minutes,
    channel_ids: o && o.channel_ids !== null ? o.channel_ids : rule.channel_ids,
  };
}

// Notification services: each form builds a Shoutrrr URL. Secret fields
// never leave the browser except inside the URL (stored encrypted); the
// other fields are kept with the channel to show and prefill the form.

export type ServiceId =
  | "telegram"
  | "discord"
  | "slack"
  | "ntfy"
  | "gotify"
  | "pushover"
  | "email"
  | "webhook"
  | "raw";

export type FieldSpec = {
  key: string;
  label: string;
  secret?: boolean;
  optional?: boolean;
  placeholder?: string;
  hint?: string;
  defaultValue?: string;
};

export type ServiceSpec = {
  id: ServiceId;
  label: string;
  fields: FieldSpec[];
  // build turns the form values into a Shoutrrr URL or throws an Error
  // whose message is shown under the form.
  build: (f: Record<string, string>) => string;
};

const enc = encodeURIComponent;

function req(f: Record<string, string>, key: string, label: string): string {
  const v = (f[key] ?? "").trim();
  if (!v) throw new Error(`${label} is required`);
  return v;
}

function opt(f: Record<string, string>, key: string): string {
  return (f[key] ?? "").trim();
}

// splitHost turns "https://host:port/path" or "host/path" into the
// scheme-less host+path and whether plain HTTP was asked for.
function splitHost(raw: string): { hostPath: string; insecure: boolean } {
  const insecure = /^http:\/\//i.test(raw);
  const hostPath = raw.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
  if (!hostPath || /\s/.test(hostPath)) throw new Error("Server must be a host name, like ntfy.example.com");
  return { hostPath, insecure };
}

function list(v: string): string {
  return v
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .join(",");
}

export const SERVICES: ServiceSpec[] = [
  {
    id: "telegram",
    label: "Telegram",
    fields: [
      { key: "token", label: "Bot token", secret: true, placeholder: "123456789:ABC-DEF…", hint: "From @BotFather." },
      { key: "chats", label: "Chats", placeholder: "-1001234567890, @mychannel", hint: "Chat IDs or @channel names, comma-separated." },
    ],
    build: (f) => {
      const token = req(f, "token", "Bot token");
      if (!/^\d+:[\w-]+$/.test(token)) throw new Error("Bot token looks like 123456789:ABC-DEF…");
      return `telegram://${token}@telegram?chats=${list(req(f, "chats", "Chats"))}`;
    },
  },
  {
    id: "discord",
    label: "Discord",
    fields: [
      {
        key: "webhook",
        label: "Webhook URL",
        secret: true,
        placeholder: "https://discord.com/api/webhooks/…",
        hint: "Channel settings → Integrations → Webhooks → Copy Webhook URL.",
      },
    ],
    build: (f) => {
      const m = req(f, "webhook", "Webhook URL").match(/discord(?:app)?\.com\/api\/webhooks\/(\d+)\/([\w-]+)/);
      if (!m) throw new Error("Paste the whole Discord webhook URL");
      return `discord://${m[2]}@${m[1]}`;
    },
  },
  {
    id: "slack",
    label: "Slack",
    fields: [
      {
        key: "webhook",
        label: "Webhook URL",
        secret: true,
        placeholder: "https://hooks.slack.com/services/T…/B…/…",
        hint: "An Incoming Webhook of your Slack app.",
      },
    ],
    build: (f) => {
      const m = req(f, "webhook", "Webhook URL").match(/hooks\.slack\.com\/services\/([^/]+)\/([^/]+)\/([^/?#]+)/);
      if (!m) throw new Error("Paste the whole Slack webhook URL");
      return `slack://hook:${m[1]}-${m[2]}-${m[3]}@webhook`;
    },
  },
  {
    id: "ntfy",
    label: "ntfy",
    fields: [
      { key: "server", label: "Server", defaultValue: "ntfy.sh", placeholder: "ntfy.sh" },
      { key: "topic", label: "Topic", secret: true, placeholder: "my-alerts-7f3k", hint: "Anyone who knows a public topic can read it." },
      { key: "username", label: "Username", optional: true },
      { key: "password", label: "Password or token", secret: true, optional: true },
    ],
    build: (f) => {
      const { hostPath, insecure } = splitHost(req(f, "server", "Server"));
      const topic = req(f, "topic", "Topic");
      const user = opt(f, "username");
      const pass = opt(f, "password");
      const auth = user || pass ? `${enc(user)}:${enc(pass)}@` : "";
      return `ntfy://${auth}${hostPath}/${enc(topic)}${insecure ? "?scheme=http" : ""}`;
    },
  },
  {
    id: "gotify",
    label: "Gotify",
    fields: [
      { key: "server", label: "Server", placeholder: "gotify.example.com" },
      { key: "token", label: "App token", secret: true, placeholder: "AzyoeNS.D4iJLVa" },
    ],
    build: (f) => {
      const { hostPath, insecure } = splitHost(req(f, "server", "Server"));
      return `gotify://${hostPath}/${enc(req(f, "token", "App token"))}${insecure ? "?disabletls=yes" : ""}`;
    },
  },
  {
    id: "pushover",
    label: "Pushover",
    fields: [
      { key: "token", label: "API token", secret: true },
      { key: "user", label: "User key", secret: true },
      { key: "devices", label: "Devices", optional: true, hint: "Comma-separated; empty sends to all." },
    ],
    build: (f) => {
      const devices = list(opt(f, "devices"));
      return `pushover://shoutrrr:${enc(req(f, "token", "API token"))}@${enc(req(f, "user", "User key"))}/${devices ? `?devices=${devices}` : ""}`;
    },
  },
  {
    id: "email",
    label: "Email (SMTP)",
    fields: [
      { key: "host", label: "SMTP server", placeholder: "smtp.example.com" },
      { key: "port", label: "Port", defaultValue: "587" },
      { key: "username", label: "Username", optional: true },
      { key: "password", label: "Password", secret: true, optional: true },
      { key: "from", label: "From", placeholder: "winterflow@example.com" },
      { key: "to", label: "To", placeholder: "ops@example.com", hint: "Comma-separated." },
    ],
    build: (f) => {
      const host = req(f, "host", "SMTP server");
      const port = req(f, "port", "Port");
      if (!/^\d{1,5}$/.test(port)) throw new Error("Port must be a number");
      const user = opt(f, "username");
      const pass = opt(f, "password");
      const auth = user ? `${enc(user)}:${enc(pass)}@` : "";
      const query = `fromaddress=${enc(req(f, "from", "From"))}&toaddresses=${enc(list(req(f, "to", "To")))}`;
      return `smtp://${auth}${host}:${port}/?${query}${user ? "" : "&auth=None"}`;
    },
  },
  {
    id: "webhook",
    label: "Webhook",
    fields: [
      {
        key: "url",
        label: "URL",
        secret: true,
        placeholder: "https://example.com/hooks/winterflow",
        hint: "Receives a JSON POST with title and message.",
      },
    ],
    build: (f) => {
      const raw = req(f, "url", "URL");
      let u: URL;
      try {
        u = new URL(raw);
      } catch {
        throw new Error("URL must start with https:// or http://");
      }
      if (u.protocol !== "https:" && u.protocol !== "http:") throw new Error("URL must start with https:// or http://");
      const extra = u.search ? `&${u.search.slice(1)}` : "";
      return `generic://${u.host}${u.pathname}?template=json${u.protocol === "http:" ? "&disabletls=yes" : ""}${extra}`;
    },
  },
  {
    id: "raw",
    label: "Shoutrrr URL",
    fields: [
      {
        key: "url",
        label: "URL",
        secret: true,
        placeholder: "matrix://user:password@host/?rooms=!room",
        hint: "Any Shoutrrr service URL (containrrr.dev/shoutrrr).",
      },
    ],
    build: (f) => req(f, "url", "URL"),
  },
];

export function serviceSpec(id: ServiceId): ServiceSpec {
  return SERVICES.find((s) => s.id === id) ?? SERVICES[SERVICES.length - 1];
}

// UI_SERVICE_FIELD names the stored field holding the form's service.
export const UI_SERVICE_FIELD = "ui_service";

// channelService is the form a stored channel was made with: its stored
// ui_service, else a guess from the URL scheme.
export function channelService(ch: Pick<Channel, "service" | "fields">): ServiceId {
  const stored = ch.fields?.[UI_SERVICE_FIELD];
  if (stored && SERVICES.some((s) => s.id === stored)) return stored as ServiceId;
  const byScheme: Record<string, ServiceId> = {
    telegram: "telegram",
    discord: "discord",
    slack: "slack",
    ntfy: "ntfy",
    gotify: "gotify",
    pushover: "pushover",
    smtp: "email",
    generic: "webhook",
  };
  return byScheme[ch.service] ?? "raw";
}

// buildChannel turns a service form into the URL and the non-secret
// fields to store with it.
export function buildChannel(
  id: ServiceId,
  values: Record<string, string>,
): { url: string; fields: Record<string, string> } {
  const spec = serviceSpec(id);
  const url = spec.build(values);
  const fields: Record<string, string> = { [UI_SERVICE_FIELD]: id };
  for (const f of spec.fields) {
    const v = (values[f.key] ?? "").trim();
    if (!f.secret && v) fields[f.key] = v;
  }
  return { url, fields };
}

// channelSummary is the public part of a channel's destination.
export function channelSummary(ch: Pick<Channel, "service" | "fields">): string {
  const id = channelService(ch);
  const spec = serviceSpec(id);
  const shown = spec.fields
    .filter((f) => !f.secret && ch.fields?.[f.key])
    .map((f) => ch.fields?.[f.key] as string);
  return [spec.label, ...shown].join(" · ");
}

// sinceText is how long ago an alert started: "just now", "12 min",
// "3 h", "2 d".
export function sinceText(since: string, now: number): string {
  const mins = Math.max(0, Math.floor((now - Date.parse(since)) / 60_000));
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours} h`;
  return `${Math.floor(hours / 24)} d`;
}

// activeText describes a firing alert: "CPU 93% > 90%" / "Down".
export function activeText(a: Pick<ActiveAlert, "target_kind" | "metric" | "value" | "threshold">): string {
  const info = metricInfo(a.target_kind, a.metric);
  if (!info || !hasThreshold(a.metric)) return "Down";
  return `${info.label} ${formatAlertValue(info.unit, a.value)} > ${formatAlertValue(info.unit, a.threshold)}`;
}

// activePath links a firing alert to its server's Monitoring page or its
// app's Metrics tab.
export function alertTargetPath(serverId: string, appId: string): string {
  const base = `/server/${encodeURIComponent(serverId)}`;
  return appId ? `${base}/app/${encodeURIComponent(appId)}?tab=metrics` : `${base}/monitoring`;
}
```

`web/src/hooks/use-alerts.ts`:

```ts
import { useContext, useEffect } from "react";

import { apiBaseUrl } from "@/config";
import { NotificationsContext } from "@/context/notifications-context-base";
import { usePolled } from "@/hooks/use-metrics";
import { readApiError } from "@/lib/api";
import { ALERT_NOTIFICATION, type ActiveAlert } from "@/lib/alerts";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

// alertsGet and alertsPost call /api/v1/alerts/* and unwrap the envelope;
// a failed call throws the server's message.
export async function alertsGet<T>(path: string, params?: Record<string, string>): Promise<T> {
  const qs = params ? `?${new URLSearchParams(params).toString()}` : "";
  const res = await fetch(`${base}/api/v1/alerts/${path}${qs}`, { credentials: "include" });
  if (!res.ok) throw await readApiError(res);
  return ((await res.json()) as { data: T }).data;
}

export async function alertsPost<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${base}/api/v1/alerts/${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw await readApiError(res);
  return ((await res.json()) as { data: T }).data;
}

function normalizeActive(list: ActiveAlert[] | null): ActiveAlert[] {
  return list ?? [];
}

// useActiveAlerts polls the firing alerts (all of the org's, or one
// server's or app's) every minute while visible, and refreshes at once on
// an SSE "alert" transition. Outside a NotificationsProvider it only polls.
export function useActiveAlerts(serverId?: string | null, appId?: string | null) {
  const params = new URLSearchParams();
  if (serverId) params.set("server_id", serverId);
  if (serverId && appId) params.set("app_id", appId);
  const qs = params.toString();
  const state = usePolled<ActiveAlert[] | null>(`/api/v1/alerts/get-active${qs ? `?${qs}` : ""}`, normalizeActive);
  const subscribe = useContext(NotificationsContext)?.subscribe;
  const { refresh } = state;
  useEffect(() => {
    if (!subscribe) return;
    return subscribe((n) => {
      if (n.type === ALERT_NOTIFICATION) void refresh();
    });
  }, [subscribe, refresh]);
  return { ...state, data: state.data ?? [] };
}
```

`web/src/test/fake-api.ts`:

```ts
import { vi } from "vitest";

export type Call = { path: string; query: string; body: unknown };

// fakeAPI answers fetches by path from data (a value, or a function of the
// request body) and records every call. Unknown paths answer 404.
export function fakeAPI(data: Record<string, unknown>) {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const u = new URL(url, "http://x");
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ path: u.pathname, query: u.search, body });
    if (!(u.pathname in data)) {
      return new Response(JSON.stringify({ success: false, message: "no route" }), { status: 404 });
    }
    const entry = data[u.pathname];
    const value = typeof entry === "function" ? (entry as (b: unknown) => unknown)(body) : entry;
    if (value instanceof Error) {
      return new Response(JSON.stringify({ success: false, message: value.message }), { status: 400 });
    }
    return new Response(JSON.stringify({ success: true, message: "ok", data: value }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, fetchMock, posted: (path: string) => calls.filter((c) => c.path === path).map((c) => c.body) };
}
```

- [ ] **Step 3: Run the tests, typecheck and lint**

Run: `pnpm --dir web exec vitest run src/lib/alerts.test.ts && pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: 9 tests pass; no type or lint errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/lib/alerts.ts web/src/lib/alerts.test.ts web/src/hooks/use-alerts.ts web/src/test/fake-api.ts
git commit -m "feat(web): alerting model, Shoutrrr URL builders and API hooks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 11: Web: Org → Alerts page with the Channels tab

**Files:**
- Create: `web/src/components/alerts/channel-dialog.tsx`, `web/src/components/alerts/channels-tab.tsx`, `web/src/pages/org-alerts.tsx`
- Modify: `web/src/main.tsx` (route `/org/alerts`), `web/src/components/nav-user.tsx` (Alerts item)
- Test: `web/src/components/alerts/channel-dialog.test.tsx`, `web/src/components/alerts/channels-tab.test.tsx`, `web/src/components/nav-user.test.tsx`

**Interfaces:**
- Consumes: Task 10 (`buildChannel`, `channelService`, `channelSummary`, `SERVICES`, `alertsGet/Post`, `fakeAPI`).
- Produces:
  - `ChannelDialog({open, onOpenChange, channel?, onSaved})`;
  - `ChannelsTab()`;
  - `OrgAlertsPage` (default export), whose tab lives in `?tab=`. Task 12 adds History and Rules to it.

UX rules:
- The service picker is a grid of radio buttons.
- Secret fields are password inputs that are never prefilled.
- Editing shows the stored public summary and a **Change destination** button. Saving without changing it posts `url: ""`, which keeps the stored URL.
- **Send test** tests the draft URL, or the saved channel by id while the destination is unchanged.
- The account menu gets **Alerts** right after **Members**, following the spec's "linked next to Members".

- [ ] **Step 1: Write the failing tests**

`web/src/components/alerts/channel-dialog.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ChannelDialog } from "@/components/alerts/channel-dialog";
import type { Channel } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const stored: Channel = {
  id: "c1",
  name: "Ops",
  service: "ntfy",
  fields: { ui_service: "ntfy", server: "ntfy.example.com" },
  is_default: true,
  enabled: true,
  last_error: "",
  last_sent_at: null,
  created_at: "2026-10-11T12:00:00Z",
};

describe("ChannelDialog", () => {
  it("builds the Shoutrrr URL from the form and keeps secrets out of fields", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-channel": { id: "c9" } });
    const onSaved = vi.fn();
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} onSaved={onSaved} />);

    await user.type(screen.getByLabelText("Name"), "Phone");
    await user.click(screen.getByRole("radio", { name: "ntfy" }));
    await user.type(screen.getByLabelText("Topic"), "wf-alerts");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(api.posted("/api/v1/alerts/save-channel")).toEqual([
      {
        id: "",
        name: "Phone",
        url: "ntfy://ntfy.sh/wf-alerts",
        fields: { ui_service: "ntfy", server: "ntfy.sh" },
        is_default: true,
        enabled: true,
      },
    ]);
    expect(onSaved).toHaveBeenCalled();
  });

  it("explains an invalid form instead of saving", async () => {
    const api = fakeAPI({});
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} onSaved={() => {}} />);
    await user.type(screen.getByLabelText("Name"), "Bot");
    await user.type(screen.getByLabelText("Bot token"), "not-a-token");
    await user.type(screen.getByLabelText("Chats"), "1");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("123456789:ABC");
    expect(api.calls).toHaveLength(0);
  });

  it("tests a draft by URL and shows the outcome", async () => {
    const api = fakeAPI({ "/api/v1/alerts/test-channel": { ok: false, error: "403 Forbidden" } });
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} onSaved={() => {}} />);
    await user.click(screen.getByRole("radio", { name: "Webhook" }));
    await user.type(screen.getByLabelText("URL"), "https://hooks.example.com/x");
    await user.click(screen.getByRole("button", { name: /Send test/ }));
    expect(await screen.findByRole("status")).toHaveTextContent("Test failed: 403 Forbidden");
    expect(api.posted("/api/v1/alerts/test-channel")).toEqual([
      { url: "generic://hooks.example.com/x?template=json" },
    ]);
  });

  it("edits name and flags without resending the destination", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/save-channel": stored,
      "/api/v1/alerts/test-channel": { ok: true, error: "" },
    });
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} channel={stored} onSaved={() => {}} />);

    expect(screen.getByTestId("channel-destination")).toHaveTextContent("ntfy · ntfy.example.com");
    await user.click(screen.getByRole("button", { name: /Send test/ }));
    expect(api.posted("/api/v1/alerts/test-channel")).toEqual([{ id: "c1" }]);

    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "Ops 2");
    await user.click(screen.getByLabelText("Default channel"));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-channel")).toEqual([
      { id: "c1", name: "Ops 2", url: "", fields: {}, is_default: false, enabled: true },
    ]);
  });

  it("asks for the secrets again when the destination changes", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-channel": stored });
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} channel={stored} onSaved={() => {}} />);
    await user.click(screen.getByRole("button", { name: "Change destination" }));
    // The public server is prefilled; the secret topic is not.
    expect(screen.getByLabelText("Server")).toHaveValue("ntfy.example.com");
    expect(screen.getByLabelText("Topic")).toHaveValue("");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Topic is required");
    await user.type(screen.getByLabelText("Topic"), "t2");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-channel")[0]).toMatchObject({ url: "ntfy://ntfy.example.com/t2" });
  });
});
```

`web/src/components/alerts/channels-tab.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ChannelsTab } from "@/components/alerts/channels-tab";
import type { Channel } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const channels: Channel[] = [
  {
    id: "c1",
    name: "Ops",
    service: "telegram",
    fields: { ui_service: "telegram" },
    is_default: true,
    enabled: true,
    last_error: "",
    last_sent_at: "2026-10-11T12:00:00Z",
    created_at: "2026-10-11T11:00:00Z",
  },
  {
    id: "c2",
    name: "Hook",
    service: "generic",
    fields: {},
    is_default: false,
    enabled: false,
    last_error: "dial tcp: connection refused",
    last_sent_at: null,
    created_at: "2026-10-11T11:00:00Z",
  },
];

describe("ChannelsTab", () => {
  it("lists channels with their state and deletes after confirmation", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-channels": channels,
      "/api/v1/alerts/delete-channel": null,
      "/api/v1/alerts/test-channel": { ok: true, error: "" },
    });
    const user = userEvent.setup({ delay: null });
    render(<ChannelsTab />);

    const rows = await screen.findAllByTestId("channel-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText("Default")).toBeInTheDocument();
    expect(within(rows[0]).getByText("Telegram")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Disabled")).toBeInTheDocument();
    expect(within(rows[1]).getByText(/Last delivery failed: dial tcp/)).toBeInTheDocument();

    await user.click(within(rows[0]).getByRole("button", { name: /Test/ }));
    expect(api.posted("/api/v1/alerts/test-channel")).toEqual([{ id: "c1" }]);

    await user.click(screen.getByRole("button", { name: "Delete Hook" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(api.posted("/api/v1/alerts/delete-channel")).toEqual([{ id: "c2" }]);
  });

  it("explains the empty state", async () => {
    fakeAPI({ "/api/v1/alerts/get-channels": null });
    render(<ChannelsTab />);
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
  });
});
```

Apply to `web/src/components/nav-user.test.tsx`:

```diff
diff --git a/web/src/components/nav-user.test.tsx b/web/src/components/nav-user.test.tsx
index ccb9f18..4179cc0 100644
--- a/web/src/components/nav-user.test.tsx
+++ b/web/src/components/nav-user.test.tsx
@@ -35,6 +35,7 @@ function renderNav() {
               <Routes>
                 <Route path="/" element={<NavUser />} />
                 <Route path="/org/members" element={<p>members page</p>} />
+                <Route path="/org/alerts" element={<p>alerts page</p>} />
               </Routes>
             </ProfileContext.Provider>
           </UserContext.Provider>
@@ -54,4 +55,14 @@ describe("NavUser", () => {
 
     expect(await screen.findByText("members page")).toBeInTheDocument();
   });
+
+  it("offers Alerts next to Members", async () => {
+    const user = userEvent.setup();
+    renderNav();
+
+    await user.click(screen.getByRole("button", { name: /Ann/ }));
+    await user.click(await screen.findByRole("menuitem", { name: "Alerts" }));
+
+    expect(await screen.findByText("alerts page")).toBeInTheDocument();
+  });
 });
```

Run: `pnpm --dir web exec vitest run src/components/alerts src/components/nav-user.test.tsx`
Expected: FAIL (missing modules; no "Alerts" menu item).

- [ ] **Step 2: Write the dialog, the tab and the page**

`web/src/components/alerts/channel-dialog.tsx`:

```tsx
import { useState } from "react";
import { Send } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { alertsPost } from "@/hooks/use-alerts";
import {
  buildChannel,
  channelService,
  channelSummary,
  SERVICES,
  serviceSpec,
  type Channel,
  type ServiceId,
} from "@/lib/alerts";
import { cn } from "@/lib/utils";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // channel is the one being edited; undefined creates a new one.
  channel?: Channel;
  onSaved: () => void;
};

type TestResult = { ok: boolean; error: string };

// initialValues prefills a service form: stored non-secret fields when
// editing, field defaults otherwise. Secrets always start empty.
function initialValues(id: ServiceId, channel?: Channel): Record<string, string> {
  const values: Record<string, string> = {};
  for (const f of serviceSpec(id).fields) {
    values[f.key] = (!f.secret && channel?.fields?.[f.key]) || f.defaultValue || "";
  }
  return values;
}

// ChannelDialog creates or edits a notification channel. The service form
// builds the Shoutrrr URL in the browser; the URL is sent once and stored
// encrypted. Editing keeps the destination unless "Change destination" is
// used, which needs the secrets again.
export function ChannelDialog({ open, onOpenChange, channel, onSaved }: Props) {
  const editing = channel !== undefined;
  const [name, setName] = useState(channel?.name ?? "");
  const [service, setService] = useState<ServiceId>(channel ? channelService(channel) : "telegram");
  const [values, setValues] = useState<Record<string, string>>(() =>
    initialValues(channel ? channelService(channel) : "telegram", channel),
  );
  const [isDefault, setIsDefault] = useState(channel?.is_default ?? true);
  const [enabled, setEnabled] = useState(channel?.enabled ?? true);
  const [changing, setChanging] = useState(!editing);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [test, setTest] = useState<TestResult | null>(null);

  const spec = serviceSpec(service);

  const pickService = (id: ServiceId) => {
    setService(id);
    setValues(initialValues(id, channel && channelService(channel) === id ? channel : undefined));
    setFormError(null);
    setTest(null);
  };

  // destination builds the URL from the form, showing why it cannot.
  const destination = () => {
    try {
      const built = buildChannel(service, values);
      setFormError(null);
      return built;
    } catch (e) {
      setFormError(e instanceof Error ? e.message : "Invalid form");
      return null;
    }
  };

  const runTest = async () => {
    setTest(null);
    let body: Record<string, string>;
    if (changing) {
      const built = destination();
      if (!built) return;
      body = { url: built.url };
    } else {
      body = { id: channel!.id };
    }
    setBusy("test");
    try {
      setTest(await alertsPost<TestResult>("test-channel", body));
    } catch (e) {
      setTest({ ok: false, error: e instanceof Error ? e.message : "Test failed" });
    } finally {
      setBusy(null);
    }
  };

  const save = async () => {
    if (!name.trim()) {
      setFormError("Name is required");
      return;
    }
    let url = "";
    let fields: Record<string, string> = {};
    if (changing) {
      const built = destination();
      if (!built) return;
      ({ url, fields } = built);
    }
    setBusy("save");
    try {
      await alertsPost("save-channel", {
        id: channel?.id ?? "",
        name: name.trim(),
        url,
        fields,
        is_default: isDefault,
        enabled,
      });
      toast.success(editing ? "Channel updated" : "Channel added");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setFormError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? "Edit channel" : "Add channel"}</DialogTitle>
          <DialogDescription>
            Where alert notifications go. Secrets are encrypted on the server and never shown again.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="channel-name">Name</Label>
            <Input
              id="channel-name"
              value={name}
              maxLength={64}
              placeholder="Ops on Telegram"
              onChange={(e) => setName(e.target.value)}
            />
          </div>

          {changing ? (
            <>
              <div className="space-y-1.5">
                <Label>Service</Label>
                <div className="grid grid-cols-3 gap-1.5" role="radiogroup" aria-label="Service">
                  {SERVICES.map((s) => (
                    <button
                      key={s.id}
                      type="button"
                      role="radio"
                      aria-checked={service === s.id}
                      onClick={() => pickService(s.id)}
                      className={cn(
                        "rounded-md border px-2 py-1.5 text-sm transition-colors hover:border-primary/40",
                        service === s.id && "border-primary bg-primary/5 font-medium text-foreground",
                      )}
                    >
                      {s.label}
                    </button>
                  ))}
                </div>
              </div>
              {spec.fields.map((f) => (
                <div key={f.key} className="space-y-1.5">
                  <Label htmlFor={`channel-${f.key}`}>
                    {f.label}
                    {f.optional ? <span className="font-normal text-muted-foreground"> (optional)</span> : null}
                  </Label>
                  <Input
                    id={`channel-${f.key}`}
                    type={f.secret ? "password" : "text"}
                    autoComplete="off"
                    placeholder={f.placeholder}
                    value={values[f.key] ?? ""}
                    onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                  />
                  {f.hint ? <p className="text-xs text-muted-foreground">{f.hint}</p> : null}
                </div>
              ))}
            </>
          ) : (
            <div className="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm">
              <span className="truncate" data-testid="channel-destination">
                {channelSummary(channel!)}
              </span>
              <Button variant="ghost" size="sm" onClick={() => setChanging(true)}>
                Change destination
              </Button>
            </div>
          )}

          <div className="flex items-center justify-between gap-3">
            <div>
              <Label htmlFor="channel-default">Default channel</Label>
              <p className="text-xs text-muted-foreground">Rules without their own channels notify here.</p>
            </div>
            <Switch id="channel-default" checked={isDefault} onCheckedChange={setIsDefault} />
          </div>
          <div className="flex items-center justify-between gap-3">
            <Label htmlFor="channel-enabled">Enabled</Label>
            <Switch id="channel-enabled" checked={enabled} onCheckedChange={setEnabled} />
          </div>

          {formError ? (
            <p role="alert" className="text-sm text-destructive">
              {formError}
            </p>
          ) : null}
          {test ? (
            <p role="status" className={cn("text-sm", test.ok ? "text-emerald-600 dark:text-emerald-400" : "text-destructive")}>
              {test.ok ? "Test notification sent." : `Test failed: ${test.error}`}
            </p>
          ) : null}
        </div>

        <DialogFooter className="gap-2 sm:justify-between">
          <Button variant="outline" onClick={() => void runTest()} disabled={busy !== null}>
            <Send className="size-4" /> {busy === "test" ? "Sending…" : "Send test"}
          </Button>
          <Button onClick={() => void save()} disabled={busy !== null}>
            {busy === "save" ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

`web/src/components/alerts/channels-tab.tsx`:

```tsx
import { useCallback, useEffect, useState } from "react";
import { BellRing, Pencil, Plus, Send, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { ChannelDialog } from "@/components/alerts/channel-dialog";
import { alertsGet, alertsPost } from "@/hooks/use-alerts";
import { channelSummary, type Channel } from "@/lib/alerts";

// ChannelsTab lists the org's notification channels: destination, default
// flag, last delivery, with Test, Edit and Delete.
export function ChannelsTab() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // editing: undefined = closed, null = new channel.
  const [editing, setEditing] = useState<Channel | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Channel | null>(null);
  const [testing, setTesting] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setChannels((await alertsGet<Channel[] | null>("get-channels")) ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load channels");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const test = async (ch: Channel) => {
    setTesting(ch.id);
    try {
      const res = await alertsPost<{ ok: boolean; error: string }>("test-channel", { id: ch.id });
      if (res.ok) toast.success(`Test sent to ${ch.name}`);
      else toast.error(`${ch.name}: ${res.error}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Test failed");
    } finally {
      setTesting(null);
      void refresh();
    }
  };

  const remove = async () => {
    if (!deleting) return;
    try {
      await alertsPost("delete-channel", { id: deleting.id });
      toast.success(`Deleted ${deleting.name}`);
      void refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(null);
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          Telegram, Discord, Slack, ntfy, Gotify, Pushover, email or any webhook.
        </p>
        <Button size="sm" onClick={() => setEditing(null)}>
          <Plus className="size-4" /> Add channel
        </Button>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {!loading && channels.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BellRing />
            </EmptyMedia>
            <EmptyTitle>No channels yet</EmptyTitle>
            <EmptyDescription>Alerts are recorded in History; add a channel to get notified.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="grid gap-2">
          {channels.map((ch) => (
            <Card key={ch.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 p-3.5" data-testid="channel-row">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate font-medium">{ch.name}</span>
                  {ch.is_default ? <Badge variant="secondary">Default</Badge> : null}
                  {!ch.enabled ? <Badge variant="outline">Disabled</Badge> : null}
                </div>
                <div className="truncate text-xs text-muted-foreground">{channelSummary(ch)}</div>
                {ch.last_error ? (
                  <div className="truncate text-xs text-destructive" title={ch.last_error}>
                    Last delivery failed: {ch.last_error}
                  </div>
                ) : ch.last_sent_at ? (
                  <div className="text-xs text-muted-foreground">
                    Last sent {new Date(ch.last_sent_at).toLocaleString()}
                  </div>
                ) : null}
              </div>
              <div className="flex gap-1">
                <Button variant="ghost" size="sm" onClick={() => void test(ch)} disabled={testing === ch.id}>
                  <Send className="size-4" /> Test
                </Button>
                <Button variant="ghost" size="icon" aria-label={`Edit ${ch.name}`} onClick={() => setEditing(ch)}>
                  <Pencil className="size-4" />
                </Button>
                <Button variant="ghost" size="icon" aria-label={`Delete ${ch.name}`} onClick={() => setDeleting(ch)}>
                  <Trash2 className="size-4" />
                </Button>
              </div>
            </Card>
          ))}
        </div>
      )}

      {editing !== undefined ? (
        <ChannelDialog
          key={editing?.id ?? "new"}
          open
          onOpenChange={(open) => !open && setEditing(undefined)}
          channel={editing ?? undefined}
          onSaved={() => void refresh()}
        />
      ) : null}

      <AlertDialog open={deleting !== null} onOpenChange={(open) => !open && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {deleting?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Rules that send to it will skip it. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => void remove()}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
```

`web/src/pages/org-alerts.tsx`:

```tsx
import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ChannelsTab } from "@/components/alerts/channels-tab";

const TABS = ["channels"] as const;
type Tab = (typeof TABS)[number];

// OrgAlertsPage is Org → Alerts: the org's notification channels (and, in
// later tabs, its rules and history). The tab lives in ?tab= so links can
// open one.
export default function OrgAlertsPage() {
  const breadcrumbs = useMemo(() => [{ label: "Organization" }, { label: "Alerts" }], []);
  useAppBreadcrumbs(breadcrumbs);
  const [params, setParams] = useSearchParams();
  const tab: Tab = (TABS as readonly string[]).includes(params.get("tab") ?? "")
    ? (params.get("tab") as Tab)
    : TABS[0];

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold tracking-tight">Alerts</h1>
      <Tabs value={tab} onValueChange={(v) => setParams({ tab: v }, { replace: true })}>
        <TabsList>
          <TabsTrigger value="channels">Channels</TabsTrigger>
        </TabsList>
        <TabsContent value="channels" className="mt-4">
          <ChannelsTab />
        </TabsContent>
      </Tabs>
    </div>
  );
}
```

- [ ] **Step 3: Add the route and the menu item**

```diff
diff --git a/web/src/components/nav-user.tsx b/web/src/components/nav-user.tsx
index 9220fef..1056af5 100644
--- a/web/src/components/nav-user.tsx
+++ b/web/src/components/nav-user.tsx
@@ -1,6 +1,6 @@
 "use client";
 
-import { ChevronsUpDown, KeyRound, LockKeyhole, LogOut, Users } from "lucide-react";
+import { BellRing, ChevronsUpDown, KeyRound, LockKeyhole, LogOut, Users } from "lucide-react";
 import { useNavigate } from "react-router-dom";
 
 import { useProfile } from "@/context/use-profile";
@@ -90,6 +90,10 @@ export function NavUser() {
                 <Users />
                 Members
               </DropdownMenuItem>
+              <DropdownMenuItem onClick={() => navigate("/org/alerts")}>
+                <BellRing />
+                Alerts
+              </DropdownMenuItem>
               <DropdownMenuItem onClick={() => navigate("/user/tokens")}>
                 <KeyRound />
                 API tokens
diff --git a/web/src/main.tsx b/web/src/main.tsx
index b2a7362..40d94f1 100644
--- a/web/src/main.tsx
+++ b/web/src/main.tsx
@@ -25,6 +25,7 @@ import SettingsPage from "@/pages/settings"
 import UserTokensPage from "@/pages/user-tokens"
 import UserPasswordPage from "@/pages/user-password"
 import OrgMembersPage from "@/pages/org-members"
+import OrgAlertsPage from "@/pages/org-alerts"
 import LoginPage from "@/pages/login"
 import RegisterPage from "@/pages/register"
 import { followSystemTheme } from "@/lib/theme"
@@ -79,6 +80,10 @@ const router = createBrowserRouter([
             path: "/org/members",
             element: <OrgMembersPage />,
           },
+          {
+            path: "/org/alerts",
+            element: <OrgAlertsPage />,
+          },
           { path: "*", element: <NotFound /> },
         ],
       },
```

- [ ] **Step 4: Run the tests, typecheck and lint**

Run: `pnpm --dir web exec vitest run src/components/alerts src/components/nav-user.test.tsx && pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): Org → Alerts page with notification channels

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 12: Web: Rules and History tabs

**Files:**
- Create: `web/src/components/alerts/channel-picker.tsx`, `rule-dialog.tsx`, `rules-tab.tsx`, `history-tab.tsx`
- Modify:
  - `web/src/pages/org-alerts.tsx` (History, Rules and Channels; History by default);
  - `web/src/test/setup.ts` (Radix Select polyfills for jsdom);
  - `web/vite.config.ts` (`testTimeout: 20_000`).
- Test:
  - `web/src/components/alerts/rule-dialog.test.tsx`, `rules-tab.test.tsx`, `history-tab.test.tsx`;
  - `web/src/pages/org-alerts.test.tsx`.

**Interfaces:**
- Consumes: Tasks 10 and 11.
- Produces:
  - `ChannelPicker({value, onChange, channels, idPrefix})`, where `value` is null (defaults), `[]` (History only) or ids;
  - `RuleDialog({open, onOpenChange, rule?, channels, onSaved})`. Kind and metric are disabled when editing, and the threshold field is hidden for `down`;
  - `RulesTab()`: Servers and Apps groups (`role="region"`), an inline enable switch that posts the whole rule, edit, and delete with confirmation;
  - `HistoryTab()`: kind and server filters, target links into `/server/:id/monitoring` or `/server/:id/app/:appId?tab=metrics`, delivery badges (✓/✗ with the error as title), and "Load more" through `next_cursor`.

Notes from prototyping:
- Radix Select needs `hasPointerCapture`, `releasePointerCapture` and `scrollIntoView`, which jsdom lacks; they go in `setup.ts`.
- Under a parallel run, a dialog test that drives two Selects and types into several inputs takes 5–8 s in jsdom. Hence `userEvent.setup({ delay: null })` and a 20 s `testTimeout`.
- The full web suite (244 tests) takes about 60 s.

- [ ] **Step 1: Write the failing tests**

`web/src/components/alerts/rule-dialog.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RuleDialog } from "@/components/alerts/rule-dialog";
import type { Channel, Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const channels = [
  { id: "c1", name: "Ops", enabled: true },
  { id: "c2", name: "Pager", enabled: true },
] as Channel[];

describe("RuleDialog", () => {
  it("creates an app disk rule routed to chosen channels", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-rule": {} });
    const onSaved = vi.fn();
    const user = userEvent.setup({ delay: null });
    render(<RuleDialog open onOpenChange={() => {}} channels={channels} onSaved={onSaved} />);

    await user.click(screen.getByRole("combobox", { name: "Watch" }));
    await user.click(await screen.findByRole("option", { name: "Apps" }));
    await user.click(screen.getByRole("combobox", { name: "Metric" }));
    await user.click(await screen.findByRole("option", { name: "Disk" }));
    expect(screen.getByLabelText(/Above \(GB\)/)).toHaveValue(50);
    await user.clear(screen.getByLabelText(/Above/));
    await user.type(screen.getByLabelText(/Above/), "120");
    await user.clear(screen.getByLabelText("For (minutes)"));
    await user.type(screen.getByLabelText("For (minutes)"), "15");
    await user.click(screen.getByLabelText("Specific channels"));
    await user.click(screen.getByLabelText("Pager"));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(api.posted("/api/v1/alerts/save-rule")).toEqual([
      {
        id: "",
        target_kind: "app",
        metric: "disk",
        threshold: 120,
        duration_minutes: 15,
        channel_ids: ["c1", "c2"],
        enabled: true,
      },
    ]);
    expect(onSaved).toHaveBeenCalled();
  });

  it("has no threshold for down rules and checks the duration", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-rule": {} });
    const user = userEvent.setup({ delay: null });
    render(<RuleDialog open onOpenChange={() => {}} channels={[]} onSaved={() => {}} />);
    await user.click(screen.getByRole("combobox", { name: "Metric" }));
    await user.click(await screen.findByRole("option", { name: "Down" }));
    expect(screen.queryByLabelText(/Above/)).not.toBeInTheDocument();
    await user.clear(screen.getByLabelText("For (minutes)"));
    await user.type(screen.getByLabelText("For (minutes)"), "90");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("1 to 60 minutes");
    expect(api.calls).toHaveLength(0);
  });

  it("edits a rule without changing what it watches", async () => {
    const rule: Rule = {
      id: "r1",
      target_kind: "server",
      metric: "memory",
      threshold: 90,
      duration_minutes: 10,
      channel_ids: [],
      enabled: true,
      created_at: "2026-10-11T12:00:00Z",
    };
    const api = fakeAPI({ "/api/v1/alerts/save-rule": rule });
    const user = userEvent.setup({ delay: null });
    render(<RuleDialog open onOpenChange={() => {}} rule={rule} channels={channels} onSaved={() => {}} />);
    expect(screen.getByRole("combobox", { name: "Watch" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Metric" })).toBeDisabled();
    expect(screen.getByLabelText("History only")).toBeChecked();
    await user.clear(screen.getByLabelText(/Above/));
    await user.type(screen.getByLabelText(/Above/), "101");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("at most 100");
    await user.clear(screen.getByLabelText(/Above/));
    await user.type(screen.getByLabelText(/Above/), "85");
    await user.click(screen.getByLabelText("Default channels"));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-rule")[0]).toMatchObject({ id: "r1", metric: "memory", threshold: 85, channel_ids: null });
  });
});
```

`web/src/components/alerts/rules-tab.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RulesTab } from "@/components/alerts/rules-tab";
import type { Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const base = { channel_ids: null, enabled: true, created_at: "2026-10-11T12:00:00Z" };
const rules: Rule[] = [
  { ...base, id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10 },
  { ...base, id: "r2", target_kind: "server", metric: "down", threshold: 0, duration_minutes: 5 },
  { ...base, id: "r3", target_kind: "app", metric: "down", threshold: 0, duration_minutes: 5, channel_ids: ["c1"] },
];

describe("RulesTab", () => {
  it("groups rules by target and toggles one inline", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-channels": [{ id: "c1", name: "Ops", enabled: true }],
      "/api/v1/alerts/save-rule": {},
      "/api/v1/alerts/delete-rule": null,
    });
    const user = userEvent.setup({ delay: null });
    render(<RulesTab />);

    const servers = await screen.findByRole("region", { name: "Servers" });
    expect(within(servers).getAllByTestId("rule-row")).toHaveLength(2);
    expect(within(servers).getByText("CPU above 90% for 10 min")).toBeInTheDocument();
    const apps = screen.getByRole("region", { name: "Apps" });
    expect(within(apps).getByText("App down for 5 min")).toBeInTheDocument();
    expect(within(apps).getByText("Ops")).toBeInTheDocument();

    await user.click(screen.getByRole("switch", { name: "Enable CPU above 90% for 10 min" }));
    expect(api.posted("/api/v1/alerts/save-rule")[0]).toMatchObject({ id: "r1", enabled: false, threshold: 90 });

    await user.click(screen.getByRole("button", { name: "Delete Server down for 5 min" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(api.posted("/api/v1/alerts/delete-rule")).toEqual([{ id: "r2" }]);
  });
});
```

`web/src/components/alerts/history-tab.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { HistoryTab } from "@/components/alerts/history-tab";
import type { AlertEvent } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: [{ id: "s1", name: "prod-1" }, { id: "s2", name: "lab" }] }),
}));

afterEach(() => vi.unstubAllGlobals());

const event = (id: string, over: Partial<AlertEvent> = {}): AlertEvent => ({
  id,
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  server_name: "prod-1",
  app_name: "",
  target_kind: "server",
  metric: "cpu",
  kind: "fired",
  value: 93,
  threshold: 90,
  title: `🔥 prod-1 · CPU 93% > 90% for 10m (${id})`,
  message: "",
  deliveries: [],
  created_at: "2026-10-11T12:00:00Z",
  ...over,
});

describe("HistoryTab", () => {
  it("shows events with targets and deliveries, filters and pages", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-events": () =>
        api.calls.at(-1)?.query.includes("cursor")
          ? { events: [event("e3")], next_cursor: "" }
          : {
              events: [
                event("e1", {
                  app_id: "a1",
                  app_name: "nextcloud",
                  deliveries: [
                    { channel_id: "c1", channel_name: "Ops", ok: true, error: "" },
                    { channel_id: "c2", channel_name: "Mail", ok: false, error: "535 auth failed" },
                  ],
                }),
                event("e2", { kind: "resolved", title: "✅ Resolved · prod-1 · CPU 41%" }),
              ],
              next_cursor: "1760184000_e2",
            },
    });
    const user = userEvent.setup({ delay: null });
    render(
      <MemoryRouter>
        <HistoryTab />
      </MemoryRouter>,
    );

    const rows = await screen.findAllByTestId("event-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByRole("link", { name: "prod-1 / nextcloud" })).toHaveAttribute(
      "href",
      "/server/s1/app/a1?tab=metrics",
    );
    expect(within(rows[0]).getByText(/✓ Ops/)).toBeInTheDocument();
    expect(within(rows[0]).getByText(/✗ Mail/)).toHaveAttribute("title", "535 auth failed");
    expect(within(rows[1]).getByLabelText("Resolved")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Not sent")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findAllByTestId("event-row")).toHaveLength(3);
    expect(api.calls.at(-1)?.query).toContain("cursor=1760184000_e2");

    await user.click(screen.getByRole("combobox", { name: "Kind" }));
    await user.click(await screen.findByRole("option", { name: "Fired" }));
    expect(api.calls.at(-1)?.query).toBe("?kind=fired");
    await user.click(screen.getByRole("combobox", { name: "Server" }));
    await user.click(await screen.findByRole("option", { name: "lab" }));
    expect(api.calls.at(-1)?.query).toBe("?kind=fired&server_id=s2");
  });
});
```

`web/src/pages/org-alerts.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import OrgAlertsPage from "@/pages/org-alerts";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [] }) }));

afterEach(() => vi.unstubAllGlobals());

describe("OrgAlertsPage", () => {
  it("opens on History and switches tabs", async () => {
    fakeAPI({
      "/api/v1/alerts/get-events": { events: [], next_cursor: "" },
      "/api/v1/alerts/get-rules": [],
      "/api/v1/alerts/get-channels": [],
    });
    const user = userEvent.setup({ delay: null });
    render(
      <MemoryRouter initialEntries={["/org/alerts"]}>
        <OrgAlertsPage />
      </MemoryRouter>,
    );
    expect(await screen.findByText(/No alerts yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Channels" }));
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
  });

  it("honours ?tab=", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": [], "/api/v1/alerts/get-channels": [] });
    render(
      <MemoryRouter initialEntries={["/org/alerts?tab=rules"]}>
        <OrgAlertsPage />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("button", { name: /Add rule/ })).toBeInTheDocument();
  });
});
```

Apply to `web/src/test/setup.ts` and `web/vite.config.ts`:

```diff
diff --git a/web/src/test/setup.ts b/web/src/test/setup.ts
index 56daa64..1617a4e 100644
--- a/web/src/test/setup.ts
+++ b/web/src/test/setup.ts
@@ -15,3 +15,8 @@ globalThis.ResizeObserver ??= class {
   unobserve() {}
   disconnect() {}
 };
+
+// Radix Select uses pointer capture and scrollIntoView, which jsdom lacks.
+Element.prototype.hasPointerCapture ??= () => false;
+Element.prototype.releasePointerCapture ??= () => {};
+Element.prototype.scrollIntoView ??= () => {};
diff --git a/web/vite.config.ts b/web/vite.config.ts
index ca9f201..aa1e0f7 100644
--- a/web/vite.config.ts
+++ b/web/vite.config.ts
@@ -26,6 +26,9 @@ export default defineConfig({
   test: {
     environment: "jsdom",
     setupFiles: ["./src/test/setup.ts"],
+    // Dialog tests drive Radix Select and type into several inputs; under
+    // a parallel run in jsdom one such test takes seconds.
+    testTimeout: 20_000,
     coverage: {
       provider: "v8",
       reporter: ["text", "lcov"],
```

Run: `pnpm --dir web exec vitest run src/components/alerts src/pages/org-alerts.test.tsx`
Expected: FAIL (missing modules; the page has only the Channels tab).

- [ ] **Step 2: Write the picker, the dialog and the tabs**

`web/src/components/alerts/channel-picker.tsx`:

```tsx
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { routingOf, type Channel } from "@/lib/alerts";

type Props = {
  // value: null = default channels, [] = History only, else those ids.
  value: string[] | null;
  onChange: (ids: string[] | null) => void;
  channels: Channel[];
  // idPrefix keeps element ids unique when two pickers share a page.
  idPrefix: string;
};

// ChannelPicker chooses where notifications go: the org's default channels,
// specific channels, or none (the alert is only recorded in History).
export function ChannelPicker({ value, onChange, channels, idPrefix }: Props) {
  const routing = routingOf(value);
  const selected = value ?? [];
  return (
    <div className="space-y-2">
      <RadioGroup
        value={routing}
        onValueChange={(v) => {
          if (v === "default") onChange(null);
          else if (v === "none") onChange([]);
          else onChange(channels.filter((c) => c.enabled).slice(0, 1).map((c) => c.id));
        }}
      >
        {(
          [
            ["default", "Default channels"],
            ["specific", "Specific channels"],
            ["none", "History only"],
          ] as const
        ).map(([v, label]) => (
          <div key={v} className="flex items-center gap-2">
            <RadioGroupItem value={v} id={`${idPrefix}-${v}`} />
            <Label htmlFor={`${idPrefix}-${v}`} className="font-normal">
              {label}
            </Label>
          </div>
        ))}
      </RadioGroup>
      {routing === "specific" ? (
        channels.length === 0 ? (
          <p className="text-xs text-muted-foreground">No channels yet: add one in the Channels tab.</p>
        ) : (
          <div className="grid gap-1.5 pl-6">
            {channels.map((c) => (
              <div key={c.id} className="flex items-center gap-2">
                <Checkbox
                  id={`${idPrefix}-ch-${c.id}`}
                  checked={selected.includes(c.id)}
                  onCheckedChange={(checked) =>
                    onChange(checked ? [...selected, c.id] : selected.filter((id) => id !== c.id))
                  }
                />
                <Label htmlFor={`${idPrefix}-ch-${c.id}`} className="font-normal">
                  {c.name}
                  {!c.enabled ? <span className="text-muted-foreground"> (disabled)</span> : null}
                </Label>
              </div>
            ))}
          </div>
        )
      ) : null}
    </div>
  );
}
```

`web/src/components/alerts/rule-dialog.tsx`:

```tsx
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ChannelPicker } from "@/components/alerts/channel-picker";
import { alertsPost } from "@/hooks/use-alerts";
import {
  ALERT_METRICS,
  hasThreshold,
  metricInfo,
  type AlertMetric,
  type Channel,
  type Rule,
  type TargetKind,
} from "@/lib/alerts";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rule?: Rule;
  channels: Channel[];
  onSaved: () => void;
};

// RuleDialog creates or edits an org-wide alert rule. A rule's target kind
// and metric are fixed once created.
export function RuleDialog({ open, onOpenChange, rule, channels, onSaved }: Props) {
  const editing = rule !== undefined;
  const [kind, setKind] = useState<TargetKind>(rule?.target_kind ?? "server");
  const [metric, setMetric] = useState<AlertMetric>(rule?.metric ?? "cpu");
  const [threshold, setThreshold] = useState(String(rule?.threshold ?? metricInfo("server", "cpu")!.suggested));
  const [minutes, setMinutes] = useState(String(rule?.duration_minutes ?? 10));
  const [channelIds, setChannelIds] = useState<string[] | null>(rule?.channel_ids ?? null);
  const [enabled, setEnabled] = useState(rule?.enabled ?? true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const info = metricInfo(kind, metric);
  const pick = (k: TargetKind, m: AlertMetric) => {
    const next = metricInfo(k, m) ?? metricInfo(k, "down")!;
    setKind(k);
    setMetric(next.metric);
    setThreshold(String(next.suggested));
  };

  const save = async () => {
    const t = Number(threshold);
    const d = Number(minutes);
    if (!Number.isInteger(d) || d < 1 || d > 60) {
      setError("Duration must be 1 to 60 minutes");
      return;
    }
    if (hasThreshold(metric) && (!Number.isFinite(t) || t <= 0 || (info!.max > 0 && t > info!.max))) {
      setError(info!.max > 0 ? `Threshold must be above 0 and at most ${info!.max}` : "Threshold must be above 0");
      return;
    }
    setBusy(true);
    try {
      await alertsPost("save-rule", {
        id: rule?.id ?? "",
        target_kind: kind,
        metric,
        threshold: hasThreshold(metric) ? t : 0,
        duration_minutes: d,
        channel_ids: channelIds,
        enabled,
      });
      toast.success(editing ? "Rule updated" : "Rule added");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{editing ? "Edit rule" : "Add rule"}</DialogTitle>
          <DialogDescription>
            Applies to every {kind === "app" ? "app" : "server"} of the organization; a server or app can override it.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="rule-kind">Watch</Label>
              <Select value={kind} onValueChange={(v) => pick(v as TargetKind, metric)} disabled={editing}>
                <SelectTrigger id="rule-kind" aria-label="Watch">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="server">Servers</SelectItem>
                  <SelectItem value="app">Apps</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="rule-metric">Metric</Label>
              <Select value={metric} onValueChange={(v) => pick(kind, v as AlertMetric)} disabled={editing}>
                <SelectTrigger id="rule-metric" aria-label="Metric">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ALERT_METRICS.filter((m) => m.kind === kind).map((m) => (
                    <SelectItem key={m.metric} value={m.metric}>
                      {m.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            {hasThreshold(metric) ? (
              <div className="space-y-1.5">
                <Label htmlFor="rule-threshold">Above ({info?.unit === "load" ? "load" : info?.unit})</Label>
                <Input
                  id="rule-threshold"
                  type="number"
                  inputMode="decimal"
                  min={0}
                  max={info?.max || undefined}
                  step="any"
                  value={threshold}
                  onChange={(e) => setThreshold(e.target.value)}
                />
              </div>
            ) : null}
            <div className="space-y-1.5">
              <Label htmlFor="rule-minutes">For (minutes)</Label>
              <Input
                id="rule-minutes"
                type="number"
                min={1}
                max={60}
                value={minutes}
                onChange={(e) => setMinutes(e.target.value)}
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>Notify</Label>
            <ChannelPicker value={channelIds} onChange={setChannelIds} channels={channels} idPrefix="rule" />
          </div>
          <div className="flex items-center justify-between">
            <Label htmlFor="rule-enabled">Enabled</Label>
            <Switch id="rule-enabled" checked={enabled} onCheckedChange={setEnabled} />
          </div>
          {error ? (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button onClick={() => void save()} disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

`web/src/components/alerts/rules-tab.tsx`:

```tsx
import { useCallback, useEffect, useState } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { RuleDialog } from "@/components/alerts/rule-dialog";
import { alertsGet, alertsPost } from "@/hooks/use-alerts";
import { routingText, ruleSummary, type Channel, type Rule } from "@/lib/alerts";
import { cn } from "@/lib/utils";

// RulesTab lists the org's rules, servers first, with an inline enable
// toggle, edit and delete.
export function RulesTab() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Rule | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Rule | null>(null);

  const refresh = useCallback(async () => {
    try {
      const [r, c] = await Promise.all([
        alertsGet<Rule[] | null>("get-rules"),
        alertsGet<Channel[] | null>("get-channels"),
      ]);
      setRules(r ?? []);
      setChannels(c ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load rules");
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const toggle = async (rule: Rule, enabled: boolean) => {
    setRules((rs) => rs.map((r) => (r.id === rule.id ? { ...r, enabled } : r)));
    try {
      await alertsPost("save-rule", { ...rule, enabled });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Update failed");
      void refresh();
    }
  };

  const remove = async () => {
    if (!deleting) return;
    try {
      await alertsPost("delete-rule", { id: deleting.id });
      toast.success("Rule deleted");
      void refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(null);
    }
  };

  const group = (kind: "server" | "app", title: string) => {
    const list = rules.filter((r) => r.target_kind === kind);
    return (
      <section className="space-y-2" aria-label={title}>
        <h2 className="text-sm font-medium text-muted-foreground">{title}</h2>
        {list.length === 0 ? (
          <p className="text-sm text-muted-foreground">No rules.</p>
        ) : (
          list.map((r) => (
            <Card key={r.id} className="flex items-center gap-3 p-3.5" data-testid="rule-row">
              <Switch
                checked={r.enabled}
                aria-label={`Enable ${ruleSummary(r)}`}
                onCheckedChange={(v) => void toggle(r, v)}
              />
              <div className={cn("min-w-0 flex-1", !r.enabled && "opacity-60")}>
                <div className="truncate font-medium">{ruleSummary(r)}</div>
                <div className="truncate text-xs text-muted-foreground">{routingText(r.channel_ids, channels)}</div>
              </div>
              <Button variant="ghost" size="icon" aria-label={`Edit ${ruleSummary(r)}`} onClick={() => setEditing(r)}>
                <Pencil className="size-4" />
              </Button>
              <Button variant="ghost" size="icon" aria-label={`Delete ${ruleSummary(r)}`} onClick={() => setDeleting(r)}>
                <Trash2 className="size-4" />
              </Button>
            </Card>
          ))
        )}
      </section>
    );
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          Rules apply to every server or app; override them per server or app from its Monitoring or Metrics page.
        </p>
        <Button size="sm" onClick={() => setEditing(null)}>
          <Plus className="size-4" /> Add rule
        </Button>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {group("server", "Servers")}
      {group("app", "Apps")}

      {editing !== undefined ? (
        <RuleDialog
          key={editing?.id ?? "new"}
          open
          onOpenChange={(open) => !open && setEditing(undefined)}
          rule={editing ?? undefined}
          channels={channels}
          onSaved={() => void refresh()}
        />
      ) : null}

      <AlertDialog open={deleting !== null} onOpenChange={(open) => !open && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this rule?</AlertDialogTitle>
            <AlertDialogDescription>
              {deleting ? ruleSummary(deleting) : ""}. Its overrides go with it; its history stays.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => void remove()}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
```

`web/src/components/alerts/history-tab.tsx`:

```tsx
import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { CircleCheck, Flame } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useServers } from "@/context/use-servers";
import { alertsGet } from "@/hooks/use-alerts";
import type { AlertEvent } from "@/lib/alerts";
import { serverPath } from "@/lib/server-paths";

type Page = { events: AlertEvent[] | null; next_cursor: string };

const ALL = "all";

// targetPath links an event to its server's Monitoring page or its app's
// Metrics tab.
function targetPath(ev: AlertEvent): string {
  return ev.app_id
    ? serverPath(ev.server_id, `/app/${encodeURIComponent(ev.app_id)}?tab=metrics`)
    : serverPath(ev.server_id, "/monitoring");
}

// HistoryTab is the org's alert history, newest first, filterable by kind
// and server, with each event's delivery outcome per channel.
export function HistoryTab() {
  const { servers } = useServers();
  const [kind, setKind] = useState(ALL);
  const [server, setServer] = useState(ALL);
  const [events, setEvents] = useState<AlertEvent[]>([]);
  const [cursor, setCursor] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (after: string) => {
      setLoading(true);
      const params: Record<string, string> = {};
      if (kind !== ALL) params.kind = kind;
      if (server !== ALL) params.server_id = server;
      if (after) params.cursor = after;
      try {
        const page = await alertsGet<Page>("get-events", params);
        setEvents((prev) => (after ? [...prev, ...(page.events ?? [])] : (page.events ?? [])));
        setCursor(page.next_cursor);
        setError(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : "Failed to load history");
      } finally {
        setLoading(false);
      }
    },
    [kind, server],
  );

  useEffect(() => {
    void load("");
  }, [load]);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-2">
        <Select value={kind} onValueChange={setKind}>
          <SelectTrigger className="w-36" aria-label="Kind">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All events</SelectItem>
            <SelectItem value="fired">Fired</SelectItem>
            <SelectItem value="resolved">Resolved</SelectItem>
          </SelectContent>
        </Select>
        {servers.length > 1 ? (
          <Select value={server} onValueChange={setServer}>
            <SelectTrigger className="w-44" aria-label="Server">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All servers</SelectItem>
              {servers.map((s) => (
                <SelectItem key={s.id} value={s.id}>
                  {s.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {!loading && events.length === 0 ? (
        <p className="text-sm text-muted-foreground">No alerts yet. Fired and resolved alerts show up here.</p>
      ) : null}
      <div className="grid gap-2">
        {events.map((ev) => (
          <Card key={ev.id} className="flex items-start gap-3 p-3.5" data-testid="event-row">
            {ev.kind === "fired" ? (
              <Flame className="mt-0.5 size-4 shrink-0 text-destructive" aria-label="Fired" />
            ) : (
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" aria-label="Resolved" />
            )}
            <div className="min-w-0 flex-1 space-y-1">
              <div className="truncate font-medium">{ev.title}</div>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                <time dateTime={ev.created_at}>{new Date(ev.created_at).toLocaleString()}</time>
                <Link to={targetPath(ev)} className="hover:text-foreground hover:underline">
                  {ev.app_name ? `${ev.server_name} / ${ev.app_name}` : ev.server_name}
                </Link>
                {(ev.deliveries ?? []).length === 0 ? <span>Not sent</span> : null}
                {(ev.deliveries ?? []).map((d) => (
                  <Badge
                    key={d.channel_id}
                    variant={d.ok ? "secondary" : "destructive"}
                    title={d.ok ? "Delivered" : d.error}
                  >
                    {d.ok ? "✓" : "✗"} {d.channel_name}
                  </Badge>
                ))}
              </div>
            </div>
          </Card>
        ))}
      </div>
      {cursor ? (
        <Button variant="outline" size="sm" onClick={() => void load(cursor)} disabled={loading}>
          Load more
        </Button>
      ) : null}
    </div>
  );
}
```

- [ ] **Step 3: Give the page its three tabs**

```diff
diff --git a/web/src/pages/org-alerts.tsx b/web/src/pages/org-alerts.tsx
index efe8b06..d985a63 100644
--- a/web/src/pages/org-alerts.tsx
+++ b/web/src/pages/org-alerts.tsx
@@ -4,13 +4,14 @@ import { useSearchParams } from "react-router-dom";
 import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
 import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
 import { ChannelsTab } from "@/components/alerts/channels-tab";
+import { HistoryTab } from "@/components/alerts/history-tab";
+import { RulesTab } from "@/components/alerts/rules-tab";
 
-const TABS = ["channels"] as const;
+const TABS = ["history", "rules", "channels"] as const;
 type Tab = (typeof TABS)[number];
 
-// OrgAlertsPage is Org → Alerts: the org's notification channels (and, in
-// later tabs, its rules and history). The tab lives in ?tab= so links can
-// open one.
+// OrgAlertsPage is Org → Alerts: the alert history, the org-wide rules and
+// the notification channels. The tab lives in ?tab= so links can open one.
 export default function OrgAlertsPage() {
   const breadcrumbs = useMemo(() => [{ label: "Organization" }, { label: "Alerts" }], []);
   useAppBreadcrumbs(breadcrumbs);
@@ -24,8 +25,16 @@ export default function OrgAlertsPage() {
       <h1 className="text-2xl font-semibold tracking-tight">Alerts</h1>
       <Tabs value={tab} onValueChange={(v) => setParams({ tab: v }, { replace: true })}>
         <TabsList>
+          <TabsTrigger value="history">History</TabsTrigger>
+          <TabsTrigger value="rules">Rules</TabsTrigger>
           <TabsTrigger value="channels">Channels</TabsTrigger>
         </TabsList>
+        <TabsContent value="history" className="mt-4">
+          <HistoryTab />
+        </TabsContent>
+        <TabsContent value="rules" className="mt-4">
+          <RulesTab />
+        </TabsContent>
         <TabsContent value="channels" className="mt-4">
           <ChannelsTab />
         </TabsContent>
```

- [ ] **Step 4: Run the tests, typecheck and lint**

Run: `pnpm --dir web exec vitest run && pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: the whole web suite passes; no type or lint errors.

- [ ] **Step 5: Commit**

```bash
git add web
git commit -m "feat(web): alert rules and history tabs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 13: Web: alert cards, overrides, badges, dashboard strip and toasts

**Files:**
- Create:
  - `web/src/components/alerts/target-alerts-card.tsx`, `override-dialog.tsx`, `active-alerts.tsx`, `alert-toasts.tsx`;
  - `web/src/test/notifications.tsx`.
- Modify:
  - `web/src/pages/monitoring.tsx`, `web/src/components/app-metrics.tsx`, `web/src/pages/app-details.tsx`;
  - `web/src/components/server-cards.tsx`, `web/src/lib/metrics.ts`;
  - `web/src/pages/home.tsx`, `web/src/layouts/app-layout.tsx`.
- Test:
  - `web/src/components/alerts/target-alerts-card.test.tsx`, `active-alerts.test.tsx`, `alert-toasts.test.tsx`;
  - `web/src/components/server-cards.test.tsx`, `web/src/pages/monitoring.test.tsx`, `web/src/components/app-metrics.test.tsx`.

**Interfaces:**
- Consumes: Tasks 10 to 12; `useNow` from `@/hooks/use-now`; `ServerOverview` from `@/lib/metrics`.
- Produces:
  - `TargetAlertsCard({serverId, appId?, targetLabel})`: what fires on this target now, then every org rule of its kind with the effective setting and an **Adjust** button that opens `OverrideDialog`;
  - `OverrideDialog`: "Alert on X", threshold and duration placeholders showing the rule's values, "Same as the rule / Specific channels / History only", and "Reset to rule";
  - `FiringBadge({count})`, `AppAlertsBadge({serverId, appId})`, `ActiveAlertsStrip()`;
  - `AlertToasts()`, mounted once in `AppLayout`;
  - `ServerOverview.alerts_firing?: number`;
  - `AppMetricsTab` gains an `appName?: string` prop.

Placement follows the spec's §4:
- the server Monitoring page gets the Alerts card after the Docker disk card;
- the app Metrics tab gets the Alerts card after its charts;
- the app header gets a firing badge next to its status;
- each dashboard server tile gets a firing badge next to its name;
- the dashboard gets the active-alerts strip above "Servers".

Every surface follows the Frost look: `Card`, destructive tone for firing, muted meta, nothing new.

- [ ] **Step 1: Write the failing tests**

`web/src/test/notifications.tsx`:

```tsx
import type { ReactNode } from "react";

import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
} from "@/context/notifications-context-base";

// fakeNotifications is an SSE stand-in: emit delivers a notification to
// every subscribed handler.
export function fakeNotifications() {
  const handlers = new Set<NotificationHandler>();
  const value = {
    connected: true,
    subscribe: (h: NotificationHandler) => {
      handlers.add(h);
      return () => {
        handlers.delete(h);
      };
    },
    waitFor: () => new Promise<Notification>(() => {}),
  };
  return {
    emit: (n: Notification) => handlers.forEach((h) => h(n)),
    Provider: ({ children }: { children: ReactNode }) => (
      <NotificationsContext.Provider value={value}>{children}</NotificationsContext.Provider>
    ),
  };
}
```

`web/src/components/alerts/target-alerts-card.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { TargetAlertsCard } from "@/components/alerts/target-alerts-card";
import type { Override, Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const base = { channel_ids: null, enabled: true, created_at: "2026-10-11T12:00:00Z" };
const rules: Rule[] = [
  { ...base, id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10 },
  { ...base, id: "r2", target_kind: "server", metric: "disk", threshold: 90, duration_minutes: 5 },
  { ...base, id: "r3", target_kind: "server", metric: "down", threshold: 0, duration_minutes: 5 },
  { ...base, id: "r4", target_kind: "app", metric: "down", threshold: 0, duration_minutes: 5 },
];
const overrides: Override[] = [
  { rule_id: "r1", server_id: "s1", app_id: "", disabled: false, threshold: 95, duration_minutes: null, channel_ids: null },
  { rule_id: "r2", server_id: "s1", app_id: "", disabled: true, threshold: null, duration_minutes: null, channel_ids: null },
];

function renderCard() {
  return render(
    <MemoryRouter>
      <TargetAlertsCard serverId="s1" targetLabel="prod-1" />
    </MemoryRouter>,
  );
}

describe("TargetAlertsCard", () => {
  it("shows what fires here and every server rule with its effective setting", async () => {
    fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-overrides": overrides,
      "/api/v1/alerts/get-channels": [],
      "/api/v1/alerts/get-active": [
        { rule_id: "r1", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "", since: new Date().toISOString(), value: 97, threshold: 95, server_name: "prod-1", app_name: "" },
        { rule_id: "r4", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1", since: new Date().toISOString(), value: 0, threshold: 0, server_name: "prod-1", app_name: "x" },
      ],
    });
    renderCard();
    const firing = await screen.findByRole("list", { name: "Firing" });
    // The app's alert belongs to the app's card, not the server's.
    expect(within(firing).getAllByRole("listitem")).toHaveLength(1);
    expect(firing).toHaveTextContent("CPU 97% > 95%");

    const rows = await screen.findAllByTestId("target-rule");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("CPU above 95% for 10 min");
    expect(within(rows[0]).getByText("Adjusted")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Off here")).toBeInTheDocument();
    expect(rows[2]).toHaveTextContent("Down for 5 min");
  });

  it("saves and resets an override for this target", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-overrides": overrides,
      "/api/v1/alerts/get-channels": [{ id: "c1", name: "Ops", enabled: true }],
      "/api/v1/alerts/get-active": [],
      "/api/v1/alerts/save-override": {},
      "/api/v1/alerts/delete-override": null,
    });
    const user = userEvent.setup({ delay: null });
    renderCard();
    expect(await screen.findByText("Nothing firing.")).toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "Adjust Down for prod-1" }));
    expect(screen.queryByLabelText(/Above/)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("For (minutes)"), "15");
    await user.click(screen.getByLabelText("Specific channels"));
    await user.click(screen.getByLabelText("Ops"));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-override")).toEqual([
      { rule_id: "r3", server_id: "s1", app_id: "", disabled: false, threshold: null, duration_minutes: 15, channel_ids: ["c1"] },
    ]);

    await user.click(screen.getByRole("button", { name: "Adjust CPU for prod-1" }));
    expect(screen.getByLabelText(/Above/)).toHaveValue(95);
    await user.click(screen.getByRole("button", { name: "Reset to rule" }));
    expect(api.posted("/api/v1/alerts/delete-override")).toEqual([{ rule_id: "r1", server_id: "s1", app_id: "" }]);
  });
});
```

`web/src/components/alerts/active-alerts.test.tsx`:

```tsx
import { act, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ActiveAlertsStrip, AppAlertsBadge, FiringBadge } from "@/components/alerts/active-alerts";
import type { ActiveAlert } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";
import { fakeNotifications } from "@/test/notifications";

afterEach(() => vi.unstubAllGlobals());

const since = new Date(Date.now() - 12 * 60_000).toISOString();
const firing: ActiveAlert[] = [
  { rule_id: "r1", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "", since, value: 93.4, threshold: 90, server_name: "prod-1", app_name: "" },
  { rule_id: "r2", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1", since, value: 7, threshold: 0, server_name: "prod-1", app_name: "nextcloud" },
];

describe("ActiveAlertsStrip", () => {
  it("lists firing alerts with links, and refreshes on an alert notification", async () => {
    let list = firing.slice(0, 1);
    const api = fakeAPI({ "/api/v1/alerts/get-active": () => list });
    const sse = fakeNotifications();
    render(
      <MemoryRouter>
        <sse.Provider>
          <ActiveAlertsStrip />
        </sse.Provider>
      </MemoryRouter>,
    );
    const strip = await screen.findByRole("region", { name: "Active alerts" });
    expect(strip).toHaveTextContent("1 active alert");
    expect(within(strip).getByRole("link", { name: "prod-1" })).toHaveAttribute("href", "/server/s1/monitoring");
    expect(strip).toHaveTextContent("CPU 93% > 90%");
    expect(strip).toHaveTextContent("12 min");

    list = firing;
    await act(async () => sse.emit({ type: "alert", ref: "", payload: {} }));
    expect(await screen.findByRole("link", { name: "prod-1 / nextcloud" })).toHaveAttribute(
      "href",
      "/server/s1/app/a1?tab=metrics",
    );
    expect(screen.getByRole("region", { name: "Active alerts" })).toHaveTextContent("2 active alerts");
    expect(api.calls.filter((c) => c.path === "/api/v1/alerts/get-active")).toHaveLength(2);
  });

  it("renders nothing while all is quiet", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [] });
    const { container } = render(
      <MemoryRouter>
        <ActiveAlertsStrip />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(api.calls).toHaveLength(1));
    expect(container).toBeEmptyDOMElement();
  });
});

describe("badges", () => {
  it("counts firing alerts and hides at zero", () => {
    const { rerender } = render(<FiringBadge count={3} />);
    expect(screen.getByTestId("firing-badge")).toHaveTextContent("3");
    rerender(<FiringBadge count={0} />);
    expect(screen.queryByTestId("firing-badge")).not.toBeInTheDocument();
  });

  it("asks for one app's alerts", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": firing.slice(1) });
    render(
      <MemoryRouter>
        <AppAlertsBadge serverId="s1" appId="a1" />
      </MemoryRouter>,
    );
    expect(await screen.findByTestId("firing-badge")).toHaveTextContent("1");
    expect(api.calls[0].query).toBe("?server_id=s1&app_id=a1");
  });
});
```

`web/src/components/alerts/alert-toasts.test.tsx`:

```tsx
import { act, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AlertToasts } from "@/components/alerts/alert-toasts";
import { fakeNotifications } from "@/test/notifications";

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

describe("AlertToasts", () => {
  it("toasts fired alerts red and resolved ones green, ignoring other events", () => {
    const sse = fakeNotifications();
    render(
      <sse.Provider>
        <AlertToasts />
      </sse.Provider>,
    );
    act(() => {
      sse.emit({ type: "alert", ref: "", payload: { kind: "fired", title: "🔥 prod-1 · CPU 93% > 90% for 10m" } });
      sse.emit({ type: "alert", ref: "", payload: { kind: "resolved", title: "✅ Resolved · prod-1 · CPU 41%" } });
      sse.emit({ type: "apps_status", ref: "", payload: {} });
    });
    expect(toast.error).toHaveBeenCalledWith("🔥 prod-1 · CPU 93% > 90% for 10m");
    expect(toast.success).toHaveBeenCalledWith("✅ Resolved · prod-1 · CPU 41%");
    expect(toast.error).toHaveBeenCalledTimes(1);
  });
});
```

Apply to the existing tests. Each page test stubs the Alerts card, which has its own tests, and checks that the page places it with the right target. The server cards test gains a badge case.

```diff
diff --git a/web/src/components/app-metrics.test.tsx b/web/src/components/app-metrics.test.tsx
index de585ba..02eb10d 100644
--- a/web/src/components/app-metrics.test.tsx
+++ b/web/src/components/app-metrics.test.tsx
@@ -10,6 +10,12 @@ const refresh = vi.fn(async () => {});
 let data: Series | null = null;
 let loading = false;
 
+// The Alerts card has its own tests; here it only has to be placed.
+vi.mock("@/components/alerts/target-alerts-card", () => ({
+  TargetAlertsCard: (p: { serverId: string; appId?: string }) => (
+    <div data-testid="alerts-card">{`${p.serverId}/${p.appId ?? ""}`}</div>
+  ),
+}));
 vi.mock("@/hooks/use-metrics", () => ({
   useAppMetrics: (...args: unknown[]) => {
     calls.push(args);
@@ -96,6 +102,7 @@ describe("AppMetricsTab", () => {
   it("charts the app over the chosen range", async () => {
     render(<AppMetricsTab serverId="s1" appId="a1" />);
     expect(calls.at(-1)).toEqual(["s1", "a1", "1h"]);
+    expect(screen.getByTestId("alerts-card")).toHaveTextContent("s1/a1");
     for (const title of ["CPU", "Memory", "Network", "Disk"]) {
       expect(screen.getByTestId(`chart-${title}`)).toBeInTheDocument();
     }
diff --git a/web/src/components/server-cards.test.tsx b/web/src/components/server-cards.test.tsx
index c4145be..7ed0533 100644
--- a/web/src/components/server-cards.test.tsx
+++ b/web/src/components/server-cards.test.tsx
@@ -90,6 +90,13 @@ describe("ServerCards", () => {
     expect(within(card).getByTestId("app-counts")).toHaveTextContent("3 running · 1 problematic");
   });
 
+  it("badges a server with firing alerts", () => {
+    overview[0].alerts_firing = 2;
+    renderCards();
+    expect(within(screen.getByTitle("prod-1: online")).getByTestId("firing-badge")).toHaveTextContent("2");
+    expect(within(screen.getByTitle("backup: offline")).queryByTestId("firing-badge")).not.toBeInTheDocument();
+  });
+
   it("says when a server is offline and has no data", () => {
     renderCards();
     const card = screen.getByTitle("backup: offline");
diff --git a/web/src/pages/monitoring.test.tsx b/web/src/pages/monitoring.test.tsx
index 969540b..9edfdc8 100644
--- a/web/src/pages/monitoring.test.tsx
+++ b/web/src/pages/monitoring.test.tsx
@@ -32,6 +32,12 @@ vi.mock("@/context/use-servers", () => ({
 vi.mock("@/context/use-apps", () => ({
   useApps: () => ({ apps: [{ id: "app-1", name: "Nextcloud" }, { id: "app-2-unnamed", name: "" }] }),
 }));
+// The Alerts card has its own tests; here it only has to be placed.
+vi.mock("@/components/alerts/target-alerts-card", () => ({
+  TargetAlertsCard: (p: { serverId: string; appId?: string }) => (
+    <div data-testid="alerts-card">{`${p.serverId}/${p.appId ?? ""}`}</div>
+  ),
+}));
 vi.mock("@/hooks/use-metrics", () => ({
   useServerMetrics: (...args: unknown[]) => {
     useServerMetrics(...args);
@@ -109,6 +115,7 @@ describe("MonitoringPage", { timeout: 20_000 }, () => {
       expect(screen.getByTestId(`chart-${title}`)).toBeInTheDocument();
     }
     expect(screen.getByTestId("docker-df")).toHaveTextContent("Images1.0 GB");
+    expect(screen.getByTestId("alerts-card")).toHaveTextContent("s1/");
     expect(screen.getByTestId("docker-df")).toHaveTextContent("Reclaimable512 B");
     // Rates were never reported: that chart says so instead of drawing zeros.
     expect(screen.getByTestId("chart-Network")).toHaveTextContent("No data yet");
```

Run: `pnpm --dir web exec vitest run src/components/alerts src/components/server-cards.test.tsx src/pages/monitoring.test.tsx src/components/app-metrics.test.tsx`
Expected: FAIL (missing modules; no `alerts-card`; no `firing-badge`).

- [ ] **Step 2: Write the components**

`web/src/components/alerts/active-alerts.tsx`:

```tsx
import { Link } from "react-router-dom";
import { BellRing, Flame } from "lucide-react";

import { Card } from "@/components/ui/card";
import { useActiveAlerts } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import { activeText, alertTargetPath, sinceText } from "@/lib/alerts";
import { cn } from "@/lib/utils";

// FiringBadge is the small red count of firing alerts on a server card or
// an app header. Nothing when there are none.
export function FiringBadge({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full bg-destructive/10 px-1.5 py-0.5 text-xs font-medium text-destructive",
        className,
      )}
      title={`${count} firing alert${count === 1 ? "" : "s"}`}
      data-testid="firing-badge"
    >
      <BellRing className="size-3" aria-hidden />
      {count}
    </span>
  );
}

// AppAlertsBadge is an app header's firing count, linking to its Metrics
// tab where the alerts card is.
export function AppAlertsBadge({ serverId, appId }: { serverId: string; appId: string }) {
  const { data } = useActiveAlerts(serverId, appId);
  if (data.length === 0) return null;
  return (
    <Link to="?tab=metrics" aria-label="Firing alerts">
      <FiringBadge count={data.length} />
    </Link>
  );
}

// MAX_STRIP is how many firing alerts the dashboard lists before "View all".
const MAX_STRIP = 5;

// ActiveAlertsStrip lists the org's firing alerts above the dashboard's
// server health. It renders nothing while all is quiet.
export function ActiveAlertsStrip() {
  const { data } = useActiveAlerts();
  const now = useNow();
  if (data.length === 0) return null;
  return (
    <Card className="gap-2 border-destructive/30 p-3.5" role="region" aria-label="Active alerts">
      <div className="flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-medium text-destructive">
          <Flame className="size-4" aria-hidden />
          {data.length} active alert{data.length === 1 ? "" : "s"}
        </h2>
        <Link to="/org/alerts" className="text-xs text-muted-foreground hover:text-foreground hover:underline">
          View history
        </Link>
      </div>
      <ul className="grid gap-1 text-sm">
        {data.slice(0, MAX_STRIP).map((a) => (
          <li key={`${a.rule_id}-${a.server_id}-${a.app_id}`} className="flex min-w-0 items-center gap-2">
            <Link to={alertTargetPath(a.server_id, a.app_id)} className="truncate font-medium hover:underline">
              {a.app_id ? `${a.server_name} / ${a.app_name || a.app_id}` : a.server_name}
            </Link>
            <span className="truncate text-muted-foreground">{activeText(a)}</span>
            <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">
              {sinceText(a.since, now)}
            </span>
          </li>
        ))}
      </ul>
      {data.length > MAX_STRIP ? (
        <p className="text-xs text-muted-foreground">and {data.length - MAX_STRIP} more</p>
      ) : null}
    </Card>
  );
}
```

`web/src/components/alerts/override-dialog.tsx`:

```tsx
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { alertsPost } from "@/hooks/use-alerts";
import { hasThreshold, metricInfo, routingText, ruleSummary, type Channel, type Override, type Rule } from "@/lib/alerts";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rule: Rule;
  override?: Override;
  channels: Channel[];
  serverId: string;
  appId: string;
  // targetLabel names the server or app ("prod-1", "nextcloud").
  targetLabel: string;
  onSaved: () => void;
};

type Notify = "inherit" | "specific" | "none";

// OverrideDialog adjusts one rule for one server or app: switch it off
// here, change its threshold or duration, or send elsewhere. Empty fields
// keep the rule's value; "Reset to rule" removes the override.
export function OverrideDialog({ open, onOpenChange, rule, override, channels, serverId, appId, targetLabel, onSaved }: Props) {
  const info = metricInfo(rule.target_kind, rule.metric);
  const [on, setOn] = useState(!override?.disabled);
  const [threshold, setThreshold] = useState(override?.threshold != null ? String(override.threshold) : "");
  const [minutes, setMinutes] = useState(override?.duration_minutes != null ? String(override.duration_minutes) : "");
  const initialNotify: Notify =
    override?.channel_ids == null ? "inherit" : override.channel_ids.length === 0 ? "none" : "specific";
  const [notify, setNotify] = useState<Notify>(initialNotify);
  const [picked, setPicked] = useState<string[]>(override?.channel_ids ?? []);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const target = { server_id: serverId, app_id: appId };

  const save = async () => {
    let t: number | null = null;
    if (hasThreshold(rule.metric) && threshold.trim() !== "") {
      t = Number(threshold);
      if (!Number.isFinite(t) || t <= 0 || (info!.max > 0 && t > info!.max)) {
        setError(info!.max > 0 ? `Threshold must be above 0 and at most ${info!.max}` : "Threshold must be above 0");
        return;
      }
    }
    let d: number | null = null;
    if (minutes.trim() !== "") {
      d = Number(minutes);
      if (!Number.isInteger(d) || d < 1 || d > 60) {
        setError("Duration must be 1 to 60 minutes");
        return;
      }
    }
    const channelIds = notify === "inherit" ? null : notify === "none" ? [] : picked;
    setBusy(true);
    try {
      await alertsPost("save-override", {
        rule_id: rule.id,
        ...target,
        disabled: !on,
        threshold: t,
        duration_minutes: d,
        channel_ids: channelIds,
      });
      toast.success("Override saved");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  const reset = async () => {
    setBusy(true);
    try {
      await alertsPost("delete-override", { rule_id: rule.id, ...target });
      toast.success("Back to the rule's settings");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Reset failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Adjust for {targetLabel}</DialogTitle>
          <DialogDescription>
            Rule: {ruleSummary(rule)}, {routingText(rule.channel_ids, channels).toLowerCase()}. Empty fields keep the
            rule&apos;s values.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <Label htmlFor="override-on">Alert on {targetLabel}</Label>
            <Switch id="override-on" checked={on} onCheckedChange={setOn} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            {hasThreshold(rule.metric) ? (
              <div className="space-y-1.5">
                <Label htmlFor="override-threshold">Above ({info?.unit})</Label>
                <Input
                  id="override-threshold"
                  type="number"
                  step="any"
                  placeholder={String(rule.threshold)}
                  value={threshold}
                  disabled={!on}
                  onChange={(e) => setThreshold(e.target.value)}
                />
              </div>
            ) : null}
            <div className="space-y-1.5">
              <Label htmlFor="override-minutes">For (minutes)</Label>
              <Input
                id="override-minutes"
                type="number"
                min={1}
                max={60}
                placeholder={String(rule.duration_minutes)}
                value={minutes}
                disabled={!on}
                onChange={(e) => setMinutes(e.target.value)}
              />
            </div>
          </div>
          <div className="space-y-2">
            <Label>Notify</Label>
            <RadioGroup value={notify} onValueChange={(v) => setNotify(v as Notify)} disabled={!on}>
              {(
                [
                  ["inherit", "Same as the rule"],
                  ["specific", "Specific channels"],
                  ["none", "History only"],
                ] as const
              ).map(([v, label]) => (
                <div key={v} className="flex items-center gap-2">
                  <RadioGroupItem value={v} id={`override-${v}`} />
                  <Label htmlFor={`override-${v}`} className="font-normal">
                    {label}
                  </Label>
                </div>
              ))}
            </RadioGroup>
            {notify === "specific" ? (
              <div className="grid gap-1.5 pl-6">
                {channels.map((c) => (
                  <div key={c.id} className="flex items-center gap-2">
                    <Checkbox
                      id={`override-ch-${c.id}`}
                      checked={picked.includes(c.id)}
                      onCheckedChange={(checked) =>
                        setPicked((p) => (checked ? [...p, c.id] : p.filter((id) => id !== c.id)))
                      }
                    />
                    <Label htmlFor={`override-ch-${c.id}`} className="font-normal">
                      {c.name}
                    </Label>
                  </div>
                ))}
              </div>
            ) : null}
          </div>
          {error ? (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          ) : null}
        </div>
        <DialogFooter className="gap-2 sm:justify-between">
          {override ? (
            <Button variant="outline" onClick={() => void reset()} disabled={busy}>
              Reset to rule
            </Button>
          ) : (
            <span />
          )}
          <Button onClick={() => void save()} disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

`web/src/components/alerts/target-alerts-card.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Flame, SlidersHorizontal } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { OverrideDialog } from "@/components/alerts/override-dialog";
import { alertsGet, useActiveAlerts } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import {
  activeText,
  conditionText,
  effective,
  hasThreshold,
  metricInfo,
  sinceText,
  type Channel,
  type Override,
  type Rule,
} from "@/lib/alerts";
import { cn } from "@/lib/utils";

type Props = {
  serverId: string;
  // appId targets one app; empty or absent targets the server itself.
  appId?: string;
  // targetLabel names the target in dialogs ("prod-1", "nextcloud").
  targetLabel: string;
};

// TargetAlertsCard is the Alerts card of a server's Monitoring page or an
// app's Metrics tab: what is firing here now, and every org rule that
// applies with its effective setting for this target, adjustable per
// target (overrides).
export function TargetAlertsCard({ serverId, appId = "", targetLabel }: Props) {
  const kind = appId ? "app" : "server";
  const now = useNow();
  const { data: active } = useActiveAlerts(serverId, appId || null);
  const firing = active.filter((a) => a.app_id === appId);
  const [rules, setRules] = useState<Rule[]>([]);
  const [overrides, setOverrides] = useState<Override[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [adjusting, setAdjusting] = useState<Rule | null>(null);

  // version bumps after an override changes, reloading the card.
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let active = true;
    const target: Record<string, string> = { server_id: serverId };
    if (appId) target.app_id = appId;
    Promise.all([
      alertsGet<Rule[] | null>("get-rules"),
      alertsGet<Override[] | null>("get-overrides", target),
      alertsGet<Channel[] | null>("get-channels"),
    ])
      .then(([r, o, c]) => {
        if (!active) return;
        setRules((r ?? []).filter((x) => x.target_kind === kind));
        setOverrides(o ?? []);
        setChannels(c ?? []);
        setError(null);
      })
      .catch((e: unknown) => {
        if (active) setError(e instanceof Error ? e.message : "Failed to load alerts");
      });
    return () => {
      active = false;
    };
  }, [serverId, appId, kind, version]);

  return (
    <Card data-slot="widget-card" className="flex min-w-0 flex-col gap-3 p-3.5" role="region" aria-label="Alerts">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-sm font-medium">Alerts</h2>
        <Link to="/org/alerts?tab=rules" className="text-xs text-muted-foreground hover:text-foreground hover:underline">
          Manage rules
        </Link>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {firing.length > 0 ? (
        <ul className="grid gap-1" aria-label="Firing">
          {firing.map((a) => (
            <li key={a.rule_id} className="flex items-center gap-2 text-sm text-destructive">
              <Flame className="size-4 shrink-0" aria-hidden />
              <span className="truncate font-medium">{activeText(a)}</span>
              <span className="ml-auto shrink-0 text-xs tabular-nums">for {sinceText(a.since, now)}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted-foreground">Nothing firing.</p>
      )}

      <ul className="grid gap-1.5" aria-label="Rules">
        {rules.map((r) => {
          const o = overrides.find((x) => x.rule_id === r.id);
          const eff = effective(r, o);
          const info = metricInfo(r.target_kind, r.metric);
          return (
            <li key={r.id} className="flex items-center gap-2 text-sm" data-testid="target-rule">
              <span className={cn("min-w-0 flex-1 truncate", !eff.enabled && "text-muted-foreground line-through")}>
                {hasThreshold(r.metric) ? (
                  <>
                    <span className="font-medium">{info?.label ?? r.metric}</span>{" "}
                    {conditionText(r.metric, info?.unit ?? "", eff.threshold, eff.duration_minutes)}
                  </>
                ) : (
                  <span className="font-medium">Down for {eff.duration_minutes} min</span>
                )}
              </span>
              {o ? <Badge variant="secondary">{o.disabled ? "Off here" : "Adjusted"}</Badge> : null}
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label={`Adjust ${info?.label ?? r.metric} for ${targetLabel}`}
                onClick={() => setAdjusting(r)}
              >
                <SlidersHorizontal className="size-3.5" />
              </Button>
            </li>
          );
        })}
        {rules.length === 0 && !error ? (
          <li className="text-sm text-muted-foreground">No {kind} rules in this organization.</li>
        ) : null}
      </ul>

      {adjusting ? (
        <OverrideDialog
          key={adjusting.id}
          open
          onOpenChange={(open) => !open && setAdjusting(null)}
          rule={adjusting}
          override={overrides.find((x) => x.rule_id === adjusting.id)}
          channels={channels}
          serverId={serverId}
          appId={appId}
          targetLabel={targetLabel}
          onSaved={() => setVersion((v) => v + 1)}
        />
      ) : null}
    </Card>
  );
}
```

`web/src/components/alerts/alert-toasts.tsx`:

```tsx
import { useEffect } from "react";
import { toast } from "sonner";

import { useNotifications } from "@/context/use-notifications";
import { ALERT_NOTIFICATION, type AlertNotice } from "@/lib/alerts";

// AlertToasts shows a toast for every alert transition pushed over SSE
// while the app is open: red when one fires, green when it resolves.
export function AlertToasts() {
  const { subscribe } = useNotifications();
  useEffect(
    () =>
      subscribe((n) => {
        if (n.type !== ALERT_NOTIFICATION || !n.payload) return;
        const notice = n.payload as AlertNotice;
        if (notice.kind === "fired") toast.error(notice.title);
        else toast.success(notice.title);
      }),
    [subscribe],
  );
  return null;
}
```

- [ ] **Step 3: Place them**

```diff
diff --git a/web/src/components/app-metrics.tsx b/web/src/components/app-metrics.tsx
index 4790382..e5243c1 100644
--- a/web/src/components/app-metrics.tsx
+++ b/web/src/components/app-metrics.tsx
@@ -4,6 +4,7 @@ import { DataFreshness } from "@/components/data-freshness";
 import { KpiTile, KpiTileSkeleton } from "@/components/kpi-tile";
 import { ChartCardSkeleton, MetricChartCard } from "@/components/metric-chart";
 import { RangeMenu } from "@/components/range-menu";
+import { TargetAlertsCard } from "@/components/alerts/target-alerts-card";
 import { useAppMetrics } from "@/hooks/use-metrics";
 import {
   chartColor,
@@ -113,7 +114,7 @@ export function AppMetricBlocks({ serverId, appId }: { serverId: string; appId:
 
 // AppMetricsTab is the app page's Metrics tab: CPU, memory, network and
 // disk growth over the selected range.
-export function AppMetricsTab({ serverId, appId }: { serverId: string; appId: string }) {
+export function AppMetricsTab({ serverId, appId, appName }: { serverId: string; appId: string; appName?: string }) {
   // 1h has data from the first minute (raw samples); see MonitoringPage.
   const [range, setRange] = useState<MetricsRange>("1h");
   const [mode, setMode] = useState<MetricsMode>("avg");
@@ -183,6 +184,7 @@ export function AppMetricsTab({ serverId, appId }: { serverId: string; appId: st
           />
         </div>
       )}
+      <TargetAlertsCard serverId={serverId} appId={appId} targetLabel={appName || "this app"} />
     </div>
   );
 }
diff --git a/web/src/components/server-cards.tsx b/web/src/components/server-cards.tsx
index 7b3f91d..f1b0ea3 100644
--- a/web/src/components/server-cards.tsx
+++ b/web/src/components/server-cards.tsx
@@ -5,6 +5,7 @@ import { Card } from "@/components/ui/card";
 import { useServers } from "@/context/use-servers";
 import type { Server } from "@/context/servers-context-base";
 import { useMetricsOverview } from "@/hooks/use-metrics";
+import { FiringBadge } from "@/components/alerts/active-alerts";
 import { useNow } from "@/hooks/use-now";
 import { TILE_GRID } from "@/lib/grid";
 import { latestValue, serverPresence, type Presence } from "@/lib/kpi";
@@ -131,7 +132,10 @@ function HealthCard({
       className="flex min-w-0 cursor-pointer flex-col gap-2 p-3.5 transition-colors outline-none hover:border-primary/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
     >
       <div className="flex items-center justify-between gap-3">
-        <div className="truncate text-base font-semibold tracking-tight">{server.name}</div>
+        <div className="flex min-w-0 items-center gap-2">
+          <div className="truncate text-base font-semibold tracking-tight">{server.name}</div>
+          <FiringBadge count={overview?.alerts_firing ?? 0} />
+        </div>
         <span
           className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground"
           title={presence.title}
diff --git a/web/src/layouts/app-layout.tsx b/web/src/layouts/app-layout.tsx
index 6a473bb..ab4edab 100644
--- a/web/src/layouts/app-layout.tsx
+++ b/web/src/layouts/app-layout.tsx
@@ -4,6 +4,7 @@ import { Navigate, Outlet, useLocation } from "react-router-dom"
 import { useProfile } from "@/context/use-profile"
 
 import { AppSidebar } from "@/components/app-sidebar"
+import { AlertToasts } from "@/components/alerts/alert-toasts"
 import { ServerSelectionDialog } from "@/components/server-creation-dialog"
 import { isStandalone } from "@/config"
 import { useServers } from "@/context/use-servers"
@@ -115,6 +116,7 @@ export function AppLayout() {
     <SidebarProvider>
       <AppSidebar onAddServer={handleAddServer} />
       <AppLayoutContent />
+      <AlertToasts />
       {!isStandalone ? (
         <ServerSelectionDialog
           isOpen={isServerDialogOpen}
diff --git a/web/src/lib/metrics.ts b/web/src/lib/metrics.ts
index 1d463b9..eeb77c7 100644
--- a/web/src/lib/metrics.ts
+++ b/web/src/lib/metrics.ts
@@ -68,6 +68,8 @@ export type ServerOverview = {
   latest: { at: string; server: ServerSample } | null;
   sparkline: MetricPoint[];
   apps: AppCounts;
+  // alerts_firing counts the server's (and its apps') firing alerts.
+  alerts_firing?: number;
 };
 
 // TEMP_PREFIX prefixes one sensor's field ("temp:Composite").
diff --git a/web/src/pages/app-details.tsx b/web/src/pages/app-details.tsx
index b2687d2..94303e7 100644
--- a/web/src/pages/app-details.tsx
+++ b/web/src/pages/app-details.tsx
@@ -36,6 +36,7 @@ import { LogsView, type LogLine } from "@/components/logs-view";
 import { AppEditorPanel } from "@/components/app-editor-panel";
 import { AppStatusIndicator } from "@/components/app-status-badge";
 import { AppMetricBlocks, AppMetricsTab } from "@/components/app-metrics";
+import { AppAlertsBadge } from "@/components/alerts/active-alerts";
 import { useApps } from "@/context/use-apps";
 import { useNotifications } from "@/context/use-notifications";
 import { useServers } from "@/context/use-servers";
@@ -164,6 +165,7 @@ export default function AppDetailsPage() {
           </h1>
           <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
             <AppStatusIndicator status={status} />
+            <AppAlertsBadge serverId={app.serverId} appId={app.id} />
             {app.version ? <span>Revision {app.version}</span> : null}
             {routes.slice(0, 2).map((d) => (
               <a
@@ -217,7 +219,7 @@ export default function AppDetailsPage() {
           <LogsTab appId={app.id} />
         </TabsContent>
         <TabsContent value="metrics" className="mt-6">
-          <AppMetricsTab serverId={app.serverId} appId={app.id} />
+          <AppMetricsTab serverId={app.serverId} appId={app.id} appName={appLabel(app.name, app.id)} />
         </TabsContent>
         <TabsContent value="editor" className="mt-0">
           <AppEditorPanel appId={app.id} />
diff --git a/web/src/pages/home.tsx b/web/src/pages/home.tsx
index b708a4f..e571dee 100644
--- a/web/src/pages/home.tsx
+++ b/web/src/pages/home.tsx
@@ -6,6 +6,7 @@ import { useAppBreadcrumbs } from "@/layouts/use-app-layout"
 import { Button } from "@/components/ui/button"
 import { ServerSelectionDialog } from "@/components/server-creation-dialog"
 import { ServerCards } from "@/components/server-cards"
+import { ActiveAlertsStrip } from "@/components/alerts/active-alerts"
 import { AppGrid } from "@/components/app-grid"
 import { isStandalone } from "@/config"
 import { useServers } from "@/context/use-servers"
@@ -35,6 +36,8 @@ export default function HomePage() {
         </Button>
       </div>
 
+      <ActiveAlertsStrip />
+
       {/* Server health at a glance: a compact strip above the apps. Adding
           servers is a cloud capability; the standalone build ships one
           embedded server, so the action is hidden. */}
diff --git a/web/src/pages/monitoring.tsx b/web/src/pages/monitoring.tsx
index 22ceb95..14c11ff 100644
--- a/web/src/pages/monitoring.tsx
+++ b/web/src/pages/monitoring.tsx
@@ -8,6 +8,7 @@ import { DataFreshness } from "@/components/data-freshness";
 import { KpiTile, KpiTileSkeleton } from "@/components/kpi-tile";
 import { ChartCardSkeleton, MetricChartCard, type ChartSeries } from "@/components/metric-chart";
 import { RangeMenu } from "@/components/range-menu";
+import { TargetAlertsCard } from "@/components/alerts/target-alerts-card";
 import { useApps } from "@/context/use-apps";
 import { useServers } from "@/context/use-servers";
 import { useServerPath } from "@/context/use-server-path";
@@ -354,6 +355,7 @@ export default function MonitoringPage() {
       )}
 
       <DockerDisk df={df} />
+      <TargetAlertsCard serverId={activeServerId} targetLabel={activeServer?.name ?? "this server"} />
     </div>
   );
 }
```

- [ ] **Step 4: Run the whole web suite, typecheck and lint**

Run: `pnpm --dir web exec vitest run && pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: 40 files / 244 tests pass (as prototyped); no type or lint errors.

- [ ] **Step 5: Commit**

```bash
git add web
git commit -m "feat(web): alert cards with overrides, badges, active-alerts strip and toasts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 14: Full verification, live smoke test, draft PR

**Files:** none new. This task runs the gates CI runs, exercises the feature on the local test instance, and opens the PR.

Prototype result for the whole plan, in a scratch worktree of `8497b38` with all thirteen tasks applied:
- `gofmt` clean; `go vet ./...`; `make sqlc-check`;
- `WF_CONTRACT_GUARD=require go test -race ./...` passes on SQLite;
- the contract suite and `internal/infra/db/...` pass on Postgres 16;
- `diffcov --min 80` against `main`: Go 85.5 %, web 91.7 %;
- the ratchet: Go 81.1 % (baseline 80.2), web 54.1 %;
- `vitest run`: 244 tests; `tsc -b`, `eslint .` and `vite build` all succeed.

- [ ] **Step 1: Run the CI gates**

```bash
mkdir -p $HOME/.cache/wf-gotmp && export GOTMPDIR=$HOME/.cache/wf-gotmp
test -z "$(gofmt -l $(git ls-files '*.go'))"
go vet ./... && make sqlc-check
make openapi && git diff --exit-code api/openapi.yaml
WF_CONTRACT_GUARD=require make test
make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out'
tail -n +2 contract-postgres.out >> coverage.out
make web-test && pnpm --dir web run lint && pnpm --dir web run build && touch web/dist/.gitkeep
go run ./tools/diffcov --base main --min 80 --go coverage.out --lcov web/coverage/lcov.info
go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info
bash scripts/install_test.sh
git status --short   # only coverage files and web/dist build output; commit nothing from them
```

Expected: every command exits 0. If `diffcov` reports a changed file below 80 %, add the missing test to the task that owns the file. Do not lower the threshold.

- [ ] **Step 2: Live smoke test on the local standalone instance**

The test instance:
- URL `http://localhost:18090`;
- login `admin@example.com` / `Winterflow-Test-2026!`;
- env file `/tmp/wf-mon/env`;
- personal access token in `/tmp/wf-mon/tok`.

Standalone allows private targets, so a local webhook catcher works.

```bash
make build
pkill -f "bin/standalone serve -env-file /tmp/wf-mon/env"; sleep 2
setsid nohup bin/standalone serve -env-file /tmp/wf-mon/env > /tmp/wf-mon/out.log 2>&1 &
cat > /tmp/wf-mon/hook.py <<'EOF'
import http.server, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        print(body.decode(), flush=True)
        self.send_response(200); self.end_headers()
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", 18999), H).serve_forever()
EOF
setsid nohup python3 /tmp/wf-mon/hook.py > /tmp/wf-mon/hook.log 2>&1 &
TOK=$(cat /tmp/wf-mon/tok); API=http://localhost:18090/api/v1/alerts
curl -s -H "Authorization: Bearer $TOK" $API/get-rules | head -c 400; echo   # 5 default rules
curl -s -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' $API/save-channel \
  -d '{"name":"Local hook","url":"generic://127.0.0.1:18999/hook?template=json&disabletls=yes","fields":{"ui_service":"webhook"},"is_default":true,"enabled":true}'; echo
CH=$(curl -s -H "Authorization: Bearer $TOK" $API/get-channels | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"][0]["id"])')
curl -s -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' $API/test-channel -d "{\"id\":\"$CH\"}"; echo
cat /tmp/wf-mon/hook.log    # {"message":"This is a test notification…","title":"🔔 Winterflow test"}
curl -s -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' $API/save-rule \
  -d '{"target_kind":"server","metric":"cpu","threshold":0.1,"duration_minutes":1,"channel_ids":null,"enabled":true}'; echo
```

Wait about two minutes: one sample and one evaluation. Then check:
- `curl -s -H "Authorization: Bearer $TOK" $API/get-active` lists the CPU alert;
- `/tmp/wf-mon/hook.log` holds a `🔥 … · CPU …% > 0% for 1m` title;
- `GET /api/v1/alerts/get-events` shows it fired with a delivery `ok: true`.

In the browser, check and screenshot:
1. the dashboard shows the red active-alerts strip and a firing badge on the server tile;
2. Monitoring has the Alerts card; **Adjust** on the CPU rule, set it "Off here", saves and shows "Off here";
3. Org → Alerts: History shows the event with "✓ Local hook", Rules lists the new rule, and Channels shows "Local hook" with **Send test**;
4. dark mode (OS setting) renders the same surfaces legibly.

Finally, delete the test rule (`delete-rule`) and the channel (`delete-channel`), then stop the hook catcher (`pkill -f /tmp/wf-mon/hook.py`).

- [ ] **Step 3: Push and open the draft PR**

```bash
git push -u origin feat/alerts
```

Open a draft PR against `main` titled `feat(alerts): alert rules, overrides and notification channels (monitoring PR 2)`. The body covers:
- **Summary:** spec §3 and the alert parts of §4.
- **Rulings:** decisions 1–18 of this plan, in short form. Highlight these:
  - transitions are compare-and-set, and safe with several API replicas;
  - migration `0012_alerts` collides with #12's `0011_server_storages`, and whichever merges second renumbers;
  - `ALERTS_ALLOW_PRIVATE_TARGETS`: the SSRF guard is on for the distributed API and off for standalone;
  - routes use the existing verb style;
  - the active list replaces "firing count in app responses";
  - defaults are seeded at startup, every 10 min, and on get-rules.
- **Dependencies:**
  - `github.com/nicholas-fedor/shoutrrr v0.18.0` (MIT; v0.19+ need Go 1.27);
  - it pulls `eclipse/paho.golang` (EPL-2.0 OR EDL-1.0, taken under the BSD-3-style EDL) and minor `golang.org/x/*` bumps;
  - binary size grows by the Shoutrrr service registry.
- **Test plan:** the gate results from Step 1 and the smoke checks from Step 2, with screenshots.
- The line `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

