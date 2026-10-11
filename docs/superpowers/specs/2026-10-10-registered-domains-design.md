# Registered domains and TLS modes — design

Date: 2026-10-10. Status: approved in conversation. This is sub-project **B** of
"winterflow as the runtime for twillingate cloud tenants" (A, volumes, is
merged). PR 5 stacks on PR 4 (roles removal), so there is no admin gating.

## Context

Today an app's ingress lists arbitrary exact hostnames, each with an `ssl`
toggle. Caddy, embedded in the agent, gets a Let's Encrypt cert per host
(HTTP-01). `app_domains.domain` is globally unique across all servers.
Wildcards are rejected, and there is no notion of a registered or base domain.

For twillingate, `*.twillingate.app` must work in any of three setups, without
making Cloudflare a dependency:

1. a Cloudflare Origin (or any) wildcard certificate behind the CF proxy;
2. an A record pointing at the agent, with winterflow managing certs;
3. a Cloudflare Tunnel running as a winterflow app, with TLS terminated by CF.

## Concepts

### Registration

A **registration** is a name plus a TLS mode, stored per server (agent).

- **Name:** an exact hostname (`twillingate.app`, `app.twillingate.app`), or a
  wildcard whose `*` is the whole leftmost label (`*.twillingate.app`,
  `*.eu.twillingate.app`). Names are lowercase and validated by the same
  RFC-1123 rule as ingress hostnames, plus the leading `*.`.
- **TLS mode:**

  | Mode | Meaning | Caddy behaviour |
  |---|---|---|
  | `auto` | winterflow manages certs | Each concrete app hostname under the registration is an ACME subject (one cert per host, HTTP-01). A wildcard in `auto` means "accept any single label here". |
  | `certificate` | an uploaded PEM cert + key | Loaded via `tls.certificates.load_files`. Caddy's automatic HTTPS skips names covered by a loaded cert, so there is no ACME traffic. Routes sit on the HTTPS server. That skip applies to *any* host the cert covers, so the cert also serves sibling `auto` hosts it matches (e.g. an `auto` exact name under a certificate-mode wildcard) — documented, not prevented. |
  | `off` | TLS terminated in front (tunnel or proxy) | Routes sit on the plain-HTTP server. Nothing listens on 443 for these names. |

### Coverage

- A hostname `h` is covered by an exact registration `n` iff `h == n`.
- It is covered by a wildcard `*.s` iff `h` is `<one label>.s`. The wildcard
  does not cover `s` itself, nor `a.b.s`.
- When several registrations cover `h`, an exact one wins over a wildcard. Two
  wildcards can't both cover the same host.

### The rule

- Every hostname an app puts in its ingress (a route domain or a domain-level
  redirect source) must be covered by a registration **on the same server**.
- Apps without ingress, reached on a published port, are unaffected.
- A hostname is unique **per server**: at most one app per agent claims it.
  The same hostname may be claimed by apps on different servers (DNS round
  robin).
- The same registration name may exist on any number of servers.
- The ingress `ssl` field is ignored from now on; TLS follows the registration.
  Old configs that carry it keep loading.

## Agent side

### Commands (`internal/domain/command/domain.go`)

```go
// internal/domain/model
type DomainTLS string // "auto" | "certificate" | "off"
type RegisteredDomain struct {
    Name     string     `json:"name"`
    TLS      DomainTLS  `json:"tls"`
    NotAfter *time.Time `json:"not_after,omitempty"` // certificate mode
    Issuer   string     `json:"issuer,omitempty"`    // certificate mode, CN of the issuer
}

// internal/domain/command
type Domain struct {
    model.RegisteredDomain
    Apps []string `json:"apps"` // app ids using a covered host on this agent
}
type ListDomainsRequest struct{}
type ListDomainsResponse struct{ Domains []Domain `json:"domains"` }
type CreateDomainRequest struct {
    Name        string    `json:"name"`
    TLS         model.DomainTLS `json:"tls"`
    Certificate string    `json:"certificate,omitempty"` // PEM chain, certificate mode
    Key         string    `json:"key,omitempty"`         // ECIES ciphertext of the PEM key, certificate mode
}
type CreateDomainResponse struct {
    Domain   Domain   `json:"domain"`
    Warnings []string `json:"warnings,omitempty"` // from the proxy reload
}
type DeleteDomainRequest struct{ Name string `json:"name"` }
type DeleteDomainResponse struct {
    Name     string   `json:"name"`
    Warnings []string `json:"warnings,omitempty"` // from the proxy reload
}
```

