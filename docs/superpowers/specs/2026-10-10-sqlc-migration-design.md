# Bun ORM → sqlc migration — design

Date: 2026-10-10. Status: approved in conversation, pending spec review.

## Context

Persistence (`internal/infra/db`) uses Bun ORM over SQLite or Postgres,
chosen by the `DATABASE_URL` scheme. It is small: five repositories
(user, server, app, app_domain, server_domain; ~1.5k lines, most in
`user.go`), eight Go-function migrations that branch on
`db.Dialect().Name()`, a `BunConnection.Transaction(fn(bun.IDB))` helper,
and Bun model structs in `db/models`. Dialect differences leak into Go
(`RemoveMember` adds `FOR UPDATE` only on PG; migrations template column
types per dialect). Bun records a migration as applied before running it,
which forced hand-rolled atomicity in the SQLite table-rebuild migrations.

Released installs (v260823.x and later) have live SQLite databases with a
`bun_migrations` table; some may be partially migrated.

## Goals (from the user)

- **Consistency with econumo** (github.com/econumo/econumo): the same sqlc
  tooling and the same repository pattern, so either codebase reads like the
  other.
- **More order**: repositories that are plain, reviewable SQL behind a clear
  shape, instead of ORM query builders.
- **Isolation between SQLite and Postgres**: each engine has its own SQL;
  no dialect branches in Go.

Non-goals: changing the domain `port.*Repository` interfaces, changing
behaviour visible through the API, changing on-disk data formats, supporting
downgrade to a Bun build.

## Chosen approach: faithful econumo port

Queries and migrations are written once per engine. sqlc generates two
packages (`sqlitegen`, `pgsqlgen`) from them. Each repository has a private
`querier` interface written in the SQLite-generated types; the SQLite
adapter is a passthrough and the Postgres adapter converts its identically
shaped structs with Go struct conversion. The engine is chosen once, in the
repository constructor.

Rejected: fully separate per-engine repository implementations (stricter
isolation, but doubles hydrate code and diverges from econumo); one shared
query set with runtime placeholder rewriting (sqlc cannot type-check one file
against two engines, and it removes the isolation).

## Layout

The package path `internal/infra/db` is kept.

```
internal/infra/db/
  db.go           Open(log, url) → *DB{SQL *sql.DB, Driver string, Tx *backend.TxManager}; runs migrations;
                  HealthCheck, Shutdown
  backend/        DBTX interface, TxManager (WithTx, Querier), driver name constants
  sqlite/         modernc.org/sqlite opener
  pgsql/          jackc/pgx/v5/stdlib opener
  migrate/        forward-only migration runner (+ testdata/ frozen dumps)
  migrations/     sqlite/*.sql, pgsql/*.sql, embed.go
  sqlc/
    sqlc.yaml
    query/sqlite/*.sql
    query/pgsql/*.sql
    gen/sqlite/   package sqlitegen (committed, generated)
    gen/pgsql/    package pgsqlgen  (committed, generated)
    gen.go        //go:generate directive
    ascii_test.go
  types/          DateTime (kept; now the sqlc timestamp override)
  dbtest/         test helper: migrated SQLite temp file, or fresh PG schema
  repository/
    user/  server/  app/  appdomain/  serverdomain/
      repo.go     row aliases, querier interface, New, port methods, hydrate
      sqlite.go   sqliteQuerier: passthrough to sqlitegen
      pgsql.go    pgsqlQuerier: struct-conversion shim from pgsqlgen
      repo_test.go
```

Deleted: `bun.go`, `bootstrap.go` (model registration), `models/`,
`migrations/*.go`, `migration_atomic_test.go` (its guarantee moves into the
runner and its tests), the old flat `repository/*.go`.

Query files are one per aggregate per engine: `users.sql`, `tokens.sql`,
`organizations.sql`, `servers.sql`, `apps.sql`, `app_domains.sql`,
`server_domains.sql`.

## Drivers and connections

- **SQLite**: `modernc.org/sqlite` (pure Go). This is what `sqliteshim`
  already resolves to in our CGO-off release builds, so on-disk behaviour is
  unchanged. Keeps today's pragmas (`foreign_keys=ON`, `journal_mode=WAL`,
  `synchronous=NORMAL`, `cache_size`, `temp_store=memory`, `mmap_size`) and
  pool settings (`MaxOpenConns=1`, `MaxIdleConns=1`, no lifetime). Creates the
  parent directory as today.
