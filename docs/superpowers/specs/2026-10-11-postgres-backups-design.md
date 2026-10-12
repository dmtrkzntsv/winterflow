# PostgreSQL backups — design

Builds on the storages-and-backups design
(`2026-10-10-storages-and-backups-design.md`, "C2 — Backups"), which ships
`sqlite`, `files` and `custom` and lists `postgres` as "later, same helper and
stdin/stdout contract". This adds the `postgres` type. Everything not named
here (storages, `WF_BACKUP_*`, scheduler, health, upload, manifests,
retention, copies, delete/sweep/orphans) works as for the built-in types.

## Context

Apps keep data in Postgres in two ways:

- **In the app.** A compose service runs Postgres (`image: postgres:16`).
- **Outside the app.** A managed or shared server (Supabase, RDS, a Postgres
  on another host) the app reaches through a connection URL it already has
  as a variable or secret (`DATABASE_URL`).

A `custom` backup with a `pg_dump` sidecar already works for both, but
winterflow can't Back up now, list copies, restore, or apply retention. The
`postgres` type gives both cases the full built-in treatment.

## Recipe

```yaml
x-winterflow:
  backups:
    - name: db                  # Postgres runs in this app
      type: postgres
      service: db
      database: app             # optional; default below
      user: app                 # optional; default below
      every: 24h
    - name: maindb              # an external database
      type: postgres
      url: ${DATABASE_URL}      # exactly one ${VAR} reference
      network: app              # optional: reach it from the app's network
```

A `postgres` backup names exactly one of:

| Field | Means | Reached by |
|---|---|---|
| `service` | the compose service that runs Postgres | `docker exec` in that service's container |
| `url` | a connection URL, given as one `${VAR}` reference to the app's own variable or secret | a short-lived client helper container |

Other fields:

- `database`, `user` (service form only). Defaults come from the service's
  environment in the compose file: `POSTGRES_DB` (else `POSTGRES_USER`, else
  `postgres`), and `POSTGRES_USER` (else `postgres`). In the url form both
  come from the URL.
- `network` (url form only): absent means the helper uses Docker's default
  bridge network, which reaches anything the host reaches. `app` joins the
  app's default compose network instead, for a database reachable only
  through the app (a VPN or tunnel sidecar).
- `every`, `keep`: as for `sqlite`/`files`. The default `every` for
  `postgres` is **24h**, not 1h: every run uploads a full dump (see
  "Unchanged").
- `paths`, `exclude`, `volume`, `path`: refused for `postgres`.

### Validation (save and update-backups refuse with 400)

- neither or both of `service` and `url`;
- `service` that the compose files don't define;
- `url` that is not exactly `${NAME}` (a literal URL would put a password in
  the committed compose file or config), or that names a variable the app
  doesn't have;
- `database`/`user` given with `url`; `network` given with `service`;
  `network` other than `app`;
- `database`/`user` that are not plain identifiers (letters, digits, `_`,
  `-`, `.`; at most 63 bytes).

Overrides in `.winterflow/config.json` and UI-added backups take the same
fields and the same checks. `WF_BACKUP_<NAME>_*` variables work as for the
other types.

## The URL is a secret

The URL is resolved by the agent at run time from the app's committed `.env`
(a plain variable) or by decrypting `secrets.json` with the agent's key (a
secret variable). It is handed to the helper **on stdin**, never in
arguments or the environment, so it doesn't appear in `ps`, `docker inspect`
or Docker's event log. It never appears in manifests, logs, status, API
responses or error messages: helper output is scrubbed of the URL and of its
password before it becomes an error or a warning. The UI picks the variable
by name and never shows its value.

## Runtime

### Dump

Both forms produce a Postgres custom-format archive,
`pg_dump --format=custom --compress=0` (the agent's zstd compresses it
better and keeps one pipeline), streamed to the agent's stdout reader,
compressed with zstd, hashed and uploaded lazily, exactly like `sqlite`. The
object is `<prefix><app-id>/<backup>/<UTC ts>-<kind>.pgdump.zst`, and the
manifest records the database name and the server's major version.

**Service form.** The agent finds the service's running container by the
compose project and service labels (one `docker ps`, at run time only) and
runs
`docker exec <container> pg_dump --format=custom --compress=0 -U <user> <db>`.
It connects over the container's local socket, which the official image
trusts, so no password is needed, and the server's own `pg_dump` matches its
version. An image whose local socket asks for a password (some non-official
images do) fails with Postgres's own message; the url form covers it. A
service that isn't running fails the run with "the db service isn't
running". A backup can't start a stopped database.