- Types are `domain.list`, `domain.create` and `domain.delete`.
- `Validate()` on each request checks the name, the mode, and that `Certificate`
  and `Key` are present iff the mode is `certificate`. It runs in the dispatcher
  hook (PR #4's pattern), with one dispatcher line per type.
- `domain.create` on an existing name replaces it, which is how a certificate is
  renewed. Certificate files are content-addressed (see Storage), so a new
  certificate changes the generated config and the ordinary reload loads it;
  no reload is ever forced.

### Storage (`{AGENT_DATA_DIR}/domains/`)

- `<file-safe name>.json` holds `{name, tls}`, mode 0644. A wildcard's `*` is
  encoded as `_wildcard_` in the filename.
- Certificate mode also writes the pair `<file>.<fp>.crt` (0644) and
  `<file>.<fp>.key` (0600), where `fp` is the first 12 hex digits of
  `sha256(cert || key)`; the `.json` additionally records `fingerprint`,
  `not_after` and `issuer` (read from the leaf once, at create time). The
  key is decrypted with the agent's private key at create time, and it never
  leaves the agent or appears in any response.
- **Renewal** writes the new pair next to the old one, then switches the
  `.json` to it (written last, atomically), then prunes the old pair. The
  `.json` therefore only ever names a complete pair, and the running proxy's
  files are never overwritten in place. Every file is fsynced before its
  rename and the directory after it, so a crash leaves the old or the new
  `.json`, plus at worst unreferenced pair or `.tmp` files, which the name's
  next create or delete removes. A registration whose pair is missing or
  does not load (`tls.LoadX509KeyPair`, e.g. a file damaged by the disk
  itself) is skipped when listed: its hosts fall out of ingress (fail
  closed), while every other registration keeps serving — Caddy would
  otherwise refuse its whole TLS app. Re-uploading the same pair rewrites a
  damaged one.
- **Create-time checks in certificate mode:**
  - `tls.X509KeyPair(cert, key)` must succeed;
  - the leaf certificate must cover the registered name (wildcard SAN for a
    wildcard name, exact or wildcard SAN for an exact name);
  - the leaf must not be expired.

  Each failure returns a clear error.
- **Delete** is refused while any app on the agent uses a hostname that this
  registration wins and no remaining registration would cover. A host another
  registration still covers (a seeded exact name next to a new wildcard) just
  moves to it. The error names the apps (by name; an app without one by its
  id). It fails closed: when apps-data or an app's config can't be read, the
  delete is refused, since it can't tell whether the name is in use.

### Upgrade path

- On first start with no `domains/` directory, the agent registers every
  hostname that existing apps use as an **exact** registration. The mode is
  `auto` if that host's ingress entry had `ssl: true`, and `off` otherwise.
- It then creates the directory, so this runs once. It is idempotent and
  logged.

### Caddy (`internal/infra/ingress/caddy`)

- `BuildConfig` takes the registrations in addition to the app fragments.
- **Per app hostname:** find the winning registration. If there is none, the
  host is excluded with a warning (fail closed). Otherwise its routes go to the
  HTTPS server (`auto`, `certificate`) or the HTTP server (`off`). Only `auto`
  hosts become ACME subjects.
- **Certificate mode:** add `tls.certificates.load_files` entries for every
  registration in that mode. Automatic HTTPS skips the names those certs cover.
  Verify this against real Caddy in `TestBuildConfigValidatesWithCaddy`, plus
  a test that no ACME policy lists such a host.
- **Catch-all:** after all app routes, every `off` and `certificate`
  registration gets a route matching its name (or `*.s`, minus the exact
  registrations under it) that answers **404** with a small text body: on the
  HTTP server for `off`, on the HTTPS server for `certificate` (the loaded
  cert completes the handshake). Unknown `xyz.twillingate.app` therefore gets
  a clean 404 under both. `auto` registrations get no catch-all: an unknown
  host there has no certificate — it is not an ACME subject — so the client
  sees a TLS handshake failure anyway, and no route ever names an unused
  `auto` name (so Caddy never tries to order a certificate for it). (A 410 for
  stopped apps is sub-project D.)
- The rate-limit route stays first on both servers. Read-header and idle
  timeouts are unchanged. It keys on Caddy's client IP: the TCP peer, unless
  that peer is in `INGRESS_TRUSTED_PROXIES` (CIDRs or IPs, default empty) —
  emitted as Caddy's own `trusted_proxies` (`static` source) with
  `client_ip_headers` `CF-Connecting-IP`, `X-Forwarded-For` and
  `trusted_proxies_strict` on both servers. Then the client is read from
  those headers right to left, skipping trusted hops (else the peer). Invalid
  entries are dropped with a warning (never trusted). Only proxies that
  overwrite `CF-Connecting-IP` belong in the range.
- **Reload triggers:** `app.save`, `app.delete`, `app.rename`, `rollback`
  (all as today), plus `domain.create` and `domain.delete`.

### Ingress disabled

When `INGRESS_ENABLED` is false on the agent, `domain.create` and
`domain.delete` return an error ("ingress is disabled on this server").
`domain.list` returns the stored list.

## API side

### Index

- A new table `server_domains(server_id, name, tls, not_after, issuer, updated_at)`,
  with primary key `(server_id, name)`, a foreign key to servers with cascade,
  and an index on `name`.
- It is refreshed from command results (create and delete) and from a
  `domain.list` reconcile, triggered when the Domains tab loads and when the
  server's agent comes online (an unknown→online transition, or a
  capabilities event: the agent registered). That system sync is dispatched
  for no user (its result reaches no SSE stream); a reconcile whose rows equal
  the stored ones writes nothing.