- **Postgres**: `jackc/pgx/v5/stdlib` → `*sql.DB`. Keeps the
  `timezone=UTC` session pin introduced in #7 (`utcDSN`), today's pool
  settings (25 open / 10 idle / 5 min lifetime) and the 10 s ping deadline.
- Selection by URL scheme, unchanged: `sqlite://` or `postgres://`. Anything
  else fails at startup with the current error.
- `DEBUG_SQL=true` keeps working: a thin logging wrapper around the `DBTX`
  that logs each statement and its duration through `pkg/logger`.
  `BUNDEBUG` is dropped. `.env.example` is updated accordingly.

## sqlc configuration

One `db/sqlc/sqlc.yaml`, two `sql:` targets:

| | SQLite | Postgres |
|---|---|---|
| engine | `sqlite` | `postgresql` |
| schema | `../migrations/sqlite` | `../migrations/pgsql` |
| queries | `./query/sqlite` | `./query/pgsql` |
| out / package | `gen/sqlite` / `sqlitegen` | `gen/pgsql` / `pgsqlgen` |

Both: `sql_package: database/sql` (default), `emit_interface: true`,
`emit_empty_slices: true`, `emit_pointers_for_null_types: true`,
`emit_json_tags: false`.

sqlc reads the migration directories directly as its schema, so a query that
references a dropped or renamed column fails at `sqlc generate`.

Overrides make the generated structs identical across engines:

- Timestamp columns (`TIMESTAMP` on SQLite; `timestamptz` on PG) →
  `winterflow/internal/infra/db/types.DateTime`; nullable →
  `*types.DateTime`. This preserves today's storage format exactly: naive
  UTC `2006-01-02 15:04:05` text on SQLite, and the same string bound into
  `TIMESTAMPTZ` under the UTC session on PG.
- PG `uuid` → `string` (nullable → `*string`). SQLite `VARCHAR(36)` /
  `CHAR(36)` already map to `string`.
- Nullable text/varchar on PG → `*string` (matches SQLite under
  `emit_pointers_for_null_types`).
- Integer and boolean widths are aligned by override wherever the two
  engines disagree (e.g. PG `int4` vs SQLite `INTEGER` → `int64`). The exact
  list is settled when the queries are written; any mismatch surfaces as a
  compile error in the `pgsql.go` struct conversion.

The sqlc version is pinned and run via `go run github.com/sqlc-dev/sqlc/cmd/sqlc@<version>`,
so nothing needs installing:

- `make sqlc` — regenerate.
- `make sqlc-check` — `sqlc diff`; non-zero exit if `gen/` is stale.

`ascii_test.go` fails if any file under `query/` or `migrations/` contains a
non-ASCII byte (sqlc silently truncates the emitted SQL constant otherwise;
found the hard way in econumo).

## Migrations

### Runner (`db/migrate`)

- Source: `embed.FS` of `migrations/{sqlite,pgsql}/<version>_<name>.sql`;
  the version is the leading integer (`20241113120001`). Versions must be
  identical across the two directories; a test enforces it.
- Bookkeeping table `schema_migrations(version BIGINT PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`
  (`TIMESTAMPTZ` on PG).
- Each pending migration runs in **one transaction together with the insert
  of its version row**. A failure rolls back both; startup fails with the
  migration name and the failing statement.
- Statements are split on top-level `;`, respecting single/double quotes,
  `--` and `/* */` comments.
- A file whose first line is `-- migrate:foreign_keys=off` (SQLite only)
  is a table rebuild: the runner executes `PRAGMA foreign_keys=OFF` before
  `BEGIN` (it is a no-op inside a transaction), runs the file and
  `PRAGMA foreign_key_check` inside the transaction (any row → rollback and
  fail), commits, and restores `PRAGMA foreign_keys=ON` in all cases.
- Forward-only. No down migrations.
- Runs on boot from `db.Open`, as today. Postgres concurrency (several API
  replicas starting at once) is serialized with `pg_advisory_lock` on a fixed
  key around the whole run; SQLite has a single writer already.
- Test hook `MigrateTo(ctx, db, driver, version)` applies migrations up to and
  including `version`; used by upgrade tests.