**Url form.** The agent runs a helper:
`docker run --rm -i --name … --label winterflow.role=backup-helper
--network <bridge|app> --memory 256m --cpu-shares 256 --cap-drop ALL
--security-opt no-new-privileges --log-driver none postgres:17-alpine` with an
entrypoint that reads the URL from the first stdin line and runs
`pg_dump --format=custom --compress=0 "$URL"`. The image is hard-coded like
`alpine:3.20` and pulled once. A newer `pg_dump` can dump older servers, so
the newest client covers servers 9.2 to 17. A server newer than the client
fails with Postgres's own version message.

Deadlines, helper cleanup on failure, `--log-driver none`, one run at a time,
the stop budget and lazy upload are the existing engine's. A `pg_dump` exit
other than 0 fails the run and uploads nothing.

### Unchanged

A custom-format dump carries its creation time, so two dumps of an idle
database differ and the files fingerprint trick doesn't apply. Every
`postgres` run uploads a copy, and retention keeps storage bounded: with the
24h default, `keep` holds about 14 daily and 6 monthly copies.
`NeedsRefresh` doesn't apply.

### Restore (in place)

1. Download and verify the copy's sha256, as today.
2. **Pre-restore copy:** a fresh dump, recorded as kind `pre-restore`. If it
   fails, nothing is touched.
3. **Stop** the app's services, except, in the service form, the Postgres
   service itself, which the restore needs.
4. Stream the copy into
   `pg_restore --clean --if-exists --single-transaction --no-owner
   --exit-on-error -d <db>`: `docker exec -i` in the service form; in the url
   form, the helper with the URL on stdin's first line and the archive after
   it.
5. **Start** the app again if it was running.

`--single-transaction` makes the restore all-or-nothing: on any error,
Postgres rolls back and the database is as it was. So a failed `pg_restore`
is "data unchanged": the app starts again and the error names the copy. The
one exception is a helper or `docker exec` killed after the commit began, or
with its outcome unknown. It follows the existing "left stopped, names the
pre-restore point" rule. `restoring.json`, the per-app restoring set,
`agent.update` refusal and the scheduler's per-app lock apply unchanged.

A database larger than one transaction can hold doesn't hit a special case.
Postgres's own limits apply and the error says so.

### External database warning

In the url form, the restore confirm says: **"This database is outside this
app. Anything else that uses it — other apps, people, jobs — sees the
restored data, and loses what changed since the copy."** The confirm button
reads "Restore the external database".

## UI

- **Add backup:** a third type, "PostgreSQL database". It replaces "Where the
  data is" with "The database":
  - "Runs in this app": a picker of the compose services, preselecting one
    whose image starts with `postgres`. Optional Database and User fields
    show the defaults as placeholders.
  - "External": a picker of the app's variables and secrets by name, never
    showing values, and "Reach it through the app's network" (off).

  Storage, schedule (default "Every day") and name as today. The name is
  auto-filled from the service or the variable (`db`, `database_url`).
- **Rows:** "PostgreSQL · service db → disk · Daily", or
  "PostgreSQL · external (DATABASE_URL) → disk · Daily".
- **Edit:** the same fields. The schedule and retention fields are as today.
- **Restore confirm:** the service form uses today's wording ("the app's
  database is replaced"). The url form shows the warning above.

## Testing

- **Recipe:** parse and validation tables for every rule above.
- **URL resolution:** from `.env` and from `secrets.json`. A test that the URL
  and its password never appear in docker args, env, errors, logs or the
  manifest; it fails if the scrubbing is removed.
- **Docker integration** (skipped without Docker or in `-short`):
  - a `postgres:16` service form: dump, change data, restore, assert the
    rows; pre-restore copy exists;
  - a url form against a separate `postgres:13` container (an older server
    dumped by the 17 client);
  - a failed restore (corrupted archive) leaves the database unchanged and
    starts the app again;
  - `network: app` reaches a database on the app network only.
- **Web:** Add backup and Edit for both forms (service picker preselect,
  variable picker shows names only), the external warning, row wording.
- **Contract:** update-backups refusals for the new rules.

## Out of scope

| Item | Where |
|---|---|
| MySQL/MariaDB (`mysqldump`/`mysql`) | next, same shape |
| Roles and other cluster-wide objects (`pg_dumpall --globals-only`) | follow-up; a copy holds one database |
| Point-in-time recovery (WAL archiving) | `custom` with a WAL tool |
| Restoring into a different database or a new app | D |
| Downloading a copy from the UI | follow-up |