- `app_domains`: the primary key changes from `(domain)` to `(server_id, domain)`
  (migration on both dialects). `FindClaims` becomes per server.

### Routes

All routes go through `caller` / `RequireServerAccess`.

- `GET /api/v1/domain/list?server_id=` is agent-bound: 202, with the result over
  SSE.
- `GET /api/v1/domain/get-domains?server_id=` answers synchronously from the
  index: `{domains: [{name, tls, not_after?, issuer?}]}` (no app usage; that
  needs the agent).
- `POST /api/v1/domain/create?server_id=` with body `CreateDomainRequest`.
- `POST /api/v1/domain/delete?server_id=` with body `{name}`.
- `GET /api/v1/domains/check?server_id=&domain=&app_id=` gains `server_id`
  (required) and answers `{available, covered, registration?, claims[]}`. The
  hostname's syntax is checked before any DB query (live typing sends many
  partial names); ownership is still checked before anything about claims or
  registrations is read. A claim is `{domain, app_id, app_name, server_id}`
  (claims are per server, so the server's name is not repeated).

### Save-app checks (synchronous, 400)

`save-app` checks each ingress hostname against the index for that server:

- **Covered:** a registration covers the host. Otherwise: "`<host>` is not
  covered by a domain registered on this server — register `<host>` or
  `*.<parent>`".
- **Unique per server:** no other app on the same server claims it.
- Hostnames the app already holds (per `app_domains`) are grandfathered when
  uncovered: right after an upgrade the index may still be empty until the
  agent's first `domain.list`. They are looked up (one per-app query) only
  once a host turns out uncovered.

The agent enforces coverage again when it builds the Caddy config.

### OpenAPI and contract suite

- The new routes go into the route table, with probes added to the matrix
  (malformed and valid for the server-addressed checks) and contract tests per
  route: happy path, an agent error, and the coverage and uniqueness errors on
  save-app.
- `api/openapi.yaml` is regenerated. The parity scenario stays as is.

## Web UI

### Domains tab

A **Domains** tab in Server Settings, shown when the server reports the
`ingress` feature (same as today's domain editor).

- **List:** name (monospace, wildcards visible), a mode badge
  (`Auto TLS` / `Certificate` / `TLS off`), cert expiry for certificate mode
  (warning style under 30 days, destructive style when expired), and "used by
  N apps" linking to them.
- **Add dialog:**
  - Name.
  - Mode as a radio group, with one-line explanations, including the
    Cloudflare examples: Origin cert = certificate, Tunnel = off,
    A record = auto.
  - In certificate mode, textareas for the certificate PEM and the key PEM. The
    key is encrypted in the browser with the server's public key, the same flow
    as registry passwords.
  - In certificate mode, a hint that the certificate also serves the Auto TLS
    hostnames it matches.
  - Every row's edit action reuses the dialog for an existing name (switch the
    mode, or replace the certificate).
  - A create/delete whose proxy reload reported problems shows them in a
    warning toast.
- **Delete:** a confirm dialog. The refusal (a host only this name covers) is
  shown with the app names.

### App ingress editor

- The hostname input offers suggestions from the server's registrations.
  Picking `*.twillingate.app` pre-fills `.twillingate.app` and focuses the
  label.
- Errors show inline from `domains/check`: "not covered" with a link to the
  Domains tab, or "taken by `<app>`".
- The per-host SSL switch becomes a read-only mode badge.

### Tests

Vitest covers the coverage-matching helper (a TS mirror of the Go rule, used
for suggestions and inline hints), the Domains card, and the editor's
suggestion behaviour. The 80% diff coverage applies.

## Testing (Go)

- **Coverage matching:** a table test of exact versus wildcard, one label
  only, exact wins, and case.
- **Name and mode validation;** certificate checks: mismatched key, SAN not
  covering, expired, a wildcard cert for a wildcard name, and an exact name
  under a wildcard cert.
- **`BuildConfig` per mode:**
  - routes land on the right server;
  - ACME subjects only for `auto`;
  - the `load_files` JSON;
  - the catch-all after app routes;
  - uncovered host excluded with a warning;
  - rate limit first;
  - real-Caddy validation.
- **Upgrade path:** existing apps produce exact registrations with the right
  modes, and the second start is a no-op.
- **Repositories:** per-server uniqueness and the `app_domains` PK migration on
  SQLite and Postgres (the contract suite runs both).
- **Contract suite:** the new routes, the save-app 400s, and the foreign-org
  403s via probes.

## Out of scope

- DNS provider APIs (DNS-01 wildcard issuance) and an automatic Cloudflare
  integration.
- Per-registration custom error pages and 410 for stopped apps (D).
- Subdomain allocation by winterflow (the portal picks names).
- Templates (D) and backups (C).