### Ported migrations

One SQL file per existing Bun migration, **same version number**, content a
direct translation of that migration's dialect branch:

| version | name |
|---|---|
| 20241113120001 | initial_schema |
| 20260703000001 | pat_tokens |
| 20260703000002 | user_credentials |
| 20260703000003 | org_identity |
| 20260711000001 | app_domains |
| 20261010000001 | app_domains_uuid |
| 20261010000002 | app_domains_per_server (SQLite: `foreign_keys=off` rebuild) |
| 20261010000003 | server_domains |
| 20261011000001 | drop_bun_tables (new) |

The hand-rolled transactions in the Bun-era rebuild migrations are dropped:
atomicity is the runner's job now.

`drop_bun_tables`: `DROP TABLE IF EXISTS bun_migrations;` and
`DROP TABLE IF EXISTS bun_migration_locks;` on both engines.

### Adopting existing databases

Before applying anything, the runner checks: if `bun_migrations` exists and
`schema_migrations` does not, it creates `schema_migrations` and copies the
version (the leading integer of each `bun_migrations.name`) of every applied
Bun migration (`migrated_at IS NOT NULL`, or every row if the column is
absent) into it, in one transaction. Then pending migrations run normally,
including those Bun never applied (partially migrated installs resume) and
finally `drop_bun_tables`.

A Bun name with no matching SQL version fails startup with a clear error
rather than being silently ignored.

**Downgrade is unsupported** after this build has run: a Bun binary would
find no `bun_migrations` and try to re-run every migration. Stated in the PR
and release notes.

## Repositories and transactions

### TxManager (`db/backend/tx.go`)

- `WithTx(ctx, fn func(ctx context.Context) error) error`: begins a
  transaction and stores it in the returned context. A nested call on a
  context that already holds a transaction uses `SAVEPOINT sp_N` /
  `RELEASE SAVEPOINT` / `ROLLBACK TO SAVEPOINT` (same syntax on both
  engines). A panic rolls back and re-panics. Commit/rollback errors are
  logged and returned as today.
- `Querier(ctx) DBTX`: the active transaction, or the `*sql.DB`.
- Replaces `BunConnection.Transaction` and every `RunInTx` (11 call sites).

### Repository package shape

`repo.go`:

```go
type userRow = sqlitegen.User            // canonical row types are SQLite's

type querier interface {
    GetUser(ctx context.Context, db backend.DBTX, userID string) (userRow, error)
    // ... one method per query used by this repo
}

type Repo struct { tx *backend.TxManager; q querier; log *logger.Logger }

func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) // unknown driver → error

var _ port.UserRepository = (*Repo)(nil)
```

Port methods call `r.q.X(ctx, r.tx.Querier(ctx), …)` and map results through
`hydrate` functions into `model.*`. Generated types never leave the
repository package. `sql.ErrNoRows` is mapped to the same domain errors as
today (e.g. `model.ErrorUserNotFound`).

`sqlite.go`: `func (sqliteQuerier) GetUser(ctx, db, id) (userRow, error) { return sqlitegen.New(db).GetUser(ctx, id) }`.

`pgsql.go`: `func (pgsqlQuerier) GetUser(ctx, db, id) (userRow, error) { u, err := pgsqlgen.New(db).GetUser(ctx, id); return userRow(u), err }`.
Where shapes differ (column order after `ALTER TABLE … ADD COLUMN`, PG
collapsing a reused `$1` into a scalar parameter), the shim copies fields
explicitly. Drift is caught by the compiler.

### Dialect differences live in SQL

- `RemoveMember`'s caller lock: `SELECT … FOR UPDATE` in
  `query/pgsql/users.sql`, a plain `SELECT` in `query/sqlite/users.sql`
  (SQLite serializes writers). The Go branch on `dialect.PG` is removed.
- Booleans are written as `TRUE`/`FALSE` literals on PG and `1`/`0` on
  SQLite where they appear in SQL text; placeholders are `?` vs `$N`.
- No dynamic SQL is expected (every current query is fixed-shape). If one
  appears it lives in that repo's `sqlite.go`/`pgsql.go`, never in `repo.go`.

### Wiring

