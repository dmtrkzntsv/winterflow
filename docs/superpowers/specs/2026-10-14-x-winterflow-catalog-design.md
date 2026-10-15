# x-winterflow as the native app format, and the template catalog

Date: 2026-10-14
Status: approved in brainstorming, pending spec review

## Goal

Two kinds of templates, side by side:

- **Org templates**: what `templates` is today, per org, editable, with revisions.
- **Catalog templates**: the apps in `dmtrkzntsv/winterflow-apps` (317 today), the same for every install, read-only.

Both use one format, the `x-winterflow` compose block the catalog already
uses. It becomes Winterflow's native format: the agent understands it in every
app, hand-written or installed from a template.

## Rulings (user, 2026-10-14)

1. Catalog templates are installed directly: an instance points at the catalog
   template, upgrades work like org templates. No copy-into-org.
2. The catalog reaches installs as a published, signed JSON feed fetched by the
   API. The apps repo stays private.
3. `x-winterflow` is the native format, understood by the agent for every app
   (not rendered away by the API).
4. Today's template `Body` format and its data are dropped. Nothing is
   migrated: Winterflow is not in production, so no backward compatibility
   anywhere in this work.
5. The agent applies `x-winterflow` through a generated
   `.winterflow/compose.winterflow.yml` passed as the last `-f`.
6. `http` expose entries bind `127.0.0.1` on an auto-allocated host port;
   `tcp`/`udp` entries bind `0.0.0.0` on an auto-allocated port the user can pin.
7. `pattern` is part of the format.
8. The `_*` reserved names (`_APP_ID`, ...) retire; the `WF_*` built-ins replace
   them.
9. Templates page: org templates first, catalog below. Global search (⌘K)
   shows templates.

## Non-goals

- Copying a catalog template into the org ("Customize").
- Automatic upgrades. Every install and upgrade is a user action.
- Release notes, stars, screenshots from the catalog in the UI.
- Caddy options per exposed port (paths, body size, auth).
- Typed parameters beyond `pattern`, an expression language, or per-variable
  defaults other than the compose body literal.

## The x-winterflow format

Every key is optional. Compose ignores `x-*` top-level keys, so a file stays
runnable with plain `docker compose up -d`.

```yaml
services:
  app:
    image: ghcr.io/example/notes:latest
    environment:
      # Public URL the browser uses, e.g. https://notes.example.com
      BASE_URL: "http://localhost:8080"
      # Secret used to sign sessions. Generate with: openssl rand -hex 32
      SESSION_SECRET: "changeme"
      LANGUAGE: "en"
      DATABASE_URL: "postgres://notes:notes@db:5432/notes"
    volumes:
      - data:/var/lib/notes
      - ./config.yml:/etc/notes/config.yml:ro
  db:
    image: postgres:16
    environment:
      POSTGRES_USER: notes
      POSTGRES_PASSWORD: notes
    volumes:
      - pg:/var/lib/postgresql/data
volumes:
  data:
  pg:

x-winterflow:
  expose:
    - container: app        # service name
      port: 8080            # container port
      protocol: http        # http (default) | tcp | udp
  variables:
    BASE_URL:       { required: true, pattern: "^https?://" }
    SESSION_SECRET: { required: true, encrypted: true }
    LANGUAGE:       { pattern: "^[a-z]{2}$", description: "Two-letter UI language" }
    DB_PASSWORD:    { encrypted: true }
  overrides:
    services:
      app:
        environment:
          DATABASE_URL: postgres://notes:${DB_PASSWORD}@db:5432/notes
      db:
        environment:
          POSTGRES_PASSWORD: ${DB_PASSWORD}
  files:
    - config.yml
  backups:
    - name: db
      type: files
      volume: pg
      every: 24h
```

### Keys

