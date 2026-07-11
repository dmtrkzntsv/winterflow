# Caddy Ingress — Embedded Reverse Proxy in the Agent

**Date:** 2026-07-11
**Status:** Approved design

## Summary

Embed Caddy v2 as a Go library in the agent runtime so that apps are fully
configurable from the Winterflow UI: per-app domains mapped to published host
ports, a per-domain Let's Encrypt toggle, and a per-app list of redirects
(domain-level and path rules). Ingress config lives in the app's committed
`config.json` (agent filesystem stays the source of truth, versioned and
rolled back with the app) and is mirrored into an API-side `app_domains` DB
table used purely as a rebuildable index for fast UI listing and central
cross-app/cross-server domain-conflict validation.

## Goals

- Configure domains, upstream ports, SSL, and redirects per app in the UI.
- Automatic HTTPS via ACME (Let's Encrypt) when the per-domain SSL toggle is
  on; plain HTTP on port 80 when off.
- Works in both topologies: `cmd/agent` (distributed) and `cmd/standalone`
  (in-process agent).
- Ingress problems degrade ingress only — never running apps, never the
  agent, and one app's bad ingress never breaks another app's domains.

## Non-goals (v1)

- Wildcard certificates (requires DNS-01).
- User-supplied certificates.
- Serving the Winterflow UI itself through Caddy.
- Per-domain certificate status reporting in the UI (future work; v1 relies
  on Caddy's background retry + agent logs).
- Caddy as an external managed process (explicitly rejected: the requirement
  is library embedding; avoids runtime dependency, version skew, lifecycle
  babysitting).

## Requirements decisions (settled during brainstorming)

1. **Routing:** Caddy reaches apps via published host ports — the compose
   file publishes a port (ideally bound to `127.0.0.1`), the UI maps
   domain → host port, Caddy proxies to `127.0.0.1:<port>`. No
   container-IP tracking.
2. **SSL toggle off = plain HTTP only** (port 80, no cert, no 443 route).
   On = ACME cert + automatic HTTP→HTTPS redirect.
3. **Redirects support both domain-level and path rules.**
4. **Both topologies** embed Caddy wherever the agent runtime lives.
5. **Storage:** AppConfig (agent git repo) is authoritative; the DB copy is
   a denormalized, rebuildable index.

## Data model

### AppConfig `ingress` section

Optional key in the committed `config.json` — versioned, rolled back, and
diffed with the app like everything else:

```jsonc
"ingress": {
  "domains": [
    { "id": "dom-1", "domain": "blog.example.com", "upstream_port": 8088, "ssl": true }
  ],
  "redirects": [
    // domain-level: preserves the request path ({uri}) on the target
    { "id": "red-1", "domain": "www.example.com", "to": "https://blog.example.com", "code": 301, "ssl": true },
    // path rule: scoped to one of the app's own domains; redirects the
    // matched prefix to the exact target URL
    { "id": "red-2", "domain": "blog.example.com", "path": "/old-blog/*", "to": "https://blog.example.com/blog", "code": 302 }
  ]
}
```

- Domain-level redirect entries carry their own `ssl` toggle: for
  `https://www.example.com` to redirect at all, Caddy must hold a cert for
  it — cert-wise a redirect source is just like a route domain.
- Path rules have no `ssl`: their `domain` must be one of the app's route
  domains or domain-level redirect sources, which already determines TLS.
  Semantics: prefix match, redirect to the exact `to` URL. Domain-level
  (no `path`): preserve the full request URI.
- `code` ∈ {301, 302, 307, 308}.

### Go types

`internal/domain/model`: `model.Ingress`, `model.IngressDomain`,
`model.IngressRedirect`, plus `Validate()`:

- `domain` is a bare lowercase RFC-1123 hostname — no scheme, port,
  wildcard, or trailing dot.
- `upstream_port` ∈ [1, 65535].
- `to` parses as an absolute http(s) URL.
- Path rules must reference a domain the app itself claims.
- No duplicate domains within the app.

Strict typing is the injection defense: user strings become typed JSON
fields in Caddy config structs, never templated text.

The config blob stays a blob on the wire (`AppPayload.Config`). The API
parses the `ingress` key for validation/indexing but ships the blob onward
untouched; the agent is the parser of record for deployment. A missing
`ingress` key means "no ingress" and skips every code path (old clients and
pre-feature apps keep working).

### DB index table `app_domains`

Bun model in `internal/infra/db/models/models.go`; migration
`20260711000001_app_domains.go` following the registered-migration pattern.

```go
type AppDomain struct {
    bun.BaseModel `bun:"table:app_domains"`

    // Domain is the PK: lowercased FQDN. PK = the global uniqueness
    // constraint that makes cross-app/cross-server conflicts impossible
    // to persist.
    Domain       string         `bun:"domain,pk" json:"domain"`
    AppID        string         `bun:"app_id,notnull,type:char(36)" json:"app_id"`
    ServerID     string         `bun:"server_id,notnull,type:char(36)" json:"server_id"`
    Kind         string         `bun:"kind,notnull" json:"kind"` // "route" | "redirect"
    SSL          bool           `bun:"ssl,notnull" json:"ssl"`
    UpstreamPort int            `bun:"upstream_port,notnull,default:0" json:"upstream_port"` // routes only
    Target       string         `bun:"target,notnull,default:''" json:"target"`              // redirects only
    Code         int            `bun:"code,notnull,default:0" json:"code"`                   // redirects only
    UpdatedAt    types.DateTime `bun:"updated_at,notnull" json:"updated_at"`

    App    *App    `bun:"rel:belongs-to,join:app_id=app_id"`
    Server *Server `bun:"rel:belongs-to,join:server_id=server_id"`
}
```

- Secondary index on `app_id` (fetch/delete an app's rows).
- Path rules get no rows — they cannot conflict across apps (they live
  under a domain that already has a row); the UI reads them from `app.get`.
- Domain uniqueness is global per hub deployment (one ingress namespace
  across orgs): prevents two apps fighting over ACME for one hostname.

## Agent side

### Placement

`port.IngressManager` interface in `internal/domain/port`; Caddy
implementation in `internal/infra/ingress/caddy`. The dispatcher and both
entrypoints depend on the port, not on Caddy.

### Lifecycle

- Wired in bootstrap alongside the orchestrator (`cmd/agent`,
  `cmd/standalone`). On start: scan `{AGENT_DATA_DIR}/apps-data/*/`, parse
  each committed `config.json`'s `ingress` key, build one merged Caddy JSON
  config, `caddy.Run(cfg)`. ACME work is async — a domain whose DNS isn't
  pointed yet retries in the background without blocking.
- **Reload trigger:** the dispatcher calls `ingress.Reload()` after any
  command that mutates apps-data (`app.save`, `app.delete`, `app.rollback`,
  `app.rename`) — not inside the orchestrator, which stays focused on
  compose. `Reload()` rebuilds from disk and hot-swaps via `caddy.Load()`
  (zero-downtime). Reloads are mutex-serialized; no debouncing.

### Per-app failure isolation

- The merged config is built from per-app fragments, each validated
  independently with `model.Ingress.Validate()` (agent-side defense against
  offline-edited files). An invalid fragment excludes that app from the
  merge with a warning (logged + surfaced in `apps.status`); every other
  app's domains load normally.
- One unparseable `config.json` at startup skips that app's ingress, not
  the scan.
- Backstop: if the merged `caddy.Load()` still fails, the previous config
  keeps serving; the triggering command succeeds with an ingress warning.

### Generated Caddy config

Native JSON config structs (no Caddyfile generation), admin API disabled.

- One HTTP server on `:80`/`:443`, configurable via `INGRESS_HTTP_PORT` /
  `INGRESS_HTTPS_PORT` (tests; hosts where something else owns 80).
- Cert/ACME state in `{AGENT_DATA_DIR}/caddy/` (file_system storage) so
  backups capture certs.
- Route domain → host matcher + `reverse_proxy` to
  `127.0.0.1:<upstream_port>`.
- Domain-level redirect → host matcher + `static_response` with
  `Location: <to>{http.request.uri}` and the chosen code.
- Path rule → host+path matcher + `static_response` to the exact target,
  ordered before its domain's route.
- TLS: one automation policy whose `subjects` = exactly the `ssl: true`
  domains (explicit, never on-demand). `ssl: false` domains listed in
  `automatic_https.skip`, served on :80 only. ACME email from optional
  `INGRESS_ACME_EMAIL`.
- **Logging:** map winterflow `LOG_LEVEL` to the zap level in the config's
  `logging` section (`error`→ERROR, `warn`→WARN, `info`→INFO,
  `debug`→DEBUG); structured JSON to stderr with `service: "caddy"` so
  lines sit consistently next to the agent's own output. HTTP access logs
  enabled only at debug level.
- **Merge dedup:** if two local apps claim one domain (only possible via
  offline edits — the API blocks it upstream), apps are processed in
  sorted-appID order, first claim wins, loser logged + warned in
  `apps.status`.
- **Module imports are curated**, not `modules/standard`: `caddyhttp` +
  `reverseproxy` + `caddytls`/ACME cover everything above without the full
  standard-distro binary growth.

### Capabilities / deployment

- Agent advertises `ingress: true` in its features map; UI gates on it.
- Binding 80/443 needs root or `CAP_NET_BIND_SERVICE`
  (`AmbientCapabilities=CAP_NET_BIND_SERVICE` documented for systemd). If
  the bind fails, the agent logs it, advertises `ingress: false`, and runs
  on without ingress rather than crashing.

## API side

### Save flow (`internal/domain/usecase/app`, before the bus publish)

1. Parse `ingress` from the incoming config blob; missing section = skip.
2. `model.Ingress.Validate()` → 400 with field-level messages.
3. Conflict check via new `port.AppDomainRepository.FindClaims(ctx,
   domains, excludeAppID)` → 400 like
   `blog.example.com is already used by app "Ghost" on server "hetzner-1"`.
   Both failures are synchronous HTTP errors; the request never reaches
   the bus.
4. On the success callback (agent confirmed): `ReplaceForApp(appID, rows)`
   — `DELETE WHERE app_id` + bulk insert, one transaction.

### Other write paths

- `app.delete` success → `DeleteForApp`.
- `app.rollback` success → follow-up `app.get`, then `ReplaceForApp` from
  the restored config (rollback restores routing).
- `apps.list` reconcile → `ReplaceForServer` from a new per-app ingress
  summary added to the agent's `apps.list` response (the agent reads
  `config.json` when listing anyway). This makes the table rebuildable:
  DB loss, offline agent edits, and pre-feature apps heal on reconcile.

### Read surface

- The DB-backed apps listing joins `app_domains` so app cards show domains
  without an agent round-trip.
- New endpoint `GET /api/v1/domains/check?domain=…&app_id=…` running the
  same `FindClaims` — the editor flags a taken domain while typing.
- Full ingress config (including path rules) comes from the existing
  `app.get`.

### Transport & RBAC

- No new bus commands. The only wire changes: `apps.list` response gains
  the ingress summary; `SaveAppResponse` gains `Warnings []string`.
- RBAC unchanged: ingress rides the app lifecycle (member-accessible).

### Known race (accepted for v1)

Two concurrent saves claiming the same domain can both pass the pre-check;
the PK constraint stops the second upsert, that save surfaces an error
notification, reconcile keeps the table honest, and the agent-side merge
dedup keeps Caddy consistent.

## Web UI

- **Types** (`web/src/types/app-config.ts`): `AppConfig.ingress?:
  { domains: IngressDomain[]; redirects: IngressRedirect[] }`, mirroring
  the Go model, ids via `localId()`. `AppEditorState` unchanged (no
  content maps, nothing encrypted).
- **New `ingress-editor.tsx`**, a "Domains & Routing" card in the existing
  editor card stack, two add-row/remove-row lists:
  - Domain rows: domain input · upstream-port input · SSL switch
    ("HTTPS via Let's Encrypt") · hint that the compose file must publish
    the port on `127.0.0.1`.
  - Redirect rows: domain input · optional path input (empty = whole
    domain) · target URL input · code select (301/302/307/308) · SSL
    switch shown only while path is empty.
- **Validation:** local on blur (same rules as `Validate()`), remote
  debounced against `/domains/check` with the owning app + server named
  inline.
- **Gating:** card renders only when the server's features include
  `ingress: true`; otherwise a muted "agent doesn't support ingress" note.
- **App cards/details:** domain chips from the DB-joined listing, each a
  link (`https://`/`http://` per SSL flag).
- Strings through `web/src/locales`.

## Error handling

| Failure | Behavior |
|---|---|
| Invalid ingress / domain conflict at save | Synchronous 400, field-level; nothing hits the bus |
| One app's ingress fragment invalid | That app excluded from the merge (warning in logs + `apps.status`); all other apps' domains unaffected |
| Caddy config rebuild fails after deploy | Deploy succeeds; previous config keeps serving; warning via `SaveAppResponse.Warnings` → notification → UI toast |
| ACME issuance fails (DNS not pointed, rate limit) | Caddy retries in background; v1 = agent logs only; per-domain cert status is future work |
| 80/443 bind fails at agent start | Log, advertise `ingress: false`, run on without ingress |
| Upstream port not answering | Caddy's stock 502; `apps.status` already shows the app is down |
| DB upsert fails after agent confirmed save | Error notification; next `apps.list` reconcile heals the index |

Guiding rule: ingress problems degrade ingress — never running apps, never
the agent, never other apps' domains.

## Testing

- `model.Ingress.Validate()`: table tests (hostname edge cases, unicode,
  trailing dot, wildcard rejection, port bounds, path-rule references,
  duplicates).
- Caddy config generation is a pure function (`[]appIngress → caddy
  JSON`) — golden tests: ssl/non-ssl mix, redirects with/without paths,
  cross-app dedup, invalid-fragment exclusion, empty config, log-level
  mapping.
- `IngressManager` integration test: real embedded Caddy on ephemeral
  ports, `ssl: false` only (no ACME in tests), `httptest` upstream —
  assert proxying, redirect codes + `Location`, path-rule precedence,
  hot reload swapping a route, invalid fragment isolation.
- Dispatcher tests: `Reload()` invoked after save/delete/rollback/rename,
  not after reads.
- API: conflict check + `ReplaceForApp`/`ReplaceForServer` repository
  tests on SQLite; save-usecase rejection tests.
- Web: `tsc -b` + ESLint (no JS test runner in repo; not introducing one).
- E2E sanity via the standalone binary: deploy an app with a non-SSL test
  domain against a local port and curl through Caddy.