`bootstrap/api.go` and `bootstrap/standalone.go` call `db.Open(log, url)`
instead of `db.NewBunConnection`; `bootstrap/core.go` builds the five repos
with `New(d.Tx, d.Driver, log)`. Health check and shutdown keep their
behaviour on `*sql.DB`.

## Testing

- **API contract suite** (`internal/app/web/contract`) is the primary proof
  of unchanged behaviour; it runs unchanged at the HTTP level on SQLite
  (`go` CI job) and Postgres (`contract` job, `make test-pg`).
  `migration_test.go` drops Bun and uses `migrate.MigrateTo` to build the
  "before" schema for its upgrade-path assertions.
- **Repository tests**: the current `repository/*_test.go` cases are ported
  into each new package. `dbtest.Open(t)` returns a migrated SQLite temp file
  by default, or a fresh PG schema when `WF_TEST_DATABASE_URL` is set (same
  convention as the contract suite). `scripts/test-pg.sh` and the
  `contract` CI job also run `./internal/infra/db/...`, so repositories are
  exercised on both engines.
- **Adoption tests** (`db/migrate`), per engine:
  - Frozen dumps in `migrate/testdata/{sqlite,pgsql}/`, produced once from
    the current Bun code before it is deleted: (a) full Bun-era schema with
    all eight `bun_migrations` rows; (b) schema and rows as of
    `20260711000001` (partially migrated).
  - Load dump → `Migrate` → assert `schema_migrations` holds every version,
    no ported migration re-ran (seed rows inserted with the dump survive),
    `bun_*` tables are gone, and the normalized schema (from
    `sqlite_master` / `information_schema` + `pg_indexes`) equals a fresh
    install's.
- **Runner unit tests**: statement splitting (quotes, comments, `;` inside
  strings); a failing statement mid-file leaves neither its effects nor its
  version row; the `foreign_keys=off` path including a `foreign_key_check`
  violation causing rollback; version sets identical across engines; unknown
  Bun version fails.
- **`TxManager` tests**: commit, rollback on error, rollback on panic, nested
  savepoint rollback leaves the outer transaction usable, on both engines.
- `ascii_test.go` as above.

Coverage: `internal/infra/db/sqlc/gen/**` is excluded from the diff-coverage
gate and ratchet (generated code). Hand-written code meets the 80% gate.

## CI

- New `sqlc` job: `make sqlc-check`.
- `contract` job (Postgres service) additionally runs `./internal/infra/db/...`.
- No new services or containers.

## Docs and config

- `CLAUDE.md` / `AGENTS.md`: replace "Persistence uses Bun ORM … migrations
  in `internal/infra/db/migrations` and run on startup" with the sqlc pattern
  and a short "adding a query / migration" recipe (SQL in both engine
  directories → `make sqlc` → querier method + both shims; migrations in
  both directories with the same version).
- `.env.example`: `DEBUG_SQL` re-documented for the logging wrapper.
- `Makefile`: working `sqlc` and new `sqlc-check` targets.
- `go.mod`: `uptrace/bun*` removed; `modernc.org/sqlite` and
  `jackc/pgx/v5` become direct dependencies. `scripts/` third-party licence
  collection picks this up automatically.

## Delivery

One draft PR against `main` on branch `refactor/sqlc`, in commits that each
keep the tree green:

1. Freeze schema dumps from the current Bun code (testdata only).
2. Runner, SQL migrations, adoption + runner tests (not yet wired).
3. sqlc config, queries, generated code, `ascii_test.go`, Makefile targets.
4. TxManager, openers, `dbtest`.
5. Repositories, one aggregate per commit, with ported tests.
6. Rewire bootstrap to `db.Open`, delete Bun code and models, port
   `contract/migration_test.go`, drop Bun from `go.mod`.
7. `drop_bun_tables` migration, CI job, docs, `.env.example`.

## Risks

- **Schema drift between the Bun-era schema and the SQL port**: caught by
  the adoption test's normalized schema comparison against the frozen dumps.
- **Timestamp format drift on SQLite**: prevented by keeping `types.DateTime`
  as the override on both engines; a repository test asserts the raw stored
  text format.
- **sqlc engine quirks** (type-name token joining, non-ASCII truncation,
  divergent parameter shapes on PG): overrides spelled to match, ASCII guard
  test, explicit field copies in shims where needed.