- `expose[]`: a container port published on the host.
  - `container` (string, required): a service in the body.
  - `port` (int, required, 1–65535): the container port.
  - `protocol`: `http` (default) can take hostnames through the ingress and
    binds `127.0.0.1`; `tcp`/`udp` never take hostnames and bind `0.0.0.0`.
  - An entry is identified as `<container>:<port>/<protocol>`; duplicates are
    invalid.
- `variables.<NAME>`: a user-configurable value. The agent overrides the
  `environment:` key `NAME` in every service that has it (map or list form).
  - `required` (bool): a deploy without a value is refused.
  - `encrypted` (bool): a secret, stored and sent like app secrets today.
  - `pattern` (string): RE2, at most 512 bytes, matched against the whole value.
  - `description` (string, at most 1000 bytes): falls back to the YAML comment
    directly above the first `environment:` key of that name.
  - The default is the body literal of the first service with the key; a
    variable with no body key (used only in `overrides`) has no default.
    Secrets never have a default (the catalog's `changeme` is a placeholder).
- `overrides`: a compose fragment (only `services` today) merged over the body,
  with `${VAR}` interpolated from the variables and `WF_*` built-ins. A list
  (`ports:`, `command:`) replaces the body's list.
- `files[]`: extra files the body bind-mounts, relative to the compose file. Only
  the catalog builder reads it (every app file is already in the app repo).
- `backups[]`: the backup recipe, unchanged from today
  (`internal/domain/backup/recipe`).

`volumes` is removed from the format. Compose already scopes named volumes per
app (see Volumes below).

### Rules

- Variable names match `^[A-Z][A-Z0-9_]*$`; `WF_*` and `COMPOSE_PROJECT_NAME`
  are reserved.
- A plain value may not contain `$` (compose interpolates env files). Secrets may.
- A default that fails its own `pattern` is invalid.
- A variable that no service environment and no `overrides` entry uses is a
  warning.
- A non-external top-level volume with a literal `name:` is an error in a
  template and a warning in an app: two installs would share one volume.
  Sharing on purpose uses `external: true`.
- `WF_*` built-ins may appear in `overrides` and anywhere compose interpolates.

### Volumes

Unchanged and needing no format support: the agent runs compose with
`--project-name wf-<appID>`, so `data:` becomes `wf-<appID>_data` with the label
`com.docker.compose.project=wf-<appID>`. The Volumes page, orphan detection,
delete-with-volumes (`sweepAppVolumes`) and backups (`volume: data`) all key off
that label or the compose key.

## Sub-project 1: the agent understands x-winterflow

### One parser

A new domain package `internal/domain/compose/xwinterflow` parses and validates
the whole block (expose, variables, overrides, files, backups). The backup
recipe parser in `internal/domain/backup/recipe` moves under it or is called by
it, so a compose file is parsed once. It is shared by the API (save-app 400s,
template validation, the catalog builder) and the agent. It reads the
effective compose files in compose's own order (`composeFiles`), git-sourced
apps included.

### compose.winterflow.yml

At every deploy, on the same path that writes `.winterflow/storage.env`, the
agent writes the gitignored `.winterflow/compose.winterflow.yml` and passes it as
the last `-f` (`composeArgsFor` and `run.sh`):

- for each declared variable with a value, for each service whose environment
  has the key: `services.<svc>.environment.<NAME>: ${NAME}`;
- for each expose entry: `services.<svc>.ports` gains
  `127.0.0.1:<host>:<port>` (`http`) or `<host>:<port>/<tcp|udp>` (raw);
- `overrides` copied in, lists written with compose's `!override` tag.

The file holds only `${VAR}` references, never values. Compose interpolates them
from the env files it already loads (`.env.shared .env.shared.secrets .env
.winterflow/storage.env .env.secrets`). A variable without a value emits nothing,
so the body literal stands. With no `x-winterflow` block, no file is written.

The cached `docker compose config` used by backups includes the new file.

### Variables

A declared variable's value is an ordinary app variable: plain in the committed
`.env`, secret in `secrets.json` (ECIES, `<encrypted>` keep placeholder). The
app's free-form variables keep working, and `${VAR}` in a hand-written body
keeps interpolating as today.

Save-app (API) answers 400 for a missing required value, a plain value failing
its pattern, or a plain value containing `$`. Secret values are ciphertext to
the API: the browser checks their pattern, and the agent checks it again after
decrypting at deploy, failing the deploy with the variable's name.

### Ports

- Allocation lives on the agent. A host port is allocated when an expose entry
  first deploys, from `EXPOSE_PORT_RANGE` (default `20000-29999`), and stored in
  the committed `.winterflow/config.json` under `ports`
  (`{"app:8080/http": {"host": 20013, "pinned": false}}`), so redeploys and
  rollback keep it.
- One server-wide allocation lock and an in-memory index of every app's
  allocations (built at start, pruned in `forgetApp`) prevent collisions
  between apps; a listen probe on the target address skips ports something else
  holds.
- A user may pin a port (`app.save` carries it). A pin that another app holds
  is a 400; one the probe finds busy fails the deploy naming the port.
- Entries removed from the compose lose their allocation on the next save.
- `app.get` returns each entry with its host port, bind address and pinned flag.

### Ingress

`IngressDomain` targets either an `http` expose entry (`expose:
"app:8080"`) or, for compose files that publish their own port,
`upstream_port` as today; exactly one is set. `BuildConfig` resolves an expose
target to `127.0.0.1:<allocated>`. Save-app answers 400 for an expose target that
is not an `http` entry of the app. The rate limit, timeouts and TLS by
registered domain are unchanged.

### Built-ins

The `WF_*` built-ins (`WF_APP_ID`, `WF_APP_SLUG`, `WF_SERVER_ID`, ...) are the
only reserved names; nothing reads `_*`.

## Sub-project 2: templates use x-winterflow

### Body

A template revision body is:

```json
{
  "files": { "compose.yml": "...", "config.yml": "..." },
  "summary": "...", "description": "...",
  "icon": "...", "color": "..."
}
```

The template's name stays on the template row. Parameters, exposed ports and
backups are derived from the compose's `x-winterflow` through the shared
parser; nothing about them is stored separately. Validation is the shared
`x-winterflow` validation plus: a compose file is required, the literal
volume `name:` rule is an error, and the body is at most 1 MiB.

Deleted: `template.Body`'s `Parameters`, `AppName`, `Ingress`, `Backups`, the
`Render` path that produced `.env` interpolation and ingress rows, the `PORT`
parameter convention and `instance.backup_defaults`.

### Instances

`create-instance` takes: `server_id`, `key` (idempotent as today: the same
request hash replays, another is 409, a deleted key stays retired), `template_id`,
`revision` (0 = latest), `values` (plain) and `secrets` (ECIES), an optional
`name` (default: the template name), optional `hosts`
(`[{host, expose: "app:8080"}]`, `http` entries only) and optional `backups`
(enabled, storage per recipe entry). It renders the `app.save` payload: the
revision's files, one app variable per given value, the ingress domains, the
backups overrides and the instance marker; then dispatches through `CheckSave` +
`DispatchSave` as today.

Update, upgrade, control, delete, unlink, get and list keep their routes and
semantics. An upgrade re-renders with the stored values; a newly required
variable without a value is `ErrNeedsValues` (skipped by Upgrade all). Backups a
new revision adds start off.

### Data

Migration `0016_templates_x_winterflow` (both engines) drops and recreates
`templates`, `template_revisions`, `template_instances` and clears
`template_upgrade` rollouts:

- `templates`: `id`, `source` (`org` | `catalog`), `org_id` (NULL for catalog),
  `slug` (catalog only), `name`, `latest_revision`, `withdrawn_at` (catalog only),
  timestamps. Unique `(org_id, name)` where `source = 'org'`; unique `(slug)`
  where `source = 'catalog'`.
- `template_revisions`: as today, plus `digest` (catalog only).
- `template_instances`: as today.

Every template read is scoped: an org sees its own templates plus every
catalog template. Save and delete on a catalog template answer 403.

### Rollouts

A `template_upgrade` rollout's scope key becomes `<org_id>:<template_id>`, so
two orgs upgrading the same catalog template don't block each other. Instance
selection stays "the org's `created` instances below the target revision".

## Sub-project 3: the catalog feed

### Builder

`cmd/catalog` in this repo: `go run ./cmd/catalog build --apps <dir> --out <dir>
--base-url <url> [--sign-key-env NAME]`.

- Reads each `apps/<slug>/metadata.json`, `compose/compose.yml` and the files in
  `x-winterflow.files`; skips `waiting_list: true`.
- Validates each entry with the template validation; any failure fails the build
  listing every bad app.
- Writes `catalog.json`, `catalog.json.sig` (ed25519 over the exact bytes) and
  `logos/<slug>.<ext>` copied from `assets/`.

```json
{
  "format": 1,
  "source_commit": "49bf28d...",
  "generated_at": "2026-10-14T20:05:43Z",
  "apps": [{
    "slug": "actual-budget",
    "name": "Actual Budget",
    "summary": "...", "description": "...",
    "keywords": [], "tags": [], "alternative_to": [],
    "license": "MIT",
    "homepage_url": "...", "repository_url": "...", "documentation_url": "...",
    "logo_url": "https://winterflow.io/catalog/logos/actual-budget.png",
    "files": { "compose.yml": "..." },
    "digest": "sha256:..."
  }]
}
```

`digest` is the sha256 over the sorted file names and contents.

### Publishing

The `winterflow.io` deploy (which already checks out `winterflow-apps`) runs the
builder with the signing key from a secret and serves the output under
`https://winterflow.io/catalog/`. `winterflow-apps` CI runs the builder without
signing on every PR as its validation.

### Sync (API)

One goroutine per API process, started from bootstrap next to the rollout
worker:

- runs a minute after start, then every `CATALOG_REFRESH` (default `24h`);
- GET `CATALOG_URL` and the `.sig` with `If-None-Match`, a 30 s deadline, an
  8 MiB body cap, https only (http allowed for loopback, for tests);
- rejects the whole feed when the signature does not verify against
  `CATALOG_PUBLIC_KEY` (default compiled in) or `format` is unknown;
- validates each entry; an invalid entry is logged and skipped;
- per slug, in one transaction: insert the catalog template, or add a revision
  when `digest` differs from the latest revision's, updating name/metadata in
  place; clear `withdrawn_at` if set;
- catalog templates missing from the feed get `withdrawn_at`: hidden from the
  gallery and search, instances unaffected, no new installs;
- concurrent replicas are harmless: the loser of a revision insert hits the
  `(template_id, revision)` key and skips.

`CATALOG_URL` empty disables the sync and hides the catalog.

### Using it

Every org sees every non-withdrawn catalog template. Installing one creates an
instance in the caller's org. When a newer catalog revision lands, the org's
instances read "upgrade available"; Upgrade all queues the per-org rollout. Image
updates (floating tags) stay on the app's existing Update action.

### winterflow-apps changes (a PR in that repo)

- `json-schema.winterflow-compose.json`: `pattern` and `description` on
  variables, `protocol: http|tcp|udp` (default http), `volumes` removed,
  `backups` added.
- Every compose: the `volumes:` block of `x-winterflow` removed, `_APP_ID` and
  other `_*` references replaced by `WF_*`, raw-port expose entries marked
  `tcp`/`udp`.
- README, the `app-compose` skill and CI updated; CI runs the builder.

### winterflow.io changes (a PR in that repo)

- The deploy workflow runs `cmd/catalog build` (Winterflow checked out at a
  pinned ref) with the signing key secret and publishes `catalog/` with the site.
- A new ed25519 key pair: the private key as that repo's secret, the public key
  compiled into Winterflow.

## Sub-project 4: UI

### App editor (ships with sub-project 1)

- **Variables**: declared variables first as a form (description, body default
  as placeholder, required marker, pattern checked as you type, secrets masked
  with the keep placeholder); free-form variables below.
- **Ports**: each expose entry with its host port, a `localhost` or `LAN` badge,
  a copyable `server:port` for raw entries, and "Pin port".
- **Ingress**: a hostname picks an `http` expose entry from a list, or a port for
  apps without expose.

### New app

"From a template" opens one picker: **your org's templates** first, then the
**catalog** as a grid (logo, name, summary). One search box filters both over
name, summary, keywords and alternative-to. Picking one opens the install flow:
header (logo, description, license, homepage), app name, the variables form
(the app editor's component), a hostname per `http` expose entry when the server
has registered domains, backup storages, Install. On phones the picker is a
full-screen sheet.

### Templates page

One page, no tabs: the org's templates first (list, editor, revisions,
instances as today), then **Catalog**: entries this org has instances of first
(upgrade available, Upgrade all), then the searchable grid. A catalog template's
page is read-only: compose viewer, revisions, this org's instances, Upgrade all,
and a "Withdrawn from the catalog" note when withdrawn.

The org template editor becomes a files editor plus metadata (name, summary,
icon, color) with a read-only **Detected** panel derived live from
`x-winterflow`: parameters, exposed ports, backups, warnings. "Save as template"
from an app copies its files.

### Global search

The ⌘K palette gets a **Templates** group: org templates always, catalog
templates once a query is typed. Keywords and alternative-to are passed as
search keywords. Selecting one opens the install flow on the active server.

### Instances

An instance of a catalog template shows its logo and "upgrade available" when a
newer revision exists.

## Configuration

New env vars, documented in `.env.example`:

| Variable | Default | Read by | Purpose |
|---|---|---|---|
| `EXPOSE_PORT_RANGE` | `20000-29999` | agent, standalone | host ports for expose entries |
| `CATALOG_URL` | `https://winterflow.io/catalog/catalog.json` | api, standalone | the feed; empty disables the catalog |
| `CATALOG_PUBLIC_KEY` | compiled-in key | api, standalone | ed25519 key (base64) verifying the feed |
| `CATALOG_REFRESH` | `24h` | api, standalone | sync interval |

## Invariants kept

- Every network call has a deadline (catalog fetch 30 s); the catalog sync runs
  off the standalone bridge goroutine.
- No per-app exec in polled paths: port allocation and the override file are
  built from files already read at deploy; no extra docker calls.
- Idle cost: one conditional GET a day.
- Secret values never reach the API or `compose.winterflow.yml`.
- Template and app routes keep org ownership checks; catalog templates are
  read-only to every org.

## Testing

- Parser: table tests per key and rule, including list/map `environment:`,
  comment descriptions, pattern checks, the volume-name rule.
- Override file: golden files for variables, expose (http/tcp/udp), overrides
  with `!override`, no block, and git-sourced apps.
- Ports: allocation, pinning, collision between apps, probe failure, rollback
  restoring an allocation, `forgetApp` pruning.
- Ingress: expose targets in `BuildConfig`; 400 for non-http targets.
- Templates: contract tests for create/update/upgrade with the new body; 403 on
  catalog save/delete; per-org rollout scope.
- Catalog: builder against a fixture apps tree; sync against an httptest server
  (ETag, bad signature, invalid entry skipped, withdraw and restore, two
  replicas racing) on SQLite and Postgres.
- Web: variables form, ports panel, picker search, templates page order, palette
  group.
- One end-to-end check on a standalone instance: install Actual Budget from a
  local feed, reach it through its port, upgrade after a feed change.

## Build order

1. Sub-project 1 (agent + app editor), one PR.
2. Sub-project 2 (templates), one PR.
3. Sub-project 3 (builder + sync) in this repo, plus the `winterflow-apps` PR.
4. Sub-project 4 (picker, templates page, palette), one PR.

Each gets its own implementation plan.
