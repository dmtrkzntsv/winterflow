# Bun ORM → sqlc Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Bun ORM in `internal/infra/db` with hand-written per-engine SQL compiled by sqlc (econumo's pattern): a forward-only SQL migration runner that adopts existing Bun-era databases, two generated query packages (`sqlitegen`, `pgsqlgen`), a savepoint-aware `TxManager`, and five repositories with private `querier` interfaces — with no change to the `port.*Repository` interfaces, the API, or the on-disk data formats.

**Architecture:** `db.Open(log, url)` picks `sqlite.Open` (modernc.org/sqlite) or `pgsql.Open` (pgx/v5 stdlib, simple protocol, session pinned to UTC) by URL scheme, runs `migrate.Migrate` (embedded `migrations/{sqlite,pgsql}/*.sql`, one transaction per migration with its `schema_migrations` row, Postgres advisory lock, Bun adoption first) and returns `*db.DB{SQL, Driver, Tx}`. Each repository package (`repository/{user,server,app,appdomain,serverdomain}`) has `repo.go` (port methods + hydrate, written against SQLite-generated row types), `sqlite.go` (passthrough to `sqlitegen`) and `pgsql.go` (Go struct conversion from the identically shaped `pgsqlgen` types); the engine is chosen once in `New(tx, driver, log)`. Dialect differences live only in the `.sql` files.

**Tech Stack:** Go 1.25 (module `winterflow`), sqlc v1.31.1 (run via `go run`, never a module dependency), `modernc.org/sqlite` v1.49.1, `github.com/jackc/pgx/v5` v5.9.2 (`stdlib`), Docker `postgres:16` for `make test-pg`.

**Spec:** docs/superpowers/specs/2026-10-10-sqlc-migration-design.md

## Global Constraints

- Module `winterflow`, `go 1.25.3`. New direct requirements are exactly `github.com/jackc/pgx/v5 v5.9.2` and `modernc.org/sqlite v1.49.1` (both already in `go.sum` as indirect); Task 10 removes every `github.com/uptrace/bun*` requirement. sqlc is never added to `go.mod`.
- sqlc is pinned to `v1.31.1` and run as `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 -f internal/infra/db/sqlc/sqlc.yaml <generate|diff>` (`make sqlc` / `make sqlc-check`); building it needs a C compiler (its Postgres parser is cgo); the first run takes about a minute.
- The package path `internal/infra/db` stays; layout exactly as the spec's "Layout" section, repository packages named after their directories (`user`, `server`, `app`, `appdomain`, `serverdomain`) and imported with the aliases `userrepo`, `serverrepo`, `apprepo`, `appdomainrepo`, `serverdomainrepo`.
- Driver names: `backend.SQLite = "sqlite"`, `backend.Postgres = "postgres"`. `DATABASE_URL` schemes: `sqlite://` and `postgres://` only; anything else fails with `unsupported database type in connection string: <url>`.
- SQLite opener: pragmas `foreign_keys = ON`, `journal_mode = WAL`, `synchronous = NORMAL`, `cache_size = 1000`, `temp_store = memory`, `mmap_size = 268435456`; `MaxOpenConns=1`, `MaxIdleConns=1`, `ConnMaxLifetime=0`; creates the parent directory (0755).
- Postgres opener: `utcDSN` (`timezone=UTC`, other-cased `TimeZone` keys removed), `pgx.QueryExecModeSimpleProtocol`, `MaxOpenConns=25`, `MaxIdleConns=10`, `ConnMaxLifetime=5m`, ping within 10 s.
- Timestamps are stored exactly as today: `types.DateTime.Value()` = naive UTC `"2006-01-02 15:04:05"` text on SQLite and bound into `TIMESTAMP`/`TIMESTAMPTZ` under the UTC session on Postgres; `types.DateTime.Scan` returns UTC (Task 4).
- Bookkeeping: `schema_migrations(version BIGINT PRIMARY KEY, applied_at TIMESTAMP NOT NULL)` (`TIMESTAMPTZ` on Postgres), `applied_at = CURRENT_TIMESTAMP`; files `<version>_<name>.sql`, identical version lists per engine; marker first line `-- migrate:foreign_keys=off` is SQLite-only; Postgres runs hold `pg_advisory_lock(0x77666d6967726174)`; `db.Open` bounds migration (lock wait included) by 5 minutes.
- Every network operation has a deadline (project invariant): `db.Open` 5 min, ping 10 s, `HealthCheck` 5 s, `dbtest` admin statements 30 s, repository tests use `dbtest.Ctx(t)` (30 s), so a deadlock on SQLite's single connection fails instead of hanging.
- `port.UserRepository`, `port.ServerRepository`, `port.AppRepository`, `port.AppDomainRepository`, `port.ServerDomainRepository` are not changed; `internal/app/web/contract/testdata/parity.golden.json` is not changed and `TestParity` passes on SQLite and Postgres.
- Generated code under `internal/infra/db/sqlc/gen/**` is never edited by hand; change `query/*.sql` or `migrations/*/*.sql`, then `make sqlc`. Every `.sql` file sqlc reads is ASCII-only (`TestSQLFilesAreASCII`).
- `.env.example` rule (CLAUDE.md): no env var is added or renamed; `DEBUG_SQL` changes meaning (logging wrapper) and is re-documented in Task 11; `BUNDEBUG` is dropped (it was never in `.env.example`). `WF_TEST_DATABASE_URL` is test-only.
- CI gates that must stay green at the end: gofmt, `go vet ./...`, `WF_CONTRACT_GUARD=require make test` (SQLite, `-race`, `-coverpkg=./...`), `make test-pg` (Postgres: contract suite + `./internal/infra/db/...` from Task 2 on), `make sqlc-check` (Task 3 on; CI job in Task 11), the coverage ratchet (`coverage-baseline.json` go 75.2) and changed lines ≥80% covered **with the Postgres profile merged** — exactly what CI's `coverage` job does. Locally: `make test && make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out' && tail -n +2 contract-postgres.out >> coverage.out && make web-test && go run ./tools/diffcov --base main --min 80 --go coverage.out --lcov web/coverage/lcov.info`. (`make diffcov DIFFCOV_BASE=main` alone reruns `make test` and sees SQLite only: measured 70.8% for the finished branch, because every `pgsql.go` shim runs only on Postgres; merged: 86.5%.)
- Postgres for local runs: `make test-pg` (throwaway `postgres:16`, random port, removed on exit); extra flags go through `TEST_PG_ARGS`, e.g. `make test-pg TEST_PG_ARGS='-run TestAdopt -v'`. Before Task 2 it runs only the contract suite.
- Never build SQL from user input; the only hand-built SQL outside `query/*.sql` and `migrations/` is the runner's bookkeeping and test helpers.
- Tasks run sequentially; files touched by more than one task: `go.mod`/`go.sum` (2, 10), `.github/workflows/ci.yml` (2, 11), `Makefile` (2, 3), `internal/infra/db/migrations/{sqlite,pgsql}/` (2, 11), `internal/infra/db/migrate/adopt_test.go` (2, 11), `internal/infra/db/sqlc/gen/**` (3, re-checked in 11), `AGENTS.md`/`CLAUDE.md` (11).
- Commits: Conventional Commits, imperative; each message ends with a blank line then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push. Each task ends with `go build ./... && go vet ./... && go test ./...` green.

## Review Focus

1. **A repository query escaping its transaction.** Inside `r.tx.WithTx(ctx, func(ctx context.Context) error {...})` every query must use `r.tx.Querier(ctx)` with the closure's `ctx`. Using the outer `ctx` (or a `DBTX` captured before `WithTx`) deadlocks SQLite (its single pooled connection is held by the transaction) and on Postgres silently commits that statement outside the transaction. Expected: all-or-nothing. Pinned by `TestNestedWithTxRollsBackToSavepoint` (Task 4), `TestServerDomainsReplaceIsAtomic` (Task 5), `TestReplaceForAppIsAtomic` (Task 6), `TestClaimServerIsAtomic` (Task 8), `TestCreateMemberUserIsAtomic` (Task 9) — each forces a failure after an earlier write in the same unit of work — and by `dbtest.Ctx`'s 30 s deadline turning a SQLite deadlock into a failure.
2. **Timestamp zone and format drift.** pgx hands `TIMESTAMPTZ` back in a fixed `+00:00` zone (simple protocol) or `time.Local` (extended), and a naive string bound under a non-UTC session shifts the instant; SQLite must keep the Bun-era `2006-01-02 15:04:05` text. The contract suite cannot see zone drift (`TestMain` sets `time.Local = time.UTC`). Expected: stored text unchanged, values read back in UTC. Pinned by `TestDateTimeScanNormalizesToUTC` and `TestOpenPinsUTCSession` (Task 4), `TestServerDomainsStoredTimestampFormat` (Task 5), `TestCreatedAtStorage` (Task 7), `TestUserCreatedAtLocationIsUTC` (Task 9).
3. **Foreign keys left off after a failed table rebuild.** `PRAGMA foreign_keys` is per connection and SQLite has one; if the runner forgets to restore it on the error path, every later write runs without FK enforcement (cascades stop: deleted users keep PATs). Expected: `foreign_keys = 1` after success and failure. Pinned by `TestForeignKeysOffRebuild` and `TestAppDomainsRebuildIsAtomic` (Task 2).
4. **Several API replicas migrating at once.** Without the advisory lock, concurrent boots race on `CREATE TABLE`/`schema_migrations` and crashloop. Expected: every runner succeeds, each version applied once. Pinned by `TestConcurrentMigrateIsSerialized` (Task 2; verified at plan time to fail with `duplicate key value violates unique constraint "pg_type_typname_nsp_index"` when the lock is removed).
5. **Adoption repeated or partial.** A boot that adopted `bun_migrations` but died before `drop_bun_tables` leaves both tables; adopting again would copy stale Bun rows and skip real migrations. A Bun name without a SQL counterpart must fail loudly. Expected: `schema_migrations` wins once it exists; unknown names fail and leave nothing behind. Pinned by `TestAdoptionIsNotRepeated`, `TestAdoptRejectsUnknownBunVersion` and the partial dump in `TestAdoptBunDatabase` (Task 2).

---

## Verified at plan time

Everything below was applied to scratch copies of this branch at `2c01fc9` and run; the intermediate states after Tasks 2, 3, 4 and each repository task were built and tested separately, the final state (after Task 11) fully.

- **sqlc v1.31.1** (`go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1`). With the overrides in Task 3, all 45 generated struct types (14 models, `*Params`, `*Row`) convert between `pgsqlgen` and `sqlitegen` with Go struct conversion in both directions (compile-checked), and the two `Querier` interfaces have identical method sets. Override spellings that work:
  - SQLite `db_type: "TIMESTAMP"` → `winterflow/internal/infra/db/types.DateTime`; `nullable: true` → `{import: "winterflow/internal/infra/db/types", type: "DateTime", pointer: true}`.
  - SQLite `db_type: "CHAR(36)"` → `string` (without it `app_domains.app_id/server_id` come out as `interface{}`; `VARCHAR(36)` and nullable `TEXT` already map to `string` / `*string`).
  - Postgres `TIMESTAMPTZ` is catalogued as **`timestamptz`** (unqualified): `db_type: "pg_catalog.timestamptz"` alone does **not** match; both `timestamptz` and `pg_catalog.timestamptz` are listed. Plain `TIMESTAMP` (used by `app_domains`/`server_domains` on Postgres too) is `pg_catalog.timestamp`.
  - Postgres `uuid` → `string` / `*string`; nullable `pg_catalog.varchar` and `text` → `*string` (`emit_pointers_for_null_types` does not apply to Postgres with `database/sql`); `pg_catalog.int4` → `int64` (SQLite `INTEGER` is `int64`).
  - `count(*)` is `int64` and `SELECT EXISTS (...)` is `bool` on both engines (modernc returns 0/1, `database/sql` converts).
- **modernc.org/sqlite:** `types.DateTime.Value()` stores `'2026-07-02 12:04:05'` (checked with `quote()`; a zoned input is converted to UTC); reading a `TIMESTAMP` column the driver parses the text into a UTC `time.Time`, which `Scan` accepts; Bun-era rows read back unchanged. Gotcha: scanning a `TIMESTAMP` column into a Go `string` yields `2026-07-02T12:04:05Z` (driver parse + `database/sql` formatting) — tests read raw text with `CAST(col AS TEXT)`. Booleans are stored as integer `1`/`0`, as Bun stored `TRUE`/`FALSE`.
- **pgx/v5 stdlib:** URL query keys it does not know (`timezone`, `search_path`) become startup parameters, so `utcDSN` works unchanged (`SHOW timezone` = `UTC`). Both the extended and the simple protocol bind `DateTime` through `Value()` as naive text: under `timezone=America/New_York` it is stored 4 h off, so the UTC pin is load-bearing. Reading `TIMESTAMPTZ`: extended protocol returns `time.Local`, simple protocol a fixed `+0000` zone; `TIMESTAMP` comes back UTC. Hence simple protocol (no server-side prepared statements, client-side interpolation like Bun's driver) plus UTC normalization in `DateTime.Scan`.
- **Frozen dumps:** produced by a build-tagged test in package `db` that runs the Bun migrator with a query hook and records every successful statement except `SELECT`/transaction control (DDL, Bun's own `bun_migrations` inserts, then one seed row per table written through the Bun models). Replaying the file rebuilds the Bun-era database exactly; two runs produced byte-identical files. Bun stores migration names as bare versions (`'20241113120001'`).
- **Runner/adoption:** `TestAdoptBunDatabase` passes on both engines for the full and the partial (`20260711000001`) dumps; making one column differ (`VARCHAR(33)` on SQLite, `DEFAULT TRUE` on Postgres) fails all four cases, so the normalized schema comparison bites. The Bun-era atomic-rebuild scenario (a child table referencing `app_domains(domain)`) fails `foreign_key_check` ("foreign key mismatch") and rolls back.
- **Final state:** `go vet`, gofmt clean; `go test -race ./...` green (the user repository package takes ~48 s under `-race` because of bcrypt); contract suite with `WF_CONTRACT_GUARD=require` green on SQLite and Postgres 16 including `TestParity` against the unchanged golden; Go total coverage 75.7% SQLite-only (baseline 75.2); diff coverage 70.8% SQLite-only / 86.5% with the Postgres profile merged.

## Decisions and deviations from the spec (review these)

1. **`make test-pg` and the CI `contract` job run `./internal/infra/db/...` from Task 2**, not at the end: the runner's Postgres paths need that coverage as soon as they exist. Task 11 still adds the `sqlc` CI job.
2. **`drop_bun_tables` lands in Task 11** as the spec orders. Until then the adoption tests exclude `bun_*` tables from the schema comparison; Task 11 adds the "bun tables are gone" assertion.
3. **`FindClaims` without `IN (...)`:** sqlc cannot express a variable-length list on Postgres with `database/sql` (it emits `pq.Array`, i.e. `lib/pq`), and SQLite's `sqlc.slice` turns an empty list into `NULL`. The repository looks up each distinct domain with the primary key `(server_id, domain)` and drops `excludeAppID`'s rows in Go, then returns them sorted by domain — same results; `excludeAppID` no longer reaches SQL (a non-UUID exclude id stops erroring on Postgres).
4. **`SyncApps` without `NOT IN (...)`:** inside the transaction it lists the server's app ids and deletes each one not reported — same effect as Bun's `DELETE ... WHERE server_id = ? AND app_id NOT IN (...)`, including "empty report deletes all".
5. **`GetServers`** runs three fixed queries (servers, capabilities, features — each scoped by the user's organizations) instead of Bun's relation loads; capabilities now come back ordered by name (Bun: unspecified order). `nil` capabilities/features stay `nil` (omitted from JSON).
6. **Determinism added where Bun had none:** `GetFirstServerID` orders by `created_at, server_id`; the member-provider fallback orders by `provider, external_id`.
7. **`ListMembers`** joins `organization_users` with `users` (inner join; the FK cascade guarantees the user row) and keeps Bun's per-member credentials/provider lookups.
8. **`db.Open` returns `(*DB, error)`** (spec: `→ *DB`); `BootstrapAPI`/`BootstrapStandalone` `log.Fatal` on error as `NewBunConnection` did.
9. **`DEBUG_SQL=true`** logs statement text, duration and error at Info level through `pkg/logger`, never the arguments (they include password and token hashes).
10. **Contract harness pool:** Bun-era `newEnv` set `MaxOpenConns(4)` on both engines, so three of four SQLite connections ran without `PRAGMA foreign_keys`; now only Postgres gets 4, SQLite keeps production's single connection.
11. **Repository tests run on Postgres too**, so their ids become UUIDs (`dbtest.ID(n)`) and one assertion changed data: Postgres sorts `ORDER BY name` with the database collation (`en_US.utf8` in `postgres:16`, which ignores punctuation), SQLite byte-wise, so `"*.b.app"` vs `"a.app"` order differs. This is Bun-era behaviour, unchanged; **decide** whether you want `COLLATE "C"` on the Postgres `ORDER BY`s for strict engine parity (not done here).
12. `handler/server/handler_test.go` keeps SQLite explicitly (its ids `srv-1`, `cert-1` are not UUIDs).
13. sqlc's `emit_empty_slices` makes `ListForApp` and `GetServerUserIDs` return `[]string{}` instead of `nil` when empty; both are internal (iterated, never serialized).

## File Structure

| Path | Action (task) | Responsibility |
|---|---|---|
| `internal/infra/db/freeze_dump_test.go` | create (1), delete (10) | Build-tagged (`freezedump`) one-off that replays the Bun migrator + seed rows and writes the frozen dumps |
| `internal/infra/db/migrate/testdata/{sqlite,pgsql}/bun_full.sql` | create (1) | Bun-era database with all eight migrations applied + seed rows |
| `internal/infra/db/migrate/testdata/{sqlite,pgsql}/bun_20260711000001.sql` | create (1) | Bun-era database migrated only up to `20260711000001` + seed rows |
| `internal/infra/db/backend/backend.go` | create (2) | Driver name constants, `DBTX` |
| `internal/infra/db/backend/tx.go`, `tx_test.go` | create (4) | `TxManager` (`WithTx`, savepoints, `Querier`, `DEBUG_SQL` logging wrapper) and its tests |
| `internal/infra/db/migrations/embed.go` | create (2) | `embed.FS` of both engines' SQL; `FS(driver)` |
| `internal/infra/db/migrations/sqlite/*.sql`, `pgsql/*.sql` | create (2, 11) | Ported migrations (8 per engine) and `20261011000001_drop_bun_tables` (Task 11) |
| `internal/infra/db/migrations/*.go` (Bun) | delete (10) | Bun Go migrations and `migrations.go` |
| `internal/infra/db/migrate/migrate.go` | create (2) | `Load`, `Migrate`, `MigrateTo`, `Run`; adoption; advisory lock; foreign-keys-off rebuilds |
| `internal/infra/db/migrate/split.go` | create (2) | Statement splitter |
| `internal/infra/db/migrate/{helpers,split,migrate,adopt,concurrent}_test.go` | create (2), modify adopt (11) | Runner, splitter, adoption and concurrency tests on both engines |
| `internal/infra/db/sqlc/sqlc.yaml` | create (3) | Two `sql:` targets with the type overrides |
| `internal/infra/db/sqlc/query/{sqlite,pgsql}/*.sql` | create (3) | `users`, `tokens`, `organizations`, `servers`, `apps`, `app_domains`, `server_domains` |
| `internal/infra/db/sqlc/gen/{sqlite,pgsql}/*.go` | generate (3) | `sqlitegen`, `pgsqlgen` (committed, never edited) |
| `internal/infra/db/sqlc/gen.go`, `ascii_test.go` | create (3) | `//go:generate` directive; ASCII guard for every `.sql` file sqlc reads |
| `Makefile` | modify (2, 3) | `test-pg` comment; `sqlc`, `sqlc-check` targets |
| `tools/diffcov/{classify,goprofile}.go` (+ tests) | modify (3) | Exclude `internal/infra/db/sqlc/gen/` from the diff gate and the ratchet total |
| `internal/infra/db/types/datetime.go`, `datetime_test.go` | modify (4) | `Scan` returns UTC |
| `internal/infra/db/sqlite/sqlite.go`, `sqlite_test.go` | create (4) | modernc opener (pragmas, pool, directory) |
| `internal/infra/db/pgsql/pgsql.go`, `pgsql_test.go` | create (4) | pgx opener (`utcDSN`, simple protocol, pool, ping) |
| `internal/infra/db/db.go`, `db_test.go` | create (4) | `DB`, `Open` (open + migrate), `HealthCheck`, `Shutdown` |
| `internal/infra/db/dbtest/dbtest.go` | create (4) | Test databases (SQLite temp file / fresh Postgres schema), `Ctx`, `Rebind`, `Exec`, `QueryString`, `ID`, seed helpers |
| `internal/infra/db/repository/serverdomain/{repo,sqlite,pgsql,repo_test}.go` | create (5) | `port.ServerDomainRepository` |
| `internal/infra/db/repository/appdomain/...` | create (6) | `port.AppDomainRepository` |
| `internal/infra/db/repository/app/...` | create (7) | `port.AppRepository` |
| `internal/infra/db/repository/server/...` | create (8) | `port.ServerRepository` (+ `FirstServerID`) |
| `internal/infra/db/repository/user/...` | create (9) | `port.UserRepository` |
| `internal/infra/db/{bun.go,bootstrap.go,bun_test.go,migration_atomic_test.go}`, `models/`, `repository/*.go` (flat) | delete (10) | Bun connection, model registration, models, Bun repositories and their tests |
| `internal/infra/bootstrap/{core,api,standalone}.go`, `core_test.go` | modify (10) | `db.Open`, `newRepos`, concrete `*serverrepo.Repo` |
| `internal/app/web/contract/{harness,db,migration}_test.go` | modify (10) | `db.Open`, pgx admin connection, `migrate.MigrateTo` upgrade tests |
| `internal/app/web/handler/server/handler_test.go` | modify (10) | New repositories over `db.Open` (SQLite) |
| `go.mod`, `go.sum` | modify (2, 10) | pgx + modernc direct (2); Bun removed (10) |
| `scripts/test-pg.sh` | modify (2) | Also run `./internal/infra/db/...` |
| `.github/workflows/ci.yml` | modify (2, 11) | `contract` job also runs `./internal/infra/db/...` (2); `sqlc` job (11) |
| `.gitignore` | modify (2) | `/contract-postgres.out` |
| `.env.example` | modify (11) | `DEBUG_SQL` re-documented |
| `CLAUDE.md`, `AGENTS.md`, `MIGRATION.md`, `.goreleaser.yaml` | modify (11) | sqlc pattern, recipe, commands; stale Bun/sqliteshim mentions |

---

### Task 1: Freeze the Bun-era databases

**Files:**
- Create: `internal/infra/db/freeze_dump_test.go` (build tag `freezedump`; deleted with Bun in Task 10)
- Create (generated by the step below, committed): `internal/infra/db/migrate/testdata/sqlite/bun_full.sql`, `internal/infra/db/migrate/testdata/sqlite/bun_20260711000001.sql`, `internal/infra/db/migrate/testdata/pgsql/bun_full.sql`, `internal/infra/db/migrate/testdata/pgsql/bun_20260711000001.sql`

**Interfaces:**
- Consumes (current Bun code, package `db`): `setupSQLite(connStr string, log *logger.Logger) (*sql.DB, error)`, `setupPostgreSQL(connStr string, log *logger.Logger) (*sql.DB, error)`, `utcDSN(string) string`, `migrations.Migrations` (Bun), `models.*`.
- Produces: four replayable `.sql` files. Each is a header comment (2 lines) then one statement per `;\n`. Seed rows use these fixed ids, which Task 2's adoption test relies on: organization `0192a3b4-0000-7000-8000-000000000001` (name `frozen org`, icon `server`), user `…0002`, server `…0003`, certificate `…0004`, app `…0005`, PAT `…0006` (name `ci; deploy`), pending registration `…0007`/`…0008`; two `app_domains` rows; in `bun_full.sql` also two `server_domains` rows.

This task has no red/green cycle: it records the current code's behaviour before Task 10 deletes it.

- [ ] **Step 1: Write the freeze program**

Create `internal/infra/db/freeze_dump_test.go`:

```go
//go:build freezedump

// TestFreezeBunDumps writes the frozen Bun-era databases the migration
// runner's adoption tests load (internal/infra/db/migrate/testdata). It was
// run once, before Bun was removed, with:
//
//	WF_TEST_DATABASE_URL=postgres://... go test -tags freezedump -run TestFreezeBunDumps -count=1 ./internal/infra/db/
//
// Each dump is every statement Bun executed (DDL, its bun_migrations
// bookkeeping, then seed rows written through the Bun models), minus SELECTs
// and transaction control, so replaying it rebuilds the database exactly.
package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"

	"winterflow/internal/infra/db/migrations"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// recorder keeps every successful statement Bun sends that changes the
// database, in order.
type recorder struct{ stmts []string }

func (r *recorder) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context { return ctx }

func (r *recorder) AfterQuery(_ context.Context, e *bun.QueryEvent) {
	q := strings.TrimSpace(e.Query)
	if e.Err != nil || q == "" {
		return
	}
	switch strings.ToUpper(strings.Fields(q)[0]) {
	case "SELECT", "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE":
		return
	}
	r.stmts = append(r.stmts, q)
}

// Fixed ids, so the adoption tests can look the seed rows up.
const (
	fzOrg   = "0192a3b4-0000-7000-8000-000000000001"
	fzUser  = "0192a3b4-0000-7000-8000-000000000002"
	fzSrv   = "0192a3b4-0000-7000-8000-000000000003"
	fzCert  = "0192a3b4-0000-7000-8000-000000000004"
	fzApp   = "0192a3b4-0000-7000-8000-000000000005"
	fzTok   = "0192a3b4-0000-7000-8000-000000000006"
	fzReg   = "0192a3b4-0000-7000-8000-000000000007"
	fzRegCt = "0192a3b4-0000-7000-8000-000000000008"
)

// seed writes one row into every table through the Bun models, the way the
// Bun repositories wrote them. withServerDomains: server_domains exists.
func seed(t *testing.T, ctx context.Context, db *bun.DB, withServerDomains bool) {
	t.Helper()
	at := types.DateTime(time.Date(2026, 8, 23, 10, 11, 12, 0, time.UTC))
	exp := types.DateTime(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC))
	avatar := "https://example.test/a.png"
	rows := []any{
		&models.Organization{OrganizationID: fzOrg, Name: "frozen org", Icon: "server", Color: "#3b82f6", CreatedAt: at},
		&models.User{UserID: fzUser, Name: "Frozen", Avatar: &avatar, CreatedAt: at, LastSeen: at},
		&models.OrganizationUser{OrganizationID: fzOrg, UserID: fzUser, Role: "owner", CreatedAt: at},
		&models.UserConnectedAccount{Provider: "local", ExternalID: "frozen@example.test", UserID: fzUser},
		&models.UserCredentials{UserID: fzUser, Email: "frozen@example.test", PasswordHash: "$2a$10$frozenfrozenfrozenfrozenfrozenfrozenfrozenfrozenfroze", MustChangePassword: true, UpdatedAt: at},
		// A ';' inside a string literal exercises the replay's splitter.
		&models.UserToken{TokenID: fzTok, UserID: fzUser, Name: "ci; deploy", TokenPrefix: "wfp_abcd", TokenHash: strings.Repeat("ab", 32), TokenType: "pat", ExpiresAt: &exp, CreatedAt: at},
		&models.Server{ServerID: fzSrv, OrganizationID: fzOrg, Name: "box", CreatedAt: at, LastSeen: &at},
		&models.ServerCapability{ServerID: fzSrv, Name: "server_ip", Value: "203.0.113.7", UpdatedAt: at},
		&models.ServerFeature{ServerID: fzSrv, Name: "ingress", IsEnabled: true},
		&models.ServerCertificate{CertificateID: fzCert, ServerID: fzSrv, Certificate: "Y2VydA==", IsActive: true, ExpiresAt: exp, CreatedAt: at},
		&models.ServerRegistration{ServerID: fzReg, CertificateID: fzRegCt, Hostname: "pending", Code: "123456", ExpiresAt: exp, Certificate: "Y2VydA==", CertificateExpiresAt: exp, CreatedAt: at},
		&models.App{AppID: fzApp, ServerID: fzSrv, Name: "grafana", Version: "3", Icon: "chart", Color: "#123456", CreatedAt: at},
		&models.AppDomain{Domain: "grafana.example.test", AppID: fzApp, ServerID: fzSrv, Kind: "route", SSL: true, UpstreamPort: 3000, UpdatedAt: at},
		&models.AppDomain{Domain: "www.example.test", AppID: fzApp, ServerID: fzSrv, Kind: "redirect", Target: "https://grafana.example.test", Code: 301, UpdatedAt: at},
	}
	if withServerDomains {
		rows = append(rows,
			&models.ServerDomain{ServerID: fzSrv, Name: "*.example.test", TLS: "certificate", NotAfter: &exp, Issuer: "CF Origin", UpdatedAt: at},
			&models.ServerDomain{ServerID: fzSrv, Name: "example.test", TLS: "auto", UpdatedAt: at},
		)
	}
	for _, r := range rows {
		if _, err := db.NewInsert().Model(r).Exec(ctx); err != nil {
			t.Fatalf("seed %T: %v", r, err)
		}
	}
}

// migrateUpTo applies the Bun migrations named <= last ("" = all).
func migrateUpTo(t *testing.T, ctx context.Context, db *bun.DB, last string) {
	t.Helper()
	set := migrate.NewMigrations()
	for _, m := range migrations.Migrations.Sorted() {
		if last == "" || m.Name <= last {
			set.Add(m)
		}
	}
	mg := migrate.NewMigrator(db, set)
	if err := mg.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mg.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

func freeze(t *testing.T, engine string, open func(t *testing.T) *bun.DB) {
	t.Helper()
	for _, c := range []struct{ file, last string }{
		{"bun_full.sql", ""},
		{"bun_20260711000001.sql", "20260711000001"},
	} {
		ctx := context.Background()
		db := open(t)
		rec := &recorder{}
		db.AddQueryHook(rec)
		migrateUpTo(t, ctx, db, c.last)
		seed(t, ctx, db, c.last == "")

		var b strings.Builder
		b.WriteString("-- Frozen from the Bun-era code by internal/infra/db/freeze_dump_test.go\n")
		b.WriteString("-- (every statement Bun executed except SELECTs). Do not edit.\n")
		for _, s := range rec.stmts {
			b.WriteString(s)
			b.WriteString(";\n")
		}
		dir := filepath.Join("migrate", "testdata", engine)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.file), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFreezeBunDumps(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "freeze"})
	freeze(t, "sqlite", func(t *testing.T) *bun.DB {
		sqldb, err := setupSQLite("sqlite://"+filepath.Join(t.TempDir(), "freeze.sqlite"), log)
		if err != nil {
			t.Fatal(err)
		}
		db := bun.NewDB(sqldb, sqlitedialect.New())
		t.Cleanup(func() { _ = db.Close() })
		return db
	})

	base := os.Getenv("WF_TEST_DATABASE_URL")
	if base == "" {
		t.Fatal("WF_TEST_DATABASE_URL must name a Postgres server for the pgsql dumps")
	}
	freeze(t, "pgsql", func(t *testing.T) *bun.DB {
		var rb [6]byte
		if _, err := rand.Read(rb[:]); err != nil {
			t.Fatal(err)
		}
		schema := "freeze_" + hex.EncodeToString(rb[:])
		admin, err := setupPostgreSQL(base, log)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE")
			_ = admin.Close()
		})
		u, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		sqldb, err := setupPostgreSQL(utcDSN(u.String()), log)
		if err != nil {
			t.Fatal(err)
		}
		db := bun.NewDB(sqldb, pgdialect.New())
		t.Cleanup(func() { _ = db.Close() })
		return db
	})
}
```

- [ ] **Step 2: Confirm it is invisible to the normal build**

Run: `go vet ./internal/infra/db/ && go vet -tags freezedump ./internal/infra/db/ && go test ./internal/infra/db/`
Expected: no output from vet; `ok  winterflow/internal/infra/db` (the tagged file is not compiled without `-tags freezedump`).

- [ ] **Step 3: Produce the dumps (SQLite and Postgres)**

```bash
cid="$(docker run -d --rm -p 127.0.0.1:0:5432 -e POSTGRES_PASSWORD=postgres postgres:16)"
port="$(docker port "$cid" 5432/tcp | head -n1 | sed 's/.*://')"
until docker exec "$cid" pg_isready -U postgres -h 127.0.0.1 >/dev/null 2>&1; do sleep 0.5; done
WF_TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:${port}/postgres?sslmode=disable" \
  go test -tags freezedump -run TestFreezeBunDumps -count=1 ./internal/infra/db/
docker rm -f "$cid"
wc -l internal/infra/db/migrate/testdata/*/*.sql
grep -c '^INSERT INTO bun_migrations' internal/infra/db/migrate/testdata/*/*.sql
sha256sum internal/infra/db/migrate/testdata/*/*.sql
```

Expected: `ok  winterflow/internal/infra/db`; line counts `158 pgsql/bun_20260711000001.sql`, `179 pgsql/bun_full.sql`, `158 sqlite/bun_20260711000001.sql`, `193 sqlite/bun_full.sql`; `bun_migrations` inserts 5 / 8 / 5 / 8. At plan time the files hashed to (any difference means the Bun code changed since; review the diff before committing):

```
b827728dc18645c116bf7254b6b1f9cad2eaa0d977778121b39e08262ffe77e8  internal/infra/db/migrate/testdata/pgsql/bun_20260711000001.sql
4cc90058785d0557fba70f8c231cdb48e8e9ce87caa7be828ff97dea8f5174ad  internal/infra/db/migrate/testdata/pgsql/bun_full.sql
f3fc96a37df32937c7c2504c1492972259d82f5ae413a1d82d93d196bc02979d  internal/infra/db/migrate/testdata/sqlite/bun_20260711000001.sql
f00a77864632c663d4a9fbe549aac8a41640abbf72bcdf78971c24441a241ddc  internal/infra/db/migrate/testdata/sqlite/bun_full.sql
```

Spot-check `internal/infra/db/migrate/testdata/sqlite/bun_full.sql`: it starts with `CREATE TABLE IF NOT EXISTS bun_migrations ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, ...`, contains the `app_domains_new` rebuild (`DROP TABLE IF EXISTS app_domains_new;` … `ALTER TABLE app_domains_new RENAME TO app_domains;`) and no `BEGIN`/`COMMIT`; the Postgres files contain `ALTER TABLE app_domains DROP CONSTRAINT app_domains_pkey, ADD PRIMARY KEY (server_id, domain);` only in `bun_full.sql`.

- [ ] **Step 4: Prove the dumps are reproducible**

Run Step 3's commands a second time.
Expected: `sha256sum` prints exactly the same four hashes as the first run.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/db/freeze_dump_test.go internal/infra/db/migrate/testdata
git commit -m "test(db): freeze Bun-era databases for the migration adoption tests

Replay logs of the current Bun migrator (all eight migrations, and only up
to 20260711000001) plus one seed row per table, on SQLite and Postgres.
The sqlc migration runner must adopt both without re-running anything.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Migration runner, SQL migrations, adoption tests

**Files:**
- Create: `internal/infra/db/backend/backend.go`
- Create: `internal/infra/db/migrations/embed.go`
- Create: `internal/infra/db/migrations/sqlite/{20241113120001_initial_schema,20260703000001_pat_tokens,20260703000002_user_credentials,20260703000003_org_identity,20260711000001_app_domains,20261010000001_app_domains_uuid,20261010000002_app_domains_per_server,20261010000003_server_domains}.sql`
- Create: the same eight names under `internal/infra/db/migrations/pgsql/`
- Create: `internal/infra/db/migrate/split.go`, `internal/infra/db/migrate/migrate.go`
- Test: `internal/infra/db/migrate/split_test.go`, `helpers_test.go`, `migrate_test.go`, `adopt_test.go`, `concurrent_test.go`
- Modify: `scripts/test-pg.sh`, `Makefile` (comment of `test-pg`), `.github/workflows/ci.yml` (`contract` job), `.gitignore`, `go.mod`

**Interfaces:**
- Consumes: Task 1's dumps in `internal/infra/db/migrate/testdata/`.
- Produces:
  - `package backend`: `const SQLite = "sqlite"`, `const Postgres = "postgres"`; `type DBTX interface { ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error); PrepareContext(ctx context.Context, query string) (*sql.Stmt, error); QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error); QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row }`
  - `package migrations`: `func FS(driver string) (fs.FS, error)` (rooted at the engine directory).
  - `package migrate`: `type Migration struct { Version int64; Name string; SQL string; FKOff bool }`; `func Load(fsys fs.FS) ([]Migration, error)`; `func Migrate(ctx context.Context, db *sql.DB, driverName string) error`; `func MigrateTo(ctx context.Context, db *sql.DB, driverName string, version int64) error`; `func Run(ctx context.Context, db *sql.DB, driverName string, fsys fs.FS, target int64) error` (`target` -1 = all); `func Split(sqlText string) []string`.

The SQL files are direct translations of each Bun migration's dialect branch (same DDL, whitespace normalized); the Bun-era hand-rolled transaction in `20261010000002` is gone — atomicity is the runner's job. The Postgres-only `20261010000001` has a comment-only SQLite counterpart so both engines share one version list.

- [ ] **Step 1: Write the splitter test**

Create `internal/infra/db/migrate/split_test.go`:

```go
package migrate

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := map[string]struct {
		in   string
		want []string
	}{
		"plain":            {"CREATE TABLE a (x INT);\nCREATE TABLE b (y INT);\n", []string{"CREATE TABLE a (x INT)", "CREATE TABLE b (y INT)"}},
		"no trailing ;":    {"DROP TABLE a", []string{"DROP TABLE a"}},
		"single quote":     {"INSERT INTO t VALUES ('a;b');", []string{"INSERT INTO t VALUES ('a;b')"}},
		"escaped quote":    {"INSERT INTO t VALUES ('it''s; ok');", []string{"INSERT INTO t VALUES ('it''s; ok')"}},
		"double quote":     {`CREATE TABLE "a;b" (x INT);`, []string{`CREATE TABLE "a;b" (x INT)`}},
		"line comment":     {"-- drop; everything\nDROP TABLE a;", []string{"-- drop; everything\nDROP TABLE a"}},
		"block comment":    {"/* a; b */ DROP TABLE a;", []string{"/* a; b */ DROP TABLE a"}},
		"comment only":     {"-- nothing to do\n", nil},
		"trailing notes":   {"DROP TABLE a;\n-- done; really\n", []string{"DROP TABLE a"}},
		"empty pieces":     {";;\n;", nil},
		"quote in comment": {"-- don't\nDROP TABLE a;", []string{"-- don't\nDROP TABLE a"}},
	}
	for name, c := range cases {
		if got := Split(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Split(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/infra/db/migrate/ -run TestSplit`
Expected: build failure `undefined: Split`.

- [ ] **Step 3: Implement the splitter**

Create `internal/infra/db/migrate/split.go`:

```go
package migrate

import "strings"

// Split cuts a migration into statements on top-level semicolons. Semicolons
// inside '...' strings, "..." identifiers, -- line comments and /* */ block
// comments do not split. Pieces holding nothing but comments and whitespace
// are dropped. Postgres dollar quoting ($$) is not supported; no migration
// uses it.
func Split(sqlText string) []string {
	var (
		out     []string
		b       strings.Builder
		hasCode bool
		inSQ    bool
		inDQ    bool
		inLine  bool
		inBlock bool
	)
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" && hasCode {
			out = append(out, s)
		}
		b.Reset()
		hasCode = false
	}
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		next := byte(0)
		if i+1 < len(sqlText) {
			next = sqlText[i+1]
		}
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
			}
		case inBlock:
			if c == '*' && next == '/' {
				b.WriteByte(c)
				i++
				c = next
				inBlock = false
			}
		case inSQ:
			if c == '\'' {
				inSQ = false
			}
		case inDQ:
			if c == '"' {
				inDQ = false
			}
		case c == '-' && next == '-':
			inLine = true
		case c == '/' && next == '*':
			inBlock = true
		case c == '\'':
			inSQ, hasCode = true, true
		case c == '"':
			inDQ, hasCode = true, true
		case c == ';':
			flush()
			continue
		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				hasCode = true
			}
		}
		b.WriteByte(c)
	}
	flush()
	return out
}
```

Run: `go test ./internal/infra/db/migrate/ -run TestSplit -v`
Expected: `--- PASS: TestSplit`.

- [ ] **Step 4: Add the driver constants, the SQL migrations and their embed**

Create `internal/infra/db/backend/backend.go`:

```go
// Package backend holds what every engine shares: the driver names, the
// DBTX execution surface sqlc-generated code runs on, and the
// savepoint-aware transaction manager (tx.go).
package backend

import (
	"context"
	"database/sql"
)

// Driver names. db.Open picks one from the DATABASE_URL scheme; every
// repository constructor switches on it once.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
)

// DBTX is the execution surface of sqlc-generated code. *sql.DB, *sql.Tx and
// *sql.Conn all satisfy it.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
```

Create `internal/infra/db/migrations/embed.go` (it shares package `migrations` with the Bun Go migrations until Task 10 deletes them):

```go
package migrations

import (
	"embed"
	"fmt"
	"io/fs"

	"winterflow/internal/infra/db/backend"
)

// Per-engine SQL migrations, <version>_<name>.sql. The same directories are
// sqlc's schema input (internal/infra/db/sqlc/sqlc.yaml).
//
//go:embed sqlite/*.sql pgsql/*.sql
var files embed.FS

// FS returns the migration directory for a driver (backend.SQLite or
// backend.Postgres), rooted so its entries are the .sql files.
func FS(driver string) (fs.FS, error) {
	switch driver {
	case backend.SQLite:
		return fs.Sub(files, "sqlite")
	case backend.Postgres:
		return fs.Sub(files, "pgsql")
	}
	return nil, fmt.Errorf("migrations: unknown driver %q", driver)
}
```

SQLite migrations, `internal/infra/db/migrations/sqlite/`:

`20241113120001_initial_schema.sql`
```sql
-- Initial schema (ported from the Bun migration 20241113120001_initial_schema_fixed).

CREATE TABLE IF NOT EXISTS organizations (
    organization_id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    user_id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    avatar TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS organization_users (
    organization_id VARCHAR(36) NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member', 'billing')),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (organization_id, user_id),
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_tokens (
    token_id VARCHAR(36) PRIMARY KEY,
    token VARCHAR(32) NOT NULL UNIQUE,
    token_type VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
    user_id VARCHAR(36),
    expires_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_connected_accounts (
    provider VARCHAR(16) NOT NULL,
    external_id TEXT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    PRIMARY KEY (provider, external_id),
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS servers (
    server_id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    name VARCHAR(64) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen TIMESTAMP,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_capabilities (
    server_id VARCHAR(36) NOT NULL,
    name VARCHAR(64) NOT NULL,
    value TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_features (
    server_id VARCHAR(36) NOT NULL,
    name VARCHAR(64) NOT NULL,
    is_enabled BOOLEAN NOT NULL,
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_registrations (
    server_id VARCHAR(36) PRIMARY KEY,
    certificate_id VARCHAR(36) NOT NULL,
    hostname TEXT NOT NULL,
    code VARCHAR(6) NOT NULL UNIQUE,
    expires_at TIMESTAMP NOT NULL,
    certificate TEXT NOT NULL,
    certificate_expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS server_certificates (
    certificate_id VARCHAR(36) PRIMARY KEY,
    server_id VARCHAR(36) NOT NULL,
    certificate TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS apps (
    app_id VARCHAR(36) PRIMARY KEY,
    server_id VARCHAR(36) NOT NULL,
    template_id VARCHAR(36),
    name VARCHAR(255) NOT NULL,
    version VARCHAR(128) NOT NULL DEFAULT '',
    icon VARCHAR(64) NOT NULL,
    color VARCHAR(7) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_tokens_token ON user_tokens (token);
CREATE INDEX IF NOT EXISTS idx_user_connected_accounts_user_id ON user_connected_accounts (user_id);
CREATE INDEX IF NOT EXISTS idx_server_registrations_code ON server_registrations (code);
CREATE INDEX IF NOT EXISTS idx_server_registrations_expires_at ON server_registrations (expires_at);
CREATE INDEX IF NOT EXISTS idx_server_certificates_server_id ON server_certificates (server_id);
CREATE INDEX IF NOT EXISTS idx_apps_server_id ON apps (server_id);
CREATE INDEX IF NOT EXISTS idx_apps_template_id ON apps (template_id);
CREATE INDEX IF NOT EXISTS idx_server_certificates_is_active_expires_at ON server_certificates (is_active, expires_at);
```

`20260703000001_pat_tokens.sql`
```sql
-- Replace the never-used v1 user_tokens table with the PAT shape: hashed
-- token, display prefix, name, last-used. The v1 table never held rows.

DROP TABLE IF EXISTS user_tokens;

CREATE TABLE user_tokens (
    token_id     VARCHAR(36) PRIMARY KEY,
    user_id      VARCHAR(36) NOT NULL,
    name         VARCHAR(64) NOT NULL,
    token_prefix VARCHAR(16) NOT NULL,
    token_hash   VARCHAR(64) NOT NULL UNIQUE,
    token_type   VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
    expires_at   TIMESTAMP,
    last_used_at TIMESTAMP,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_tokens_user_id ON user_tokens (user_id);
```

`20260703000002_user_credentials.sql`
```sql
-- Local (email + password) credentials: one row per local user, bcrypt hash only.

CREATE TABLE IF NOT EXISTS user_credentials (
    user_id              VARCHAR(36) PRIMARY KEY,
    email                VARCHAR(255) NOT NULL UNIQUE,
    password_hash        VARCHAR(100) NOT NULL,
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);
```

`20260703000003_org_identity.sql`
```sql
-- Organizations get a visual identity (icon + color, same vocabulary as apps).

ALTER TABLE organizations ADD COLUMN icon VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN color VARCHAR(7) NOT NULL DEFAULT '';
```

`20260711000001_app_domains.sql`
```sql
-- The rebuildable ingress index: one row per hostname an app claims (routes
-- plus domain-level redirect sources). The key is the hostname alone here;
-- 20261010000002 moves it to (server_id, domain).

CREATE TABLE app_domains (
    domain VARCHAR(253) NOT NULL PRIMARY KEY,
    app_id CHAR(36) NOT NULL,
    server_id CHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    ssl BOOLEAN NOT NULL DEFAULT FALSE,
    upstream_port INTEGER NOT NULL DEFAULT 0,
    target VARCHAR(2048) NOT NULL DEFAULT '',
    code INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_app_domains_app_id ON app_domains (app_id);
CREATE INDEX idx_app_domains_server_id ON app_domains (server_id);
```

`20261010000001_app_domains_uuid.sql`
```sql
-- Postgres retypes app_domains.app_id/server_id to UUID here. SQLite has no
-- UUID type: nothing to do (the version is kept so both engines share one
-- version list).
```

`20261010000002_app_domains_per_server.sql` (the marker must stay the first line)
```sql
-- migrate:foreign_keys=off
-- A hostname becomes unique per server instead of globally: the key moves
-- from (domain) to (server_id, domain). SQLite cannot alter a primary key, so
-- the table is rebuilt; the runner runs this file in one transaction with
-- foreign keys off and checks PRAGMA foreign_key_check before committing.

DROP TABLE IF EXISTS app_domains_new;

CREATE TABLE app_domains_new (
    domain VARCHAR(253) NOT NULL,
    app_id CHAR(36) NOT NULL,
    server_id CHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    ssl BOOLEAN NOT NULL DEFAULT FALSE,
    upstream_port INTEGER NOT NULL DEFAULT 0,
    target VARCHAR(2048) NOT NULL DEFAULT '',
    code INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (server_id, domain)
);

INSERT INTO app_domains_new (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
SELECT domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at FROM app_domains;

DROP TABLE app_domains;

ALTER TABLE app_domains_new RENAME TO app_domains;

CREATE INDEX idx_app_domains_app_id ON app_domains (app_id);
CREATE INDEX idx_app_domains_server_id ON app_domains (server_id);
```

`20261010000003_server_domains.sql`
```sql
-- The API's index of each agent's domain registrations (the agent's domains/
-- dir is the source of truth), rebuilt from domain.list. Timestamps are plain
-- TIMESTAMP on both engines, as the Bun migration created them.

CREATE TABLE server_domains (
    server_id VARCHAR(36) NOT NULL,
    name VARCHAR(253) NOT NULL,
    tls VARCHAR(16) NOT NULL,
    not_after TIMESTAMP,
    issuer VARCHAR(255) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_server_domains_name ON server_domains (name);
```

Postgres migrations, `internal/infra/db/migrations/pgsql/`:

`20241113120001_initial_schema.sql`
```sql
-- Initial schema (ported from the Bun migration 20241113120001_initial_schema_fixed).

CREATE TABLE IF NOT EXISTS organizations (
    organization_id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
    user_id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    avatar TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS organization_users (
    organization_id UUID NOT NULL,
    user_id UUID NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member', 'billing')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, user_id),
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_tokens (
    token_id UUID PRIMARY KEY,
    token VARCHAR(32) NOT NULL UNIQUE,
    token_type VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
    user_id UUID,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_connected_accounts (
    provider VARCHAR(16) NOT NULL,
    external_id TEXT NOT NULL,
    user_id UUID NOT NULL,
    PRIMARY KEY (provider, external_id),
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS servers (
    server_id UUID PRIMARY KEY,
    organization_id UUID NOT NULL,
    name VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen TIMESTAMPTZ,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_capabilities (
    server_id UUID NOT NULL,
    name VARCHAR(64) NOT NULL,
    value TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_features (
    server_id UUID NOT NULL,
    name VARCHAR(64) NOT NULL,
    is_enabled BOOLEAN NOT NULL,
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_registrations (
    server_id UUID PRIMARY KEY,
    certificate_id UUID NOT NULL,
    hostname TEXT NOT NULL,
    code VARCHAR(6) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    certificate TEXT NOT NULL,
    certificate_expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS server_certificates (
    certificate_id UUID PRIMARY KEY,
    server_id UUID NOT NULL,
    certificate TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS apps (
    app_id UUID PRIMARY KEY,
    server_id UUID NOT NULL,
    template_id UUID,
    name VARCHAR(255) NOT NULL,
    version VARCHAR(128) NOT NULL DEFAULT '',
    icon VARCHAR(64) NOT NULL,
    color VARCHAR(7) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_tokens_token ON user_tokens (token);
CREATE INDEX IF NOT EXISTS idx_user_connected_accounts_user_id ON user_connected_accounts (user_id);
CREATE INDEX IF NOT EXISTS idx_server_registrations_code ON server_registrations (code);
CREATE INDEX IF NOT EXISTS idx_server_registrations_expires_at ON server_registrations (expires_at);
CREATE INDEX IF NOT EXISTS idx_server_certificates_server_id ON server_certificates (server_id);
CREATE INDEX IF NOT EXISTS idx_apps_server_id ON apps (server_id);
CREATE INDEX IF NOT EXISTS idx_apps_template_id ON apps (template_id);
CREATE INDEX IF NOT EXISTS idx_server_certificates_is_active_expires_at ON server_certificates (is_active, expires_at);
```

`20260703000001_pat_tokens.sql`
```sql
-- Replace the never-used v1 user_tokens table with the PAT shape: hashed
-- token, display prefix, name, last-used. The v1 table never held rows.

DROP TABLE IF EXISTS user_tokens;

CREATE TABLE user_tokens (
    token_id     UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    name         VARCHAR(64) NOT NULL,
    token_prefix VARCHAR(16) NOT NULL,
    token_hash   VARCHAR(64) NOT NULL UNIQUE,
    token_type   VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_tokens_user_id ON user_tokens (user_id);
```

`20260703000002_user_credentials.sql`
```sql
-- Local (email + password) credentials: one row per local user, bcrypt hash only.

CREATE TABLE IF NOT EXISTS user_credentials (
    user_id              UUID PRIMARY KEY,
    email                VARCHAR(255) NOT NULL UNIQUE,
    password_hash        VARCHAR(100) NOT NULL,
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);
```

`20260703000003_org_identity.sql`
```sql
-- Organizations get a visual identity (icon + color, same vocabulary as apps).

ALTER TABLE organizations ADD COLUMN icon VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN color VARCHAR(7) NOT NULL DEFAULT '';
```

`20260711000001_app_domains.sql` (CHAR(36) and plain TIMESTAMP on Postgres too, exactly as Bun created it)
```sql
-- The rebuildable ingress index: one row per hostname an app claims (routes
-- plus domain-level redirect sources). The key is the hostname alone here;
-- 20261010000002 moves it to (server_id, domain).

CREATE TABLE app_domains (
    domain VARCHAR(253) NOT NULL PRIMARY KEY,
    app_id CHAR(36) NOT NULL,
    server_id CHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    ssl BOOLEAN NOT NULL DEFAULT FALSE,
    upstream_port INTEGER NOT NULL DEFAULT 0,
    target VARCHAR(2048) NOT NULL DEFAULT '',
    code INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_app_domains_app_id ON app_domains (app_id);
CREATE INDEX idx_app_domains_server_id ON app_domains (server_id);
```

`20261010000001_app_domains_uuid.sql`
```sql
-- app_domains.app_id/server_id were CHAR(36) while apps.app_id and
-- servers.server_id are UUID, so the claim join failed with "operator does
-- not exist: uuid = character". app_domains is a rebuildable index, so rows
-- whose ids are not UUIDs are deleted first: one such row would make the
-- cast fail and crashloop startup.

DELETE FROM app_domains
WHERE app_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
   OR server_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

ALTER TABLE app_domains
    ALTER COLUMN app_id TYPE UUID USING app_id::uuid,
    ALTER COLUMN server_id TYPE UUID USING server_id::uuid;
```

`20261010000002_app_domains_per_server.sql`
```sql
-- A hostname becomes unique per server instead of globally: the key moves
-- from (domain) to (server_id, domain). Every existing row satisfies it.

ALTER TABLE app_domains DROP CONSTRAINT app_domains_pkey, ADD PRIMARY KEY (server_id, domain);
```

`20261010000003_server_domains.sql` (UUID `server_id`, plain `TIMESTAMP` columns, as Bun created them)
```sql
-- The API's index of each agent's domain registrations (the agent's domains/
-- dir is the source of truth), rebuilt from domain.list. Timestamps are plain
-- TIMESTAMP on both engines, as the Bun migration created them.

CREATE TABLE server_domains (
    server_id UUID NOT NULL,
    name VARCHAR(253) NOT NULL,
    tls VARCHAR(16) NOT NULL,
    not_after TIMESTAMP,
    issuer VARCHAR(255) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_server_domains_name ON server_domains (name);
```

- [ ] **Step 5: Write the runner tests**

Create `internal/infra/db/migrate/helpers_test.go` (unmigrated databases with production-like session settings; the dump loader; the normalized schema used to compare an adopted database with a fresh install):

```go
package migrate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"winterflow/internal/infra/db/backend"
)

// engines lists the drivers this run exercises: SQLite always, Postgres when
// WF_TEST_DATABASE_URL names a server (make test-pg, CI's contract job).
func engines(t *testing.T) []string {
	t.Helper()
	if os.Getenv("WF_TEST_DATABASE_URL") == "" {
		return []string{backend.SQLite}
	}
	return []string{backend.SQLite, backend.Postgres}
}

// openEmpty opens an empty, unmigrated database with production-like
// session settings: SQLite with foreign keys on and one connection;
// Postgres in a fresh schema with the session pinned to UTC.
func openEmpty(t *testing.T, driverName string) *sql.DB {
	t.Helper()
	if driverName == backend.SQLite {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "m.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	base := os.Getenv("WF_TEST_DATABASE_URL")
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "m_" + hex.EncodeToString(b[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rebind turns ? placeholders into $n for Postgres.
func rebind(driverName, q string) string {
	if driverName != backend.Postgres {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

func mustExec(t *testing.T, db *sql.DB, driverName, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), rebind(driverName, q), args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func count(t *testing.T, db *sql.DB, driverName, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), rebind(driverName, q), args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func versions(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// loadDump replays a frozen Bun-era dump (testdata/<engine>/<file>).
func loadDump(t *testing.T, db *sql.DB, driverName, file string) {
	t.Helper()
	dir := "sqlite"
	if driverName == backend.Postgres {
		dir = "pgsql"
	}
	b, err := os.ReadFile(filepath.Join("testdata", dir, file))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range Split(string(b)) {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("replay %s: %v\n%s", file, err, s)
		}
	}
}

// schemaOf is a normalized description of every table, column, constraint
// and index outside the migration bookkeeping, comparable across a fresh
// install and an adopted Bun-era database.
func schemaOf(t *testing.T, db *sql.DB, driverName string) []string {
	t.Helper()
	var queries []string
	if driverName == backend.SQLite {
		// sqlite_master.sql with whitespace collapsed: column types,
		// defaults, keys, CHECKs and foreign keys all live in it.
		queries = []string{`SELECT type || ' ' || name || ' ' || tbl_name || ' ' || COALESCE(sql, '')
			FROM sqlite_master
			WHERE name NOT LIKE 'sqlite_%' AND name NOT LIKE 'bun_%' AND name <> 'schema_migrations'
			ORDER BY type, name`}
	} else {
		queries = []string{
			`SELECT table_name || '.' || column_name || ' ' || ordinal_position || ' ' || data_type || ' ' ||
				COALESCE(character_maximum_length::text, '-') || ' ' || is_nullable || ' ' || COALESCE(column_default, '-')
			FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name NOT LIKE 'bun_%' AND table_name <> 'schema_migrations'
			ORDER BY table_name, ordinal_position`,
			`SELECT conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
			FROM pg_constraint
			WHERE connamespace = current_schema()::regnamespace
			  AND conrelid::regclass::text NOT LIKE 'bun_%' AND conrelid::regclass::text <> 'schema_migrations'
			ORDER BY 1`,
			`SELECT tablename || ' ' || indexname || ' ' || replace(indexdef, current_schema() || '.', '')
			FROM pg_indexes
			WHERE schemaname = current_schema() AND tablename NOT LIKE 'bun_%' AND tablename <> 'schema_migrations'
			ORDER BY 1`,
		}
	}
	var out []string
	for _, q := range queries {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, strings.Join(strings.Fields(s), " "))
		}
		rows.Close()
	}
	return out
}

// sameLines fails t with the lines only one side has.
func sameLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	in := func(xs []string) map[string]bool {
		m := make(map[string]bool, len(xs))
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	g, w := in(got), in(want)
	for _, x := range got {
		if !w[x] {
			t.Errorf("%s: unexpected %q", what, x)
		}
	}
	for _, x := range want {
		if !g[x] {
			t.Errorf("%s: missing %q", what, x)
		}
	}
	if len(want) == 0 {
		t.Errorf("%s: empty", what)
	}
}
```

Create `internal/infra/db/migrate/migrate_test.go`:

```go
package migrate

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/migrations"
)

func TestEnginesShareVersions(t *testing.T) {
	var lists [2][]int64
	for i, d := range []string{backend.SQLite, backend.Postgres} {
		fsys, err := migrations.FS(d)
		if err != nil {
			t.Fatal(err)
		}
		migs, err := Load(fsys)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range migs {
			lists[i] = append(lists[i], m.Version)
		}
	}
	if len(lists[0]) == 0 || !reflect.DeepEqual(lists[0], lists[1]) {
		t.Fatalf("sqlite versions %v != pgsql versions %v", lists[0], lists[1])
	}
}

func TestLoadRejectsBadNames(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"no version": {"init.sql": {Data: []byte("SELECT 1;")}},
		"duplicate":  {"1_a.sql": {Data: []byte("SELECT 1;")}, "1_b.sql": {Data: []byte("SELECT 1;")}},
	} {
		if _, err := Load(fsys); err == nil {
			t.Errorf("%s: Load accepted it", name)
		}
	}
}

func TestMigrateFreshIsIdempotent(t *testing.T) {
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := context.Background()
			db := openEmpty(t, d)
			if err := Migrate(ctx, db, d); err != nil {
				t.Fatal(err)
			}
			first := versions(t, db)
			if err := Migrate(ctx, db, d); err != nil {
				t.Fatalf("second run: %v", err)
			}
			if got := versions(t, db); !reflect.DeepEqual(got, first) || len(first) == 0 {
				t.Fatalf("versions %v then %v", first, got)
			}
		})
	}
}

func TestMigrateToStopsAtVersion(t *testing.T) {
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := context.Background()
			db := openEmpty(t, d)
			if err := MigrateTo(ctx, db, d, 20260711000001); err != nil {
				t.Fatal(err)
			}
			got := versions(t, db)
			if got[len(got)-1] != 20260711000001 {
				t.Fatalf("versions = %v", got)
			}
			if err := MigrateTo(ctx, db, d, 1); err == nil {
				t.Fatal("unknown target accepted")
			}
		})
	}
}

// A statement failing mid-file leaves neither the earlier statements'
// effects nor the version row.
func TestFailedMigrationRollsBack(t *testing.T) {
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := context.Background()
			db := openEmpty(t, d)
			fsys := fstest.MapFS{
				"1_ok.sql":     {Data: []byte("CREATE TABLE kept (x INT);")},
				"2_broken.sql": {Data: []byte("CREATE TABLE half (x INT);\nINSERT INTO half VALUES (1);\nINSERT INTO missing VALUES (1);")},
			}
			err := Run(ctx, db, d, fsys, -1)
			if err == nil || !strings.Contains(err.Error(), "2_broken") || !strings.Contains(err.Error(), "INSERT INTO missing") {
				t.Fatalf("err = %v; want the migration name and the failing statement", err)
			}
			if got := versions(t, db); !reflect.DeepEqual(got, []int64{1}) {
				t.Fatalf("versions = %v, want [1]", got)
			}
			if n := count(t, db, d, tableCountSQL(d), "half"); n != 0 {
				t.Fatal("the failed migration's table survived")
			}
		})
	}
}

func tableCountSQL(d string) string {
	if d == backend.Postgres {
		return `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?`
	}
	return `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
}

// The foreign_keys=off rebuild: a foreign_key_check violation rolls the
// whole file back, and foreign keys are on again afterwards either way.
func TestForeignKeysOffRebuild(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t, backend.SQLite)
	base := fstest.MapFS{
		"1_parent.sql": {Data: []byte("CREATE TABLE parent (id INT PRIMARY KEY);\nCREATE TABLE child (pid INT REFERENCES parent (id));\nINSERT INTO parent VALUES (1);\nINSERT INTO child VALUES (1);")},
	}
	if err := Run(ctx, db, backend.SQLite, base, -1); err != nil {
		t.Fatal(err)
	}
	broken := fstest.MapFS{
		"1_parent.sql": base["1_parent.sql"],
		// Drops the parent row the child points at: the check must fail.
		"2_rebuild.sql": {Data: []byte(fkOffMarker + "\nCREATE TABLE parent_new (id INT PRIMARY KEY);\nDROP TABLE parent;\nALTER TABLE parent_new RENAME TO parent;")},
	}
	if err := Run(ctx, db, backend.SQLite, broken, -1); err == nil || !strings.Contains(err.Error(), "foreign_key_check") {
		t.Fatalf("err = %v, want a foreign_key_check failure", err)
	}
	if n := count(t, db, backend.SQLite, `SELECT count(*) FROM parent`); n != 1 {
		t.Fatalf("parent rows after rollback = %d, want 1", n)
	}
	if got := versions(t, db); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("versions = %v", got)
	}
	if n := count(t, db, backend.SQLite, `SELECT foreign_keys FROM pragma_foreign_keys`); n != 1 {
		t.Fatal("foreign keys left off after a failed rebuild")
	}

	good := fstest.MapFS{
		"1_parent.sql":  base["1_parent.sql"],
		"2_rebuild.sql": {Data: []byte(fkOffMarker + "\nCREATE TABLE parent_new (id INT PRIMARY KEY, note TEXT);\nINSERT INTO parent_new (id) SELECT id FROM parent;\nDROP TABLE parent;\nALTER TABLE parent_new RENAME TO parent;")},
	}
	if err := Run(ctx, db, backend.SQLite, good, -1); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, backend.SQLite, `SELECT foreign_keys FROM pragma_foreign_keys`); n != 1 {
		t.Fatal("foreign keys left off after a rebuild")
	}
}

func TestForeignKeysOffIsSQLiteOnly(t *testing.T) {
	fsys := fstest.MapFS{"1_x.sql": {Data: []byte(fkOffMarker + "\nSELECT 1;")}}
	if err := Run(context.Background(), nil, backend.Postgres, fsys, -1); err == nil {
		t.Fatal("a Postgres migration with the foreign_keys marker was accepted")
	}
}

// The real app_domains rebuild (20261010000002) under a child table that
// references app_domains(domain): the new key no longer makes domain unique,
// so the check fails and nothing is half-applied.
func TestAppDomainsRebuildIsAtomic(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t, backend.SQLite)
	if err := MigrateTo(ctx, db, backend.SQLite, 20261010000001); err != nil {
		t.Fatal(err)
	}
	for _, dom := range []string{"keep.test", "also.test"} {
		mustExec(t, db, backend.SQLite, `INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at) VALUES (?, 'a', 's', 'route', '2026-10-10 12:00:00')`, dom)
	}
	mustExec(t, db, backend.SQLite, `CREATE TABLE app_domains_child (d VARCHAR(253) REFERENCES app_domains (domain))`)
	mustExec(t, db, backend.SQLite, `INSERT INTO app_domains_child (d) VALUES ('keep.test')`)

	if err := Migrate(ctx, db, backend.SQLite); err == nil {
		t.Fatal("rebuild succeeded although foreign_key_check must fail")
	}
	if n := count(t, db, backend.SQLite, `SELECT count(*) FROM app_domains`); n != 2 {
		t.Fatalf("app_domains rows = %d, want 2", n)
	}
	if n := count(t, db, backend.SQLite, `SELECT count(*) FROM sqlite_master WHERE name = 'app_domains_new'`); n != 0 {
		t.Fatal("half-built table survived")
	}
	if _, err := db.Exec(`INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at) VALUES ('keep.test', 'b', 's2', 'route', '2026-10-10 12:00:00')`); err == nil {
		t.Fatal("the key changed despite the rollback")
	}
}

func TestAdoptRejectsUnknownBunVersion(t *testing.T) {
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			db := openEmpty(t, d)
			loadDump(t, db, d, "bun_full.sql")
			mustExec(t, db, d, `INSERT INTO bun_migrations (name, group_id) VALUES ('29990101000001', 2)`)
			err := Migrate(context.Background(), db, d)
			if err == nil || !strings.Contains(err.Error(), "29990101000001") {
				t.Fatalf("err = %v, want the unknown Bun version named", err)
			}
			if n := count(t, db, d, tableCountSQL(d), "schema_migrations"); n != 0 {
				t.Fatal("a failed adoption left schema_migrations behind")
			}
		})
	}
}
```

Create `internal/infra/db/migrate/adopt_test.go` (Task 11 adds the "bun tables are gone" assertion once `drop_bun_tables` exists):

```go
package migrate

import (
	"context"
	"reflect"
	"testing"
)

const (
	frozenOrg   = "0192a3b4-0000-7000-8000-000000000001"
	frozenToken = "0192a3b4-0000-7000-8000-000000000006"
)

// TestAdoptBunDatabase loads a frozen Bun-era database (every migration
// applied, or only those up to 20260711000001), migrates it, and checks
// that adoption recorded every version, re-ran nothing Bun had applied
// (seeded rows survive, the PAT table was not recreated), and left the
// same schema a fresh install has.
func TestAdoptBunDatabase(t *testing.T) {
	for _, d := range engines(t) {
		for _, dump := range []string{"bun_full.sql", "bun_20260711000001.sql"} {
			t.Run(d+"/"+dump, func(t *testing.T) {
				ctx := context.Background()
				fresh := openEmpty(t, d)
				if err := Migrate(ctx, fresh, d); err != nil {
					t.Fatal(err)
				}

				db := openEmpty(t, d)
				loadDump(t, db, d, dump)
				if err := Migrate(ctx, db, d); err != nil {
					t.Fatal(err)
				}
				if got, want := versions(t, db), versions(t, fresh); !reflect.DeepEqual(got, want) {
					t.Fatalf("versions = %v, want %v", got, want)
				}
				if n := count(t, db, d, `SELECT count(*) FROM organizations WHERE organization_id = ? AND name = 'frozen org' AND icon = 'server'`, frozenOrg); n != 1 {
					t.Fatal("seeded organization lost or altered")
				}
				if n := count(t, db, d, `SELECT count(*) FROM user_tokens WHERE token_id = ? AND name = 'ci; deploy'`, frozenToken); n != 1 {
					t.Fatal("seeded token lost: a Bun migration re-ran")
				}
				if n := count(t, db, d, `SELECT count(*) FROM app_domains`); n != 2 {
					t.Fatalf("app_domains rows = %d, want 2", n)
				}
				// Idempotent: a second boot adopts nothing and applies nothing.
				if err := Migrate(ctx, db, d); err != nil {
					t.Fatalf("second Migrate: %v", err)
				}
				sameLines(t, "adopted schema vs fresh install", schemaOf(t, db, d), schemaOf(t, fresh, d))
			})
		}
	}
}

// A build that adopted bun_migrations but crashed before drop_bun_tables
// leaves both tables: the next boot must not adopt again (schema_migrations
// wins) and finishes the pending migrations.
func TestAdoptionIsNotRepeated(t *testing.T) {
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := context.Background()
			db := openEmpty(t, d)
			loadDump(t, db, d, "bun_20260711000001.sql")
			if err := MigrateTo(ctx, db, d, 20260711000001); err != nil {
				t.Fatal(err)
			}
			if got := versions(t, db); len(got) != 5 {
				t.Fatalf("adopted versions = %v, want the 5 Bun applied", got)
			}
			// Bun's bookkeeping would now claim a version this build has
			// not run; a repeated adoption would copy it and skip it.
			mustExec(t, db, d, `INSERT INTO bun_migrations (name, group_id) VALUES ('20261010000003', 2)`)
			if err := Migrate(ctx, db, d); err != nil {
				t.Fatal(err)
			}
			if n := count(t, db, d, tableCountSQL(d), "server_domains"); n != 1 {
				t.Fatal("server_domains missing: adoption ran twice and skipped its migration")
			}
		})
	}
}
```

Create `internal/infra/db/migrate/concurrent_test.go`:

```go
package migrate

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"reflect"
	"sync"
	"testing"

	"winterflow/internal/infra/db/backend"
)

// Several API replicas booting at once against one Postgres: the advisory
// lock serializes them, so every runner succeeds and each version is
// applied once.
func TestConcurrentMigrateIsSerialized(t *testing.T) {
	if os.Getenv("WF_TEST_DATABASE_URL") == "" {
		t.Skip("Postgres only (make test-pg)")
	}
	first := openEmpty(t, backend.Postgres)
	var schema string
	if err := first.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(os.Getenv("WF_TEST_DATABASE_URL"))
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()

	const replicas = 4
	var wg sync.WaitGroup
	errs := make([]error, replicas)
	for i := range replicas {
		db, err := sql.Open("pgx", u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = Migrate(context.Background(), db, backend.Postgres)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d: %v", i, err)
		}
	}
	fresh := openEmpty(t, backend.Postgres)
	if err := Migrate(context.Background(), fresh, backend.Postgres); err != nil {
		t.Fatal(err)
	}
	if got, want := versions(t, first), versions(t, fresh); !reflect.DeepEqual(got, want) {
		t.Fatalf("versions = %v, want %v", got, want)
	}
}
```

- [ ] **Step 6: Run them to see them fail**

Run: `go test ./internal/infra/db/migrate/`
Expected: build failure listing `undefined: Migrate`, `undefined: MigrateTo`, `undefined: Run`, `undefined: Load`, `undefined: fkOffMarker`.

- [ ] **Step 7: Implement the runner**

Create `internal/infra/db/migrate/migrate.go`:

```go
// Package migrate is winterflow's forward-only migration runner. Migrations
// are per-engine SQL files (internal/infra/db/migrations/{sqlite,pgsql}),
// named <version>_<name>.sql. Each pending one runs in one transaction
// together with the insert of its version into schema_migrations, so a
// failure leaves neither its effects nor its bookkeeping behind.
//
// A database last run by a Bun-era build (bun_migrations present,
// schema_migrations absent) is adopted first: the versions Bun applied are
// copied into schema_migrations without re-running them.
package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/migrations"
)

// fkOffMarker, as the first line of a SQLite migration, marks a table
// rebuild: it runs with foreign keys off and must pass PRAGMA
// foreign_key_check before it commits.
const fkOffMarker = "-- migrate:foreign_keys=off"

// pgLockKey serializes concurrent runners (several API replicas booting at
// once) on one Postgres database: pg_advisory_lock("wfmigrat").
const pgLockKey int64 = 0x77666d6967726174

// Migration is one parsed migration file.
type Migration struct {
	Version int64
	Name    string // file name without .sql, e.g. 20241113120001_initial_schema
	SQL     string
	FKOff   bool
}

// Load reads every <version>_<name>.sql in fsys, sorted by version.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: read migrations: %w", err)
	}
	var out []Migration
	seen := map[int64]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".sql")
		digits, _, _ := strings.Cut(name, "_")
		v, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migrate: %s: name must start with a numeric version", e.Name())
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrate: %s and %s share version %d", prev, e.Name(), v)
		}
		seen[v] = e.Name()
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", e.Name(), err)
		}
		text := string(b)
		first, _, _ := strings.Cut(text, "\n")
		out = append(out, Migration{
			Version: v,
			Name:    name,
			SQL:     text,
			FKOff:   strings.TrimSpace(first) == fkOffMarker,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate applies every pending embedded migration for driver.
func Migrate(ctx context.Context, db *sql.DB, driverName string) error {
	return MigrateTo(ctx, db, driverName, -1)
}

// MigrateTo applies pending embedded migrations up to and including version
// (-1: all). Upgrade tests use it to build the schema an older install had.
func MigrateTo(ctx context.Context, db *sql.DB, driverName string, version int64) error {
	fsys, err := migrations.FS(driverName)
	if err != nil {
		return err
	}
	return Run(ctx, db, driverName, fsys, version)
}

// Run applies the pending migrations of fsys up to and including target
// (-1: all) on db.
func Run(ctx context.Context, db *sql.DB, driverName string, fsys fs.FS, target int64) (err error) {
	if driverName != backend.SQLite && driverName != backend.Postgres {
		return fmt.Errorf("migrate: unknown driver %q", driverName)
	}
	migs, err := Load(fsys)
	if err != nil {
		return err
	}
	known := make(map[int64]bool, len(migs))
	for _, m := range migs {
		known[m.Version] = true
		if m.FKOff && driverName != backend.SQLite {
			return fmt.Errorf("migrate: %s: %s is SQLite-only", m.Name, fkOffMarker)
		}
	}
	if target >= 0 && !known[target] {
		return fmt.Errorf("migrate: unknown target version %d", target)
	}

	// One pinned connection for the whole run: the Postgres advisory lock
	// is per session, and SQLite's PRAGMA foreign_keys is per connection
	// (and its pool has a single connection anyway).
	c, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer c.Close()

	if driverName == backend.Postgres {
		if _, err := c.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, pgLockKey); err != nil {
			return fmt.Errorf("migrate: advisory lock: %w", err)
		}
		defer func() {
			if _, uerr := c.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, pgLockKey); uerr != nil {
				// Never return a connection that may still hold the lock
				// to the pool: closing the session releases it.
				_ = c.Raw(func(any) error { return driver.ErrBadConn })
				if err == nil {
					err = fmt.Errorf("migrate: advisory unlock: %w", uerr)
				}
			}
		}()
	}

	if err := adopt(ctx, c, driverName, known); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, c)
	if err != nil {
		return err
	}
	for _, m := range migs {
		if target >= 0 && m.Version > target {
			break
		}
		if applied[m.Version] {
			continue
		}
		if err := apply(ctx, c, driverName, m); err != nil {
			return err
		}
	}
	return nil
}

func schemaMigrationsDDL(driverName string) string {
	if driverName == backend.Postgres {
		return `CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL)`
	}
	return `CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`
}

func recordVersionSQL(driverName string) string {
	if driverName == backend.Postgres {
		return `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, CURRENT_TIMESTAMP)`
	}
	return `INSERT INTO schema_migrations (version, applied_at) VALUES (?, CURRENT_TIMESTAMP)`
}

// querier is what the bookkeeping helpers need from a *sql.Conn or *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func tableExists(ctx context.Context, q querier, driverName, table string) (bool, error) {
	query := `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	if driverName == backend.Postgres {
		query = `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1`
	}
	var n int
	if err := q.QueryRowContext(ctx, query, table).Scan(&n); err != nil {
		return false, fmt.Errorf("migrate: look up table %s: %w", table, err)
	}
	return n > 0, nil
}

func columnExists(ctx context.Context, q querier, driverName, table, column string) (bool, error) {
	query := `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`
	if driverName == backend.Postgres {
		query = `SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`
	}
	var n int
	if err := q.QueryRowContext(ctx, query, table, column).Scan(&n); err != nil {
		return false, fmt.Errorf("migrate: look up column %s.%s: %w", table, column, err)
	}
	return n > 0, nil
}

// adopt creates schema_migrations. On a Bun-era database (bun_migrations
// present, schema_migrations absent) it first copies the version of every
// migration Bun applied, in the same transaction.
func adopt(ctx context.Context, c *sql.Conn, driverName string, known map[int64]bool) error {
	hasSchema, err := tableExists(ctx, c, driverName, "schema_migrations")
	if err != nil {
		return err
	}
	hasBun, err := tableExists(ctx, c, driverName, "bun_migrations")
	if err != nil {
		return err
	}
	if hasSchema || !hasBun {
		if _, err := c.ExecContext(ctx, schemaMigrationsDDL(driverName)); err != nil {
			return fmt.Errorf("migrate: create schema_migrations: %w", err)
		}
		return nil
	}

	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: adopt bun_migrations: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, schemaMigrationsDDL(driverName)); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	query := `SELECT name FROM bun_migrations`
	withAt, err := columnExists(ctx, tx, driverName, "bun_migrations", "migrated_at")
	if err != nil {
		return err
	}
	if withAt {
		query += ` WHERE migrated_at IS NOT NULL`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("migrate: read bun_migrations: %w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: read bun_migrations: %w", err)
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate: read bun_migrations: %w", err)
	}
	seen := map[int64]bool{}
	for _, name := range names {
		v, err := bunVersion(name)
		if err != nil {
			return err
		}
		if !known[v] {
			return fmt.Errorf("migrate: bun_migrations records %q, which has no SQL migration in this build", name)
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		if _, err := tx.ExecContext(ctx, recordVersionSQL(driverName), v); err != nil {
			return fmt.Errorf("migrate: adopt bun migration %q: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: adopt bun_migrations: commit: %w", err)
	}
	return nil
}

// bunVersion is the leading integer of a Bun migration name
// ("20241113120001", or "20241113120001_initial_schema_fixed").
func bunVersion(name string) (int64, error) {
	end := 0
	for end < len(name) && name[end] >= '0' && name[end] <= '9' {
		end++
	}
	v, err := strconv.ParseInt(name[:end], 10, 64)
	if end == 0 || err != nil {
		return 0, fmt.Errorf("migrate: bun_migrations records %q, which has no numeric version", name)
	}
	return v, nil
}

func appliedVersions(ctx context.Context, c *sql.Conn) (map[int64]bool, error) {
	rows, err := c.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
		}
		out[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}
	return out, nil
}

// apply runs one migration and records its version in one transaction.
func apply(ctx context.Context, c *sql.Conn, driverName string, m Migration) (err error) {
	if m.FKOff {
		// A no-op inside a transaction, so it is switched before BEGIN and
		// restored afterwards whatever happens.
		if _, err := c.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return fmt.Errorf("migrate: %s: disable foreign keys: %w", m.Name, err)
		}
		defer func() {
			if _, ferr := c.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`); ferr != nil && err == nil {
				err = fmt.Errorf("migrate: %s: restore foreign keys: %w", m.Name, ferr)
			}
		}()
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: %s: begin: %w", m.Name, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range Split(m.SQL) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %s: %w\n--- statement ---\n%s", m.Name, err, stmt)
		}
	}
	if m.FKOff {
		if err := foreignKeyCheck(ctx, tx); err != nil {
			return fmt.Errorf("migrate: %s: %w", m.Name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, recordVersionSQL(driverName), m.Version); err != nil {
		return fmt.Errorf("migrate: %s: record version: %w", m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: %s: commit: %w", m.Name, err)
	}
	return nil
}

// foreignKeyCheck fails when PRAGMA foreign_key_check reports any row (or
// cannot run, e.g. a parent key that is no longer unique).
func foreignKeyCheck(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()
	var broken []string
	for rows.Next() {
		var table, parent string
		var rowid, fkid any
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("foreign_key_check: %w", err)
		}
		broken = append(broken, fmt.Sprintf("%s(rowid %v) -> %s", table, rowid, parent))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	if len(broken) > 0 {
		return errors.New("foreign_key_check failed: " + strings.Join(broken, ", "))
	}
	return nil
}
```

Notes for the reviewer: the whole run uses one `*sql.Conn` (the advisory lock is per session, `PRAGMA foreign_keys` per connection, and SQLite's pool has one connection — touching `db` directly inside the run would deadlock); `adopt` runs only when `bun_migrations` exists and `schema_migrations` does not, in one transaction; the version row is written by the same transaction as the migration's statements; on a failed `pg_advisory_unlock` the connection is discarded (`driver.ErrBadConn`) so the session — and its lock — ends instead of returning to the pool.

- [ ] **Step 8: Run the runner tests on SQLite**

Run: `go test -count=1 ./internal/infra/db/migrate/ -v 2>&1 | grep -E '^(---|ok|FAIL)'`
Expected: `--- PASS` for `TestAdoptBunDatabase`, `TestAdoptionIsNotRepeated`, `TestEnginesShareVersions`, `TestLoadRejectsBadNames`, `TestMigrateFreshIsIdempotent`, `TestMigrateToStopsAtVersion`, `TestFailedMigrationRollsBack`, `TestForeignKeysOffRebuild`, `TestForeignKeysOffIsSQLiteOnly`, `TestAppDomainsRebuildIsAtomic`, `TestAdoptRejectsUnknownBunVersion`, `TestSplit`; `--- SKIP: TestConcurrentMigrateIsSerialized`; `ok`.

- [ ] **Step 9: Run the DB packages on Postgres too (make test-pg, CI)**

In `scripts/test-pg.sh` replace the header comment and the last line:

```bash
# Run the API contract suite and the internal/infra/db packages (migration
# runner, repositories) against a throwaway postgres:16 container on a random
# host port (make test-pg). The container is removed on exit, pass or fail.
# Extra arguments go to `go test` (e.g. -run TestParity -v).
```

```bash
"$GO" test -count=1 "$@" ./internal/app/web/contract/ ./internal/infra/db/...
```

In `Makefile` replace the comment above `test-pg:`:

```make
# The API contract suite and the internal/infra/db packages against a
# throwaway postgres:16 container (Docker). Plain `go test ./...` stays on
# SQLite. Extra go test flags:
# make test-pg TEST_PG_ARGS='-run TestParity -v'
```

In `.github/workflows/ci.yml`, job `contract`, replace the comment above the job and its test step:

```yaml
  # The API contract suite and the internal/infra/db packages (migration
  # runner, repositories) on Postgres (the go job already runs them on
  # SQLite). The contract suite must match testdata/parity.golden.json like
  # the SQLite run, and this job's coverage profile is merged into the
  # gates: Postgres-only branches run nowhere else.
```

```yaml
      - name: Contract suite and DB packages (Postgres)
        run: >-
          go test -race -count=1 -covermode=atomic -coverpkg=./...
          -coverprofile=contract-postgres.out ./internal/app/web/contract/ ./internal/infra/db/...
```

Append to `.gitignore`, under `/coverage.out`:

```
# Postgres coverage profile (make test-pg TEST_PG_ARGS='... -coverprofile=contract-postgres.out')
/contract-postgres.out
```

Run: `make test-pg TEST_PG_ARGS='-run "TestAdopt|TestMigrate|TestFailed|TestConcurrent|TestEngines" -v' 2>&1 | grep -E '^(---|ok|FAIL)'`
Expected: the Step 8 tests pass with `postgres` subtests (e.g. `--- PASS: TestAdoptBunDatabase/postgres/bun_full.sql`), `--- PASS: TestConcurrentMigrateIsSerialized`, and `ok` for `internal/app/web/contract` and `internal/infra/db/migrate`.

- [ ] **Step 10: Tidy modules and run everything**

Run: `go mod tidy && git diff go.mod`
Expected: `github.com/jackc/pgx/v5 v5.9.2` and `modernc.org/sqlite v1.49.1` move from the indirect block to the direct `require` block; nothing else changes.

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all `ok` (the Bun code is untouched and still wired).

- [ ] **Step 11: Commit**

```bash
git add internal/infra/db/backend internal/infra/db/migrations internal/infra/db/migrate \
  scripts/test-pg.sh Makefile .github/workflows/ci.yml .gitignore go.mod go.sum
git commit -m "feat(db): forward-only SQL migration runner that adopts Bun databases

Per-engine SQL ports of the eight Bun migrations (same versions), applied
one transaction per migration together with its schema_migrations row,
serialized across replicas with a Postgres advisory lock. SQLite table
rebuilds run with foreign keys off and must pass foreign_key_check. A
Bun-era database's applied versions are copied over first. Not wired yet.

make test-pg and the CI contract job now also run ./internal/infra/db/...

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: sqlc configuration, queries and generated code

**Files:**
- Create: `internal/infra/db/sqlc/sqlc.yaml`, `internal/infra/db/sqlc/gen.go`
- Create: `internal/infra/db/sqlc/query/sqlite/{users,tokens,organizations,servers,apps,app_domains,server_domains}.sql`
- Create: the same seven names under `internal/infra/db/sqlc/query/pgsql/`
- Generate (commit, never edit): `internal/infra/db/sqlc/gen/sqlite/*.go` (package `sqlitegen`), `internal/infra/db/sqlc/gen/pgsql/*.go` (package `pgsqlgen`)
- Test: `internal/infra/db/sqlc/ascii_test.go`, `internal/infra/db/sqlc/shapes_test.go`
- Modify: `Makefile` (`sqlc`, `sqlc-check`), `tools/diffcov/classify.go`, `tools/diffcov/goprofile.go`, `tools/diffcov/classify_test.go`, `tools/diffcov/goprofile_test.go`

**Interfaces:**
- Consumes: Task 2's `internal/infra/db/migrations/{sqlite,pgsql}/*.sql` (sqlc's schema input).
- Produces (identical in `sqlitegen` and `pgsqlgen`; `New(db DBTX) *Queries`; every method takes `ctx context.Context` first):
  - Models: `App`, `AppDomain`, `Organization`, `OrganizationUser`, `Server`, `ServerCapability`, `ServerCertificate`, `ServerDomain`, `ServerFeature`, `ServerRegistration`, `User`, `UserConnectedAccount`, `UserCredential`, `UserToken` (timestamps `types.DateTime`, nullable ones `*types.DateTime`; `User.Avatar`, `App.TemplateID` `*string`; `AppDomain.UpstreamPort`, `Code` `int64`; `AppDomain.Ssl`, `ServerDomain.Tls` keep sqlc's initialisms).
  - users.sql: `GetUser(userID string) (User, error)`, `CountUsers() (int64, error)`, `InsertUser(InsertUserParams{UserID, Name string; Avatar *string; CreatedAt, LastSeen types.DateTime}) error`, `DeleteUser(userID string) error`, `GetConnectedAccountUserID(GetConnectedAccountUserIDParams{Provider, ExternalID string}) (string, error)`, `InsertConnectedAccount(InsertConnectedAccountParams{Provider, ExternalID, UserID string}) error`, `GetFirstConnectedAccountProvider(userID string) (string, error)`, `InsertCredentials(InsertCredentialsParams{UserID, Email, PasswordHash string; MustChangePassword bool; UpdatedAt types.DateTime}) error`, `GetCredentialsByEmail(email string) (UserCredential, error)`, `GetCredentialsByUser(userID string) (UserCredential, error)`, `EmailTaken(email string) (bool, error)`, `UpdatePassword(UpdatePasswordParams{PasswordHash string; MustChangePassword bool; UpdatedAt types.DateTime; UserID string}) (int64, error)`.
  - tokens.sql: `InsertToken(InsertTokenParams{TokenID, UserID, Name, TokenPrefix, TokenHash, TokenType string; ExpiresAt, LastUsedAt *types.DateTime; CreatedAt types.DateTime}) error`, `GetTokenByHash(tokenHash string) (UserToken, error)`, `TouchTokenLastUsed(TouchTokenLastUsedParams{LastUsedAt *types.DateTime; TokenID string}) error`, `ListTokensByUser(userID string) ([]UserToken, error)`, `DeleteToken(DeleteTokenParams{TokenID, UserID string}) (int64, error)`.
  - organizations.sql: `InsertOrganization(InsertOrganizationParams{OrganizationID, Name string; CreatedAt types.DateTime}) error`, `GetOrganization(organizationID string) (Organization, error)`, `UpdateOrganization(UpdateOrganizationParams{Name, Icon, Color, OrganizationID string}) (int64, error)`, `InsertOrganizationUser(InsertOrganizationUserParams{OrganizationID, UserID, Role string; CreatedAt types.DateTime}) error`, `GetPrimaryOrganizationID(userID string) (string, error)`, `LockOrganizationMember(LockOrganizationMemberParams{OrganizationID, UserID string}) (string, error)`, `IsOrganizationMember(IsOrganizationMemberParams{OrganizationID, UserID string}) (bool, error)`, `ListOrganizationMembers(organizationID string) ([]User, error)`.
  - servers.sql: `ListServersByUser(userID string) ([]Server, error)`, `ListCapabilitiesByUser(userID string) ([]ListCapabilitiesByUserRow{ServerID, Name, Value string}, error)`, `ListFeaturesByUser(userID string) ([]ServerFeature, error)`, `ListServerUserIDs(serverID string) ([]string, error)`, `UserOwnsServer(UserOwnsServerParams{ServerID, UserID string}) (bool, error)`, `HasAnyServer() (bool, error)`, `GetFirstServerID() (string, error)`, `InsertServer(InsertServerParams{ServerID, OrganizationID, Name string; CreatedAt types.DateTime}) error`, `TouchServerLastSeen(TouchServerLastSeenParams{LastSeen *types.DateTime; ServerID string}) error`, `UpsertCapability(UpsertCapabilityParams{ServerID, Name, Value string; UpdatedAt types.DateTime}) error`, `UpsertFeature(UpsertFeatureParams{ServerID, Name string; IsEnabled bool}) error`, `GetCapability(GetCapabilityParams{ServerID, Name string}) (string, error)`, `InsertRegistration(InsertRegistrationParams{ServerID, CertificateID, Hostname, Code string; ExpiresAt types.DateTime; Certificate string; CertificateExpiresAt, CreatedAt types.DateTime}) error`, `DeleteRegistration(serverID string) error`, `DeleteAllRegistrations() error`, `GetRegistrationByCode(code string) (ServerRegistration, error)`, `GetLatestRegistrationCode() (string, error)`, `InsertCertificate(InsertCertificateParams{CertificateID, ServerID, Certificate string; IsActive bool; ExpiresAt, CreatedAt types.DateTime}) error`.
  - apps.sql: `ListAppsByServer(serverID string) ([]App, error)`, `ListAppIDsByServer(serverID string) ([]string, error)`, `GetApp(appID string) (App, error)`, `UpsertApp(UpsertAppParams{AppID, ServerID string; TemplateID *string; Name, Version, Icon, Color string; CreatedAt types.DateTime}) error`, `DeleteApp(appID string) error`, `RenameApp(RenameAppParams{Name, AppID string}) error`.
  - app_domains.sql: `GetDomainClaim(GetDomainClaimParams{ServerID, Domain string}) (GetDomainClaimRow{Domain, AppID, ServerID, AppName string}, error)`, `InsertAppDomain(InsertAppDomainParams{Domain, AppID, ServerID, Kind string; Ssl bool; UpstreamPort int64; Target string; Code int64; UpdatedAt types.DateTime}) error`, `DeleteAppDomainsByApp(appID string) error`, `DeleteAppDomainsByServer(serverID string) error`, `ListAppDomainsByServer(serverID string) ([]AppDomain, error)`, `ListAppDomainNames(ListAppDomainNamesParams{ServerID, AppID string}) ([]string, error)`.
  - server_domains.sql: `ListServerDomains(serverID string) ([]ServerDomain, error)`, `UpsertServerDomain(UpsertServerDomainParams{ServerID, Name, Tls string; NotAfter *types.DateTime; Issuer string; UpdatedAt types.DateTime}) error`, `InsertServerDomain(InsertServerDomainParams{same fields}) error`, `DeleteServerDomain(DeleteServerDomainParams{ServerID, Name string}) error`, `DeleteServerDomainsByServer(serverID string) error`.
  - `make sqlc`, `make sqlc-check`; `tools/diffcov` ignores `internal/infra/db/sqlc/gen/` for the diff gate and the ratchet total.

Every query reproduces a Bun query's semantics; the per-repository tasks list which Bun call each replaces. Differences are only `?` vs `$N`, `excluded` vs `EXCLUDED` (both engines accept either; kept as written), and `FOR UPDATE` in Postgres's `LockOrganizationMember`.

- [ ] **Step 1: Write the guards**

Create `internal/infra/db/sqlc/gen.go`:

```go
// Package sqlc holds the sqlc configuration (sqlc.yaml) and the hand-written
// per-engine queries (query/sqlite, query/pgsql). `make sqlc` regenerates
// gen/sqlite (package sqlitegen) and gen/pgsql (package pgsqlgen); the
// generated code is committed and `make sqlc-check` fails when it is stale.
package sqlc

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
```

Create `internal/infra/db/sqlc/ascii_test.go`:

```go
package sqlc

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A multibyte character anywhere in a query or migration file (comments
// included) can make sqlc emit a truncated SQL constant that fails only at
// runtime, so every file sqlc reads must be plain ASCII.
func TestSQLFilesAreASCII(t *testing.T) {
	n := 0
	for _, root := range []string{"query", filepath.Join("..", "migrations")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
				return err
			}
			n++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			line := 1
			for _, c := range b {
				if c == '\n' {
					line++
				}
				if c > 0x7F {
					t.Errorf("%s:%d: non-ASCII byte 0x%02X", path, line, c)
					return nil
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n == 0 {
		t.Fatal("no .sql files found: the test is looking in the wrong place")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/infra/db/sqlc/`
Expected: `FAIL` with `lstat query: no such file or directory`.

- [ ] **Step 3: Write the configuration and the queries**

Create `internal/infra/db/sqlc/sqlc.yaml`:

```yaml
# sqlc: one typed query package per engine (make sqlc; make sqlc-check in CI).
#
# Each engine reads its schema straight from the migrations the runner
# applies, so a query against a dropped or renamed column fails to generate.
# Repositories are written against sqlitegen and convert pgsqlgen values with
# plain Go struct conversions, so the overrides below make every generated
# type identical across engines (shapes_test.go proves it):
#   - timestamps -> types.DateTime (*types.DateTime when nullable): the
#     Bun-era storage format, naive UTC "2006-01-02 15:04:05";
#   - Postgres uuid -> string, nullable text/varchar -> *string
#     (emit_pointers_for_null_types does not cover Postgres with database/sql);
#   - Postgres int4 -> int64 (SQLite INTEGER).
# Postgres catalogs TIMESTAMPTZ as "timestamptz" (unqualified) and TIMESTAMP
# as "pg_catalog.timestamp"; both spellings of timestamptz are listed.
version: "2"
sql:
  - engine: "sqlite"
    schema: "../migrations/sqlite"
    queries: "./query/sqlite"
    gen:
      go:
        package: "sqlitegen"
        out: "gen/sqlite"
        emit_interface: true
        emit_json_tags: false
        emit_empty_slices: true
        emit_pointers_for_null_types: true
        overrides:
          - db_type: "TIMESTAMP"
            go_type: "winterflow/internal/infra/db/types.DateTime"
          - db_type: "TIMESTAMP"
            nullable: true
            go_type:
              import: "winterflow/internal/infra/db/types"
              type: "DateTime"
              pointer: true
          # app_domains.app_id/server_id; unmapped it becomes interface{}.
          - db_type: "CHAR(36)"
            go_type: "string"
  - engine: "postgresql"
    schema: "../migrations/pgsql"
    queries: "./query/pgsql"
    gen:
      go:
        package: "pgsqlgen"
        out: "gen/pgsql"
        emit_interface: true
        emit_json_tags: false
        emit_empty_slices: true
        emit_pointers_for_null_types: true
        overrides:
          - db_type: "timestamptz"
            go_type: "winterflow/internal/infra/db/types.DateTime"
          - db_type: "timestamptz"
            nullable: true
            go_type:
              import: "winterflow/internal/infra/db/types"
              type: "DateTime"
              pointer: true
          - db_type: "pg_catalog.timestamptz"
            go_type: "winterflow/internal/infra/db/types.DateTime"
          - db_type: "pg_catalog.timestamptz"
            nullable: true
            go_type:
              import: "winterflow/internal/infra/db/types"
              type: "DateTime"
              pointer: true
          - db_type: "pg_catalog.timestamp"
            go_type: "winterflow/internal/infra/db/types.DateTime"
          - db_type: "pg_catalog.timestamp"
            nullable: true
            go_type:
              import: "winterflow/internal/infra/db/types"
              type: "DateTime"
              pointer: true
          - db_type: "uuid"
            go_type: "string"
          - db_type: "uuid"
            nullable: true
            go_type:
              type: "string"
              pointer: true
          - db_type: "pg_catalog.varchar"
            nullable: true
            go_type:
              type: "string"
              pointer: true
          - db_type: "text"
            nullable: true
            go_type:
              type: "string"
              pointer: true
          - db_type: "pg_catalog.int4"
            go_type: "int64"
```

SQLite queries, `internal/infra/db/sqlc/query/sqlite/`:

`users.sql`
```sql
-- Users, their connected accounts and local credentials.

-- name: GetUser :one
SELECT * FROM users WHERE user_id = ?;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (user_id, name, avatar, created_at, last_seen)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteUser :exec
DELETE FROM users WHERE user_id = ?;

-- name: GetConnectedAccountUserID :one
SELECT user_id FROM user_connected_accounts WHERE provider = ? AND external_id = ?;

-- name: InsertConnectedAccount :exec
INSERT INTO user_connected_accounts (provider, external_id, user_id)
VALUES (?, ?, ?);

-- name: GetFirstConnectedAccountProvider :one
SELECT provider FROM user_connected_accounts
WHERE user_id = ?
ORDER BY provider ASC, external_id ASC
LIMIT 1;

-- name: InsertCredentials :exec
INSERT INTO user_credentials (user_id, email, password_hash, must_change_password, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetCredentialsByEmail :one
SELECT * FROM user_credentials WHERE email = ?;

-- name: GetCredentialsByUser :one
SELECT * FROM user_credentials WHERE user_id = ?;

-- name: EmailTaken :one
SELECT EXISTS (SELECT 1 FROM user_credentials WHERE email = ?);

-- name: UpdatePassword :execrows
UPDATE user_credentials
SET password_hash = ?, must_change_password = ?, updated_at = ?
WHERE user_id = ?;
```

`tokens.sql`
```sql
-- Personal access tokens (hashed at rest).

-- name: InsertToken :exec
INSERT INTO user_tokens (token_id, user_id, name, token_prefix, token_hash, token_type, expires_at, last_used_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTokenByHash :one
SELECT * FROM user_tokens WHERE token_hash = ?;

-- name: TouchTokenLastUsed :exec
UPDATE user_tokens SET last_used_at = ? WHERE token_id = ?;

-- name: ListTokensByUser :many
SELECT * FROM user_tokens
WHERE user_id = ?
ORDER BY created_at DESC, token_id DESC;

-- name: DeleteToken :execrows
DELETE FROM user_tokens WHERE token_id = ? AND user_id = ?;
```

`organizations.sql`
```sql
-- Organizations and memberships.

-- name: InsertOrganization :exec
INSERT INTO organizations (organization_id, name, created_at)
VALUES (?, ?, ?);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE organization_id = ?;

-- name: UpdateOrganization :execrows
UPDATE organizations SET name = ?, icon = ?, color = ?
WHERE organization_id = ?;

-- name: InsertOrganizationUser :exec
INSERT INTO organization_users (organization_id, user_id, role, created_at)
VALUES (?, ?, ?, ?);

-- name: GetPrimaryOrganizationID :one
SELECT organization_id FROM organization_users
WHERE user_id = ?
ORDER BY created_at ASC
LIMIT 1;

-- LockOrganizationMember re-reads the caller's membership inside
-- RemoveMember's transaction. SQLite has no row locks (its writers are
-- serialized), so this is a plain SELECT; Postgres adds FOR UPDATE.
-- name: LockOrganizationMember :one
SELECT user_id FROM organization_users
WHERE organization_id = ? AND user_id = ?;

-- name: IsOrganizationMember :one
SELECT EXISTS (
    SELECT 1 FROM organization_users WHERE organization_id = ? AND user_id = ?
);

-- name: ListOrganizationMembers :many
SELECT u.user_id, u.name, u.avatar, u.created_at, u.last_seen
FROM organization_users AS ou
JOIN users AS u ON u.user_id = ou.user_id
WHERE ou.organization_id = ?
ORDER BY ou.created_at ASC, ou.user_id ASC;
```

`servers.sql`
```sql
-- Servers, their capabilities/features, registrations and certificates.

-- name: ListServersByUser :many
SELECT * FROM servers
WHERE organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY created_at ASC, server_id ASC;

-- name: ListCapabilitiesByUser :many
SELECT c.server_id, c.name, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY c.server_id ASC, c.name ASC;

-- name: ListFeaturesByUser :many
SELECT f.server_id, f.name, f.is_enabled
FROM server_features AS f
JOIN servers AS s ON s.server_id = f.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY f.server_id ASC, f.name ASC;

-- name: ListServerUserIDs :many
SELECT user_id FROM organization_users
WHERE organization_id IN (SELECT organization_id FROM servers WHERE server_id = ?);

-- name: UserOwnsServer :one
SELECT EXISTS (
    SELECT 1 FROM servers
    WHERE server_id = ?
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
);

-- name: HasAnyServer :one
SELECT EXISTS (SELECT 1 FROM servers);

-- name: GetFirstServerID :one
SELECT server_id FROM servers ORDER BY created_at ASC, server_id ASC LIMIT 1;

-- name: InsertServer :exec
INSERT INTO servers (server_id, organization_id, name, created_at)
VALUES (?, ?, ?, ?);

-- name: TouchServerLastSeen :exec
UPDATE servers SET last_seen = ? WHERE server_id = ?;

-- name: UpsertCapability :exec
INSERT INTO server_capabilities (server_id, name, value, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET value = excluded.value, updated_at = excluded.updated_at;

-- name: UpsertFeature :exec
INSERT INTO server_features (server_id, name, is_enabled)
VALUES (?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET is_enabled = excluded.is_enabled;

-- name: GetCapability :one
SELECT value FROM server_capabilities WHERE server_id = ? AND name = ?;

-- name: InsertRegistration :exec
INSERT INTO server_registrations (server_id, certificate_id, hostname, code, expires_at, certificate, certificate_expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteRegistration :exec
DELETE FROM server_registrations WHERE server_id = ?;

-- name: DeleteAllRegistrations :exec
DELETE FROM server_registrations;

-- name: GetRegistrationByCode :one
SELECT * FROM server_registrations WHERE code = ?;

-- name: GetLatestRegistrationCode :one
SELECT code FROM server_registrations ORDER BY created_at DESC LIMIT 1;

-- name: InsertCertificate :exec
INSERT INTO server_certificates (certificate_id, server_id, certificate, is_active, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?);
```

`apps.sql`
```sql
-- The apps catalog (a reconciled cache of what agents report).

-- name: ListAppsByServer :many
SELECT * FROM apps
WHERE server_id = ?
ORDER BY created_at ASC, app_id ASC;

-- name: ListAppIDsByServer :many
SELECT app_id FROM apps WHERE server_id = ?;

-- name: GetApp :one
SELECT * FROM apps WHERE app_id = ?;

-- name: UpsertApp :exec
INSERT INTO apps (app_id, server_id, template_id, name, version, icon, color, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (app_id) DO UPDATE
SET name = excluded.name,
    version = excluded.version,
    icon = excluded.icon,
    color = excluded.color,
    template_id = excluded.template_id;

-- name: DeleteApp :exec
DELETE FROM apps WHERE app_id = ?;

-- name: RenameApp :exec
UPDATE apps SET name = ? WHERE app_id = ?;
```

`app_domains.sql`
```sql
-- The app_domains ingress index (rebuildable from the agents' app configs).

-- name: GetDomainClaim :one
SELECT d.domain, d.app_id, d.server_id, COALESCE(a.name, '') AS app_name
FROM app_domains AS d
LEFT JOIN apps AS a ON a.app_id = d.app_id
WHERE d.server_id = ? AND d.domain = ?;

-- name: InsertAppDomain :exec
INSERT INTO app_domains (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAppDomainsByApp :exec
DELETE FROM app_domains WHERE app_id = ?;

-- name: DeleteAppDomainsByServer :exec
DELETE FROM app_domains WHERE server_id = ?;

-- name: ListAppDomainsByServer :many
SELECT * FROM app_domains WHERE server_id = ? ORDER BY domain ASC;

-- name: ListAppDomainNames :many
SELECT domain FROM app_domains
WHERE server_id = ? AND app_id = ?
ORDER BY domain ASC;
```

`server_domains.sql`
```sql
-- The server_domains index (mirrors each agent's domain registrations).

-- name: ListServerDomains :many
SELECT * FROM server_domains WHERE server_id = ? ORDER BY name ASC;

-- name: UpsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET tls = excluded.tls,
    not_after = excluded.not_after,
    issuer = excluded.issuer,
    updated_at = excluded.updated_at;

-- name: InsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteServerDomain :exec
DELETE FROM server_domains WHERE server_id = ? AND name = ?;

-- name: DeleteServerDomainsByServer :exec
DELETE FROM server_domains WHERE server_id = ?;
```

Postgres queries, `internal/infra/db/sqlc/query/pgsql/`:

`users.sql`
```sql
-- Users, their connected accounts and local credentials.

-- name: GetUser :one
SELECT * FROM users WHERE user_id = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (user_id, name, avatar, created_at, last_seen)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteUser :exec
DELETE FROM users WHERE user_id = $1;

-- name: GetConnectedAccountUserID :one
SELECT user_id FROM user_connected_accounts WHERE provider = $1 AND external_id = $2;

-- name: InsertConnectedAccount :exec
INSERT INTO user_connected_accounts (provider, external_id, user_id)
VALUES ($1, $2, $3);

-- name: GetFirstConnectedAccountProvider :one
SELECT provider FROM user_connected_accounts
WHERE user_id = $1
ORDER BY provider ASC, external_id ASC
LIMIT 1;

-- name: InsertCredentials :exec
INSERT INTO user_credentials (user_id, email, password_hash, must_change_password, updated_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetCredentialsByEmail :one
SELECT * FROM user_credentials WHERE email = $1;

-- name: GetCredentialsByUser :one
SELECT * FROM user_credentials WHERE user_id = $1;

-- name: EmailTaken :one
SELECT EXISTS (SELECT 1 FROM user_credentials WHERE email = $1);

-- name: UpdatePassword :execrows
UPDATE user_credentials
SET password_hash = $1, must_change_password = $2, updated_at = $3
WHERE user_id = $4;
```

`tokens.sql`
```sql
-- Personal access tokens (hashed at rest).

-- name: InsertToken :exec
INSERT INTO user_tokens (token_id, user_id, name, token_prefix, token_hash, token_type, expires_at, last_used_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetTokenByHash :one
SELECT * FROM user_tokens WHERE token_hash = $1;

-- name: TouchTokenLastUsed :exec
UPDATE user_tokens SET last_used_at = $1 WHERE token_id = $2;

-- name: ListTokensByUser :many
SELECT * FROM user_tokens
WHERE user_id = $1
ORDER BY created_at DESC, token_id DESC;

-- name: DeleteToken :execrows
DELETE FROM user_tokens WHERE token_id = $1 AND user_id = $2;
```

`organizations.sql`
```sql
-- Organizations and memberships.

-- name: InsertOrganization :exec
INSERT INTO organizations (organization_id, name, created_at)
VALUES ($1, $2, $3);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE organization_id = $1;

-- name: UpdateOrganization :execrows
UPDATE organizations SET name = $1, icon = $2, color = $3
WHERE organization_id = $4;

-- name: InsertOrganizationUser :exec
INSERT INTO organization_users (organization_id, user_id, role, created_at)
VALUES ($1, $2, $3, $4);

-- name: GetPrimaryOrganizationID :one
SELECT organization_id FROM organization_users
WHERE user_id = $1
ORDER BY created_at ASC
LIMIT 1;

-- LockOrganizationMember re-reads and row-locks the caller's membership
-- inside RemoveMember's transaction, so two members removing each other
-- concurrently cannot empty the org.
-- name: LockOrganizationMember :one
SELECT user_id FROM organization_users
WHERE organization_id = $1 AND user_id = $2
FOR UPDATE;

-- name: IsOrganizationMember :one
SELECT EXISTS (
    SELECT 1 FROM organization_users WHERE organization_id = $1 AND user_id = $2
);

-- name: ListOrganizationMembers :many
SELECT u.user_id, u.name, u.avatar, u.created_at, u.last_seen
FROM organization_users AS ou
JOIN users AS u ON u.user_id = ou.user_id
WHERE ou.organization_id = $1
ORDER BY ou.created_at ASC, ou.user_id ASC;
```

`servers.sql`
```sql
-- Servers, their capabilities/features, registrations and certificates.

-- name: ListServersByUser :many
SELECT * FROM servers
WHERE organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $1)
ORDER BY created_at ASC, server_id ASC;

-- name: ListCapabilitiesByUser :many
SELECT c.server_id, c.name, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $1)
ORDER BY c.server_id ASC, c.name ASC;

-- name: ListFeaturesByUser :many
SELECT f.server_id, f.name, f.is_enabled
FROM server_features AS f
JOIN servers AS s ON s.server_id = f.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $1)
ORDER BY f.server_id ASC, f.name ASC;

-- name: ListServerUserIDs :many
SELECT user_id FROM organization_users
WHERE organization_id IN (SELECT organization_id FROM servers WHERE server_id = $1);

-- name: UserOwnsServer :one
SELECT EXISTS (
    SELECT 1 FROM servers
    WHERE server_id = $1
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $2)
);

-- name: HasAnyServer :one
SELECT EXISTS (SELECT 1 FROM servers);

-- name: GetFirstServerID :one
SELECT server_id FROM servers ORDER BY created_at ASC, server_id ASC LIMIT 1;

-- name: InsertServer :exec
INSERT INTO servers (server_id, organization_id, name, created_at)
VALUES ($1, $2, $3, $4);

-- name: TouchServerLastSeen :exec
UPDATE servers SET last_seen = $1 WHERE server_id = $2;

-- name: UpsertCapability :exec
INSERT INTO server_capabilities (server_id, name, value, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (server_id, name) DO UPDATE
SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at;

-- name: UpsertFeature :exec
INSERT INTO server_features (server_id, name, is_enabled)
VALUES ($1, $2, $3)
ON CONFLICT (server_id, name) DO UPDATE
SET is_enabled = EXCLUDED.is_enabled;

-- name: GetCapability :one
SELECT value FROM server_capabilities WHERE server_id = $1 AND name = $2;

-- name: InsertRegistration :exec
INSERT INTO server_registrations (server_id, certificate_id, hostname, code, expires_at, certificate, certificate_expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: DeleteRegistration :exec
DELETE FROM server_registrations WHERE server_id = $1;

-- name: DeleteAllRegistrations :exec
DELETE FROM server_registrations;

-- name: GetRegistrationByCode :one
SELECT * FROM server_registrations WHERE code = $1;

-- name: GetLatestRegistrationCode :one
SELECT code FROM server_registrations ORDER BY created_at DESC LIMIT 1;

-- name: InsertCertificate :exec
INSERT INTO server_certificates (certificate_id, server_id, certificate, is_active, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6);
```

`apps.sql`
```sql
-- The apps catalog (a reconciled cache of what agents report).

-- name: ListAppsByServer :many
SELECT * FROM apps
WHERE server_id = $1
ORDER BY created_at ASC, app_id ASC;

-- name: ListAppIDsByServer :many
SELECT app_id FROM apps WHERE server_id = $1;

-- name: GetApp :one
SELECT * FROM apps WHERE app_id = $1;

-- name: UpsertApp :exec
INSERT INTO apps (app_id, server_id, template_id, name, version, icon, color, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (app_id) DO UPDATE
SET name = EXCLUDED.name,
    version = EXCLUDED.version,
    icon = EXCLUDED.icon,
    color = EXCLUDED.color,
    template_id = EXCLUDED.template_id;

-- name: DeleteApp :exec
DELETE FROM apps WHERE app_id = $1;

-- name: RenameApp :exec
UPDATE apps SET name = $1 WHERE app_id = $2;
```

`app_domains.sql`
```sql
-- The app_domains ingress index (rebuildable from the agents' app configs).

-- name: GetDomainClaim :one
SELECT d.domain, d.app_id, d.server_id, COALESCE(a.name, '') AS app_name
FROM app_domains AS d
LEFT JOIN apps AS a ON a.app_id = d.app_id
WHERE d.server_id = $1 AND d.domain = $2;

-- name: InsertAppDomain :exec
INSERT INTO app_domains (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: DeleteAppDomainsByApp :exec
DELETE FROM app_domains WHERE app_id = $1;

-- name: DeleteAppDomainsByServer :exec
DELETE FROM app_domains WHERE server_id = $1;

-- name: ListAppDomainsByServer :many
SELECT * FROM app_domains WHERE server_id = $1 ORDER BY domain ASC;

-- name: ListAppDomainNames :many
SELECT domain FROM app_domains
WHERE server_id = $1 AND app_id = $2
ORDER BY domain ASC;
```

`server_domains.sql`
```sql
-- The server_domains index (mirrors each agent's domain registrations).

-- name: ListServerDomains :many
SELECT * FROM server_domains WHERE server_id = $1 ORDER BY name ASC;

-- name: UpsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (server_id, name) DO UPDATE
SET tls = EXCLUDED.tls,
    not_after = EXCLUDED.not_after,
    issuer = EXCLUDED.issuer,
    updated_at = EXCLUDED.updated_at;

-- name: InsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: DeleteServerDomain :exec
DELETE FROM server_domains WHERE server_id = $1 AND name = $2;

-- name: DeleteServerDomainsByServer :exec
DELETE FROM server_domains WHERE server_id = $1;
```

- [ ] **Step 4: Add the Makefile targets**

In `Makefile`, add `sqlc sqlc-check` to the `.PHONY` line and replace the existing `sqlc:` target (`@sqlc generate`) with:

```make
# sqlc is pinned and run through `go run`, so nothing needs installing (its
# Postgres parser is cgo: a C compiler is required; the first run takes about
# a minute). make sqlc regenerates internal/infra/db/sqlc/gen; make sqlc-check
# fails when the committed code is stale (CI).
SQLC_VERSION := v1.31.1
SQLC := $(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) -f internal/infra/db/sqlc/sqlc.yaml

sqlc:
	$(SQLC) generate

sqlc-check:
	$(SQLC) diff
```

- [ ] **Step 5: Generate**

Run: `make sqlc && ls internal/infra/db/sqlc/gen/sqlite internal/infra/db/sqlc/gen/pgsql`
Expected: no sqlc errors; each directory holds `app_domains.sql.go apps.sql.go db.go models.go organizations.sql.go querier.go server_domains.sql.go servers.sql.go tokens.sql.go users.sql.go`; every file starts with `// Code generated by sqlc. DO NOT EDIT.` and `//   sqlc v1.31.1`. `grep -rn 'interface{}\|sql.Null\|time.Time' internal/infra/db/sqlc/gen/*/models.go` prints nothing.

- [ ] **Step 6: Pin the cross-engine shapes**

Create `internal/infra/db/sqlc/shapes_test.go` (compile-time: if any generated type differs between engines, the package does not build):

```go
package sqlc_test

import (
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

// Every generated row and parameter type must have the same shape on both
// engines: repositories are written against sqlitegen and convert pgsqlgen
// values with plain Go conversions. A query whose two versions disagree (a
// missing override, a different column order) fails to compile here.
var (
	_ = sqlitegen.App(pgsqlgen.App{})
	_ = pgsqlgen.App(sqlitegen.App{})
	_ = sqlitegen.AppDomain(pgsqlgen.AppDomain{})
	_ = pgsqlgen.AppDomain(sqlitegen.AppDomain{})
	_ = sqlitegen.DeleteServerDomainParams(pgsqlgen.DeleteServerDomainParams{})
	_ = pgsqlgen.DeleteServerDomainParams(sqlitegen.DeleteServerDomainParams{})
	_ = sqlitegen.DeleteTokenParams(pgsqlgen.DeleteTokenParams{})
	_ = pgsqlgen.DeleteTokenParams(sqlitegen.DeleteTokenParams{})
	_ = sqlitegen.GetCapabilityParams(pgsqlgen.GetCapabilityParams{})
	_ = pgsqlgen.GetCapabilityParams(sqlitegen.GetCapabilityParams{})
	_ = sqlitegen.GetConnectedAccountUserIDParams(pgsqlgen.GetConnectedAccountUserIDParams{})
	_ = pgsqlgen.GetConnectedAccountUserIDParams(sqlitegen.GetConnectedAccountUserIDParams{})
	_ = sqlitegen.GetDomainClaimParams(pgsqlgen.GetDomainClaimParams{})
	_ = pgsqlgen.GetDomainClaimParams(sqlitegen.GetDomainClaimParams{})
	_ = sqlitegen.GetDomainClaimRow(pgsqlgen.GetDomainClaimRow{})
	_ = pgsqlgen.GetDomainClaimRow(sqlitegen.GetDomainClaimRow{})
	_ = sqlitegen.InsertAppDomainParams(pgsqlgen.InsertAppDomainParams{})
	_ = pgsqlgen.InsertAppDomainParams(sqlitegen.InsertAppDomainParams{})
	_ = sqlitegen.InsertCertificateParams(pgsqlgen.InsertCertificateParams{})
	_ = pgsqlgen.InsertCertificateParams(sqlitegen.InsertCertificateParams{})
	_ = sqlitegen.InsertConnectedAccountParams(pgsqlgen.InsertConnectedAccountParams{})
	_ = pgsqlgen.InsertConnectedAccountParams(sqlitegen.InsertConnectedAccountParams{})
	_ = sqlitegen.InsertCredentialsParams(pgsqlgen.InsertCredentialsParams{})
	_ = pgsqlgen.InsertCredentialsParams(sqlitegen.InsertCredentialsParams{})
	_ = sqlitegen.InsertOrganizationParams(pgsqlgen.InsertOrganizationParams{})
	_ = pgsqlgen.InsertOrganizationParams(sqlitegen.InsertOrganizationParams{})
	_ = sqlitegen.InsertOrganizationUserParams(pgsqlgen.InsertOrganizationUserParams{})
	_ = pgsqlgen.InsertOrganizationUserParams(sqlitegen.InsertOrganizationUserParams{})
	_ = sqlitegen.InsertRegistrationParams(pgsqlgen.InsertRegistrationParams{})
	_ = pgsqlgen.InsertRegistrationParams(sqlitegen.InsertRegistrationParams{})
	_ = sqlitegen.InsertServerDomainParams(pgsqlgen.InsertServerDomainParams{})
	_ = pgsqlgen.InsertServerDomainParams(sqlitegen.InsertServerDomainParams{})
	_ = sqlitegen.InsertServerParams(pgsqlgen.InsertServerParams{})
	_ = pgsqlgen.InsertServerParams(sqlitegen.InsertServerParams{})
	_ = sqlitegen.InsertTokenParams(pgsqlgen.InsertTokenParams{})
	_ = pgsqlgen.InsertTokenParams(sqlitegen.InsertTokenParams{})
	_ = sqlitegen.InsertUserParams(pgsqlgen.InsertUserParams{})
	_ = pgsqlgen.InsertUserParams(sqlitegen.InsertUserParams{})
	_ = sqlitegen.IsOrganizationMemberParams(pgsqlgen.IsOrganizationMemberParams{})
	_ = pgsqlgen.IsOrganizationMemberParams(sqlitegen.IsOrganizationMemberParams{})
	_ = sqlitegen.ListAppDomainNamesParams(pgsqlgen.ListAppDomainNamesParams{})
	_ = pgsqlgen.ListAppDomainNamesParams(sqlitegen.ListAppDomainNamesParams{})
	_ = sqlitegen.ListCapabilitiesByUserRow(pgsqlgen.ListCapabilitiesByUserRow{})
	_ = pgsqlgen.ListCapabilitiesByUserRow(sqlitegen.ListCapabilitiesByUserRow{})
	_ = sqlitegen.LockOrganizationMemberParams(pgsqlgen.LockOrganizationMemberParams{})
	_ = pgsqlgen.LockOrganizationMemberParams(sqlitegen.LockOrganizationMemberParams{})
	_ = sqlitegen.Organization(pgsqlgen.Organization{})
	_ = pgsqlgen.Organization(sqlitegen.Organization{})
	_ = sqlitegen.OrganizationUser(pgsqlgen.OrganizationUser{})
	_ = pgsqlgen.OrganizationUser(sqlitegen.OrganizationUser{})
	_ = sqlitegen.RenameAppParams(pgsqlgen.RenameAppParams{})
	_ = pgsqlgen.RenameAppParams(sqlitegen.RenameAppParams{})
	_ = sqlitegen.Server(pgsqlgen.Server{})
	_ = pgsqlgen.Server(sqlitegen.Server{})
	_ = sqlitegen.ServerCapability(pgsqlgen.ServerCapability{})
	_ = pgsqlgen.ServerCapability(sqlitegen.ServerCapability{})
	_ = sqlitegen.ServerCertificate(pgsqlgen.ServerCertificate{})
	_ = pgsqlgen.ServerCertificate(sqlitegen.ServerCertificate{})
	_ = sqlitegen.ServerDomain(pgsqlgen.ServerDomain{})
	_ = pgsqlgen.ServerDomain(sqlitegen.ServerDomain{})
	_ = sqlitegen.ServerFeature(pgsqlgen.ServerFeature{})
	_ = pgsqlgen.ServerFeature(sqlitegen.ServerFeature{})
	_ = sqlitegen.ServerRegistration(pgsqlgen.ServerRegistration{})
	_ = pgsqlgen.ServerRegistration(sqlitegen.ServerRegistration{})
	_ = sqlitegen.TouchServerLastSeenParams(pgsqlgen.TouchServerLastSeenParams{})
	_ = pgsqlgen.TouchServerLastSeenParams(sqlitegen.TouchServerLastSeenParams{})
	_ = sqlitegen.TouchTokenLastUsedParams(pgsqlgen.TouchTokenLastUsedParams{})
	_ = pgsqlgen.TouchTokenLastUsedParams(sqlitegen.TouchTokenLastUsedParams{})
	_ = sqlitegen.UpdateOrganizationParams(pgsqlgen.UpdateOrganizationParams{})
	_ = pgsqlgen.UpdateOrganizationParams(sqlitegen.UpdateOrganizationParams{})
	_ = sqlitegen.UpdatePasswordParams(pgsqlgen.UpdatePasswordParams{})
	_ = pgsqlgen.UpdatePasswordParams(sqlitegen.UpdatePasswordParams{})
	_ = sqlitegen.UpsertAppParams(pgsqlgen.UpsertAppParams{})
	_ = pgsqlgen.UpsertAppParams(sqlitegen.UpsertAppParams{})
	_ = sqlitegen.UpsertCapabilityParams(pgsqlgen.UpsertCapabilityParams{})
	_ = pgsqlgen.UpsertCapabilityParams(sqlitegen.UpsertCapabilityParams{})
	_ = sqlitegen.UpsertFeatureParams(pgsqlgen.UpsertFeatureParams{})
	_ = pgsqlgen.UpsertFeatureParams(sqlitegen.UpsertFeatureParams{})
	_ = sqlitegen.UpsertServerDomainParams(pgsqlgen.UpsertServerDomainParams{})
	_ = pgsqlgen.UpsertServerDomainParams(sqlitegen.UpsertServerDomainParams{})
	_ = sqlitegen.User(pgsqlgen.User{})
	_ = pgsqlgen.User(sqlitegen.User{})
	_ = sqlitegen.UserConnectedAccount(pgsqlgen.UserConnectedAccount{})
	_ = pgsqlgen.UserConnectedAccount(sqlitegen.UserConnectedAccount{})
	_ = sqlitegen.UserCredential(pgsqlgen.UserCredential{})
	_ = pgsqlgen.UserCredential(sqlitegen.UserCredential{})
	_ = sqlitegen.UserOwnsServerParams(pgsqlgen.UserOwnsServerParams{})
	_ = pgsqlgen.UserOwnsServerParams(sqlitegen.UserOwnsServerParams{})
	_ = sqlitegen.UserToken(pgsqlgen.UserToken{})
	_ = pgsqlgen.UserToken(sqlitegen.UserToken{})
)
```

Run: `go test -count=1 ./internal/infra/db/sqlc/ -v 2>&1 | grep -E '^(---|ok)'`
Expected: `--- PASS: TestSQLFilesAreASCII`, `ok`.

Prove the guards bite, then revert: append `-- é` to `internal/infra/db/sqlc/query/sqlite/apps.sql`, run `go test ./internal/infra/db/sqlc/` (expected: `query/sqlite/apps.sql:...: non-ASCII byte 0xC3`), then delete that line again (the file is not committed yet, so `git checkout` cannot restore it) and rerun (expected `ok`).

- [ ] **Step 7: Check staleness detection**

Run: `make sqlc-check; echo exit=$?`
Expected: `exit=0`.

Run: `echo '// stale' >> internal/infra/db/sqlc/gen/sqlite/db.go; make sqlc-check; echo exit=$?; make sqlc`
Expected: a diff hunk for `gen/sqlite/db.go` and `exit=2` (make reports the failed recipe; sqlc itself exits 1); `make sqlc` restores the file (`make sqlc-check` exits 0 again).

- [ ] **Step 8: Exclude generated code from the coverage gates**

In `tools/diffcov/classify.go` replace the `Excluded` doc comment and the start of its switch:

```go
// sqlcGenDir holds sqlc's generated query packages (make sqlc): exempt from
// the diff gate and left out of the ratchet's total.
const sqlcGenDir = "internal/infra/db/sqlc/gen/"

// Excluded reports whether a path is exempt from the diff-coverage gate:
// tests (and the Vitest setup under web/src/test/), generated protobuf and
// sqlc code, vendored shadcn/ui, binary entry points, the coverage tooling
// itself, and the SPA bootstrap.
func Excluded(p string) bool {
	switch {
	case strings.HasSuffix(p, "_test.go"),
		strings.HasPrefix(p, sqlcGenDir),
```

(the remaining cases of the `case` list stay as they are).

In `tools/diffcov/goprofile.go` replace `Total`:

```go
// Total returns covered and total statements across the profile — the
// figure `go tool cover -func` prints as "total:", minus sqlc's generated
// query code (sqlcGenDir), which no test is expected to cover in full.
func (p GoProfile) Total() (covered, total int) {
	for file, blocks := range p {
		if strings.HasPrefix(file, sqlcGenDir) {
			continue
		}
		for _, b := range blocks {
			total += b.Stmts
			if b.Count > 0 {
				covered += b.Stmts
			}
		}
	}
	return covered, total
}
```

In `tools/diffcov/classify_test.go`, add to the `cases` map of `TestLanguage` (after the `hub_grpc.pb.go` entry):

```go
		"internal/infra/db/sqlc/gen/sqlite/users.sql.go":     "",
		"internal/infra/db/sqlc/gen/pgsql/querier.go":        "",
		"internal/infra/db/sqlc/gen.go":                      LangGo,
```

In `tools/diffcov/goprofile_test.go`, add after `TestGoProfileTotal`:

```go
func TestGoProfileTotalSkipsSQLCGen(t *testing.T) {
	p, err := ParseGoProfile(strings.NewReader(sampleProfile+
		"winterflow/internal/infra/db/sqlc/gen/sqlite/users.sql.go:10.1,20.2 7 0\n"), "winterflow")
	if err != nil {
		t.Fatal(err)
	}
	if cov, total := p.Total(); cov != 3 || total != 6 {
		t.Fatalf("Total = %d/%d, want 3/6 (generated sqlc code left out)", cov, total)
	}
}
```

Run: `gofmt -l tools && go test -count=1 ./tools/diffcov/`
Expected: no gofmt output; `ok  winterflow/tools/diffcov`.

- [ ] **Step 9: Everything still green**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all `ok` (the generated packages compile; nothing uses them yet).

- [ ] **Step 10: Commit**

```bash
git add internal/infra/db/sqlc Makefile tools/diffcov
git commit -m "build(db): sqlc config, per-engine queries and generated code

sqlc v1.31.1 (go run, pinned) reads the migration directories as schema
and generates sqlitegen and pgsqlgen; overrides make every generated type
identical across engines (shapes_test.go). make sqlc / make sqlc-check;
generated code is excluded from the diff gate and the coverage ratchet.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: TxManager, openers, `db.Open`, `dbtest`

**Files:**
- Modify: `internal/infra/db/types/datetime.go`, Test: `internal/infra/db/types/datetime_test.go`
- Create: `internal/infra/db/sqlite/sqlite.go`, Test: `internal/infra/db/sqlite/sqlite_test.go`
- Create: `internal/infra/db/pgsql/pgsql.go`, Test: `internal/infra/db/pgsql/pgsql_test.go`
- Create: `internal/infra/db/backend/tx.go`, Test: `internal/infra/db/backend/tx_test.go`
- Create: `internal/infra/db/db.go`, Test: `internal/infra/db/db_test.go` (package `db_test`; it sits next to the Bun files, which stay until Task 10)
- Create: `internal/infra/db/dbtest/dbtest.go`

**Interfaces:**
- Consumes: `backend.SQLite`, `backend.Postgres`, `backend.DBTX`, `migrate.Migrate(ctx, *sql.DB, driver) error` (Task 2).
- Produces:
  - `package sqlite`: `const Scheme = "sqlite://"`; `func Open(ctx context.Context, url string) (*sql.DB, error)` (no migration).
  - `package pgsql`: `const Scheme = "postgres://"`; `func Open(ctx context.Context, dsn string) (*sql.DB, error)` (no migration); unexported `utcDSN(string) string`.
  - `package backend`: `type TxManager struct{...}`; `func NewTxManager(db *sql.DB, log *logger.Logger, debugSQL bool) *TxManager`; `func (m *TxManager) DB() *sql.DB`; `func (m *TxManager) Querier(ctx context.Context) DBTX`; `func (m *TxManager) WithTx(ctx context.Context, fn func(ctx context.Context) error) error`.
  - `package db`: `type DB struct { SQL *sql.DB; Driver string; Tx *backend.TxManager; ... }`; `func Open(log *logger.Logger, url string) (*DB, error)`; `func (d *DB) HealthCheck() error`; `func (d *DB) Shutdown() error`.
  - `package dbtest`: `const Env = "WF_TEST_DATABASE_URL"`; `var Log *logger.Logger`; `func Driver(t testing.TB) string`; `func URL(t testing.TB) string`; `func Open(t testing.TB) *db.DB`; `func Ctx(t testing.TB) context.Context`; `func Rebind(driver, query string) string`; `func Exec(t testing.TB, d *db.DB, query string, args ...any)`; `func QueryString(t testing.TB, d *db.DB, query string, args ...any) string`; `func ID(n int) string`; `func SeedOrg(t testing.TB, d *db.DB, orgID string)`; `func SeedServer(t testing.TB, d *db.DB, orgID, serverID string)`; `func SeedMember(t testing.TB, d *db.DB, userID, orgID string)`; `func SeedApp(t testing.TB, d *db.DB, appID, serverID, name string)`.
  - `types.DateTime.Scan` now always yields UTC.

- [ ] **Step 1: Pin UTC on read**

Append to `internal/infra/db/types/datetime_test.go`:

```go
// Drivers return timestamps in the session's or host's zone (pgx: a fixed
// +00:00 zone, or time.Local); Scan normalizes to UTC so JSON always says Z.
func TestDateTimeScanNormalizesToUTC(t *testing.T) {
	for _, v := range []any{
		time.Date(2026, 7, 2, 15, 4, 5, 0, time.FixedZone("UTC+3", 3*3600)),
		time.Date(2026, 7, 2, 12, 4, 5, 0, time.FixedZone("", 0)),
		"2026-07-02T15:04:05+03:00",
		"2026-07-02 12:04:05",
	} {
		var dt DateTime
		if err := dt.Scan(v); err != nil {
			t.Fatal(err)
		}
		if dt.Time().Location() != time.UTC || !dt.Time().Equal(time.Date(2026, 7, 2, 12, 4, 5, 0, time.UTC)) {
			t.Errorf("Scan(%v) = %v (%v), want 12:04:05 UTC", v, dt.Time(), dt.Time().Location())
		}
	}
}
```

Run: `go test ./internal/infra/db/types/ -run TestDateTimeScanNormalizesToUTC`
Expected: `FAIL` — `Scan(2026-07-02 15:04:05 +0300 UTC+3) = 2026-07-02 15:04:05 +0300 UTC+3 (UTC+3), want 12:04:05 UTC` (and the same for the fixed-zone and RFC 3339 cases).

In `internal/infra/db/types/datetime.go`, in `Scan`, replace `*dt = DateTime(v)` (the `time.Time` case) with:

```go
		// Drivers hand back the session's or the host's zone (pgx: a fixed
		// +00 offset or time.Local); the API always reports UTC.
		*dt = DateTime(v.UTC())
```

and the string case's final `*dt = DateTime(t)` with `*dt = DateTime(t.UTC())`.

Run: `go test -count=1 ./internal/infra/db/types/`
Expected: `ok` (the existing `TestDateTimeScanTimeValue` still passes: `Equal` ignores the zone).

- [ ] **Step 2: SQLite opener — test, fail, implement, pass**

Create `internal/infra/db/sqlite/sqlite_test.go`:

```go
package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAppliesPragmasAndCreatesDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "wf.sqlite")
	db, err := Open(context.Background(), Scheme+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	for pragma, want := range map[string]string{
		"foreign_keys": "1",
		"journal_mode": "wal",
		"synchronous":  "1",
		"temp_store":   "2",
	} {
		var got string
		if err := db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Errorf("PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
		}
	}
	if s := db.Stats(); s.MaxOpenConnections != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", s.MaxOpenConnections)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), Scheme); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestOpenFailsOnUnwritableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Scheme+filepath.Join(file, "wf.sqlite")); err == nil {
		t.Fatal("Open succeeded under a regular file")
	}
}
```

Run: `go test ./internal/infra/db/sqlite/`
Expected: build failure `undefined: Open` / `undefined: Scheme`.

Create `internal/infra/db/sqlite/sqlite.go` (pragmas and pool exactly as `setupSQLite` in `bun.go`):

```go
// Package sqlite opens SQLite databases with the pure-Go modernc.org/sqlite
// driver (what the CGO-off release builds always used).
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Scheme prefixes a SQLite DATABASE_URL: sqlite://<path>.
const Scheme = "sqlite://"

var pragmas = []string{
	"PRAGMA foreign_keys = ON",
	"PRAGMA journal_mode = WAL",
	"PRAGMA synchronous = NORMAL",
	"PRAGMA cache_size = 1000",
	"PRAGMA temp_store = memory",
	"PRAGMA mmap_size = 268435456", // 256MB
}

// Open opens the database file named by a sqlite:// URL, creating its
// directory, applies the pragmas and pins the pool to one connection (one
// writer; the pragmas are per connection). It does not migrate.
func Open(ctx context.Context, url string) (*sql.DB, error) {
	path := strings.TrimPrefix(url, Scheme)
	if path == "" {
		return nil, fmt.Errorf("invalid connection string: path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}
	// One connection that never expires: SQLite has one writer, and the
	// pragmas below hold only on the connection they ran on.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to execute pragma %s: %w", p, err)
		}
	}
	return db, nil
}
```

Run: `go test -count=1 ./internal/infra/db/sqlite/`
Expected: `ok`.

- [ ] **Step 3: Postgres opener — test, fail, implement, pass**

Create `internal/infra/db/pgsql/pgsql_test.go` (`TestUTCDSNPinsTimeZone` is `bun_test.go`'s test, moved with the function):

```go
package pgsql

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUTCDSNPinsTimeZone(t *testing.T) {
	for _, in := range []string{
		"postgres://u:p@db:5432/wf",
		"postgres://u:p@db:5432/wf?sslmode=disable&timezone=America/New_York",
		"postgres://u:p@db:5432/wf?TimeZone=Europe/Berlin&TIMEZONE=Asia/Tokyo",
		"postgres://u:p@db/wf?search_path=t_1",
		"postgres://u:p%40%2F%3A%23@db:5432/wf",
		"postgres://u:p@/wf?host=/var/run/postgresql",
		"postgres://u:p@[2001:db8::1]:5432/wf?sslmode=disable",
	} {
		out := utcDSN(in)
		u, err := url.Parse(out)
		if err != nil {
			t.Fatalf("%s -> %s: %v", in, out, err)
		}
		q := u.Query()
		if q.Get("timezone") != "UTC" {
			t.Errorf("%s -> %s: timezone not pinned", in, out)
		}
		for k := range q {
			if k != "timezone" && strings.EqualFold(k, "timezone") {
				t.Errorf("%s -> %s: stray %s", in, out, k)
			}
		}
		orig, _ := url.Parse(in)
		if u.Scheme != orig.Scheme || u.Host != orig.Host || u.Path != orig.Path {
			t.Errorf("%s -> %s: scheme/host/path changed", in, out)
		}
		ouser := orig.User.Username()
		opass, _ := orig.User.Password()
		nuser := u.User.Username()
		npass, _ := u.User.Password()
		if ouser != nuser || opass != npass {
			t.Errorf("%s -> %s: userinfo changed (%q:%q -> %q:%q)", in, out, ouser, opass, nuser, npass)
		}
		for k := range orig.Query() {
			if !strings.EqualFold(k, "timezone") && q.Get(k) != orig.Query().Get(k) {
				t.Errorf("%s -> %s: lost %s", in, out, k)
			}
		}
	}
	if bad := "postgres://u:p@db:5432/wf\x7f"; utcDSN(bad) != bad {
		t.Error("an unparsable DSN must pass through unchanged")
	}
}

// TestOpenPinsUTCSession: whatever zone the URL asks for, the session runs
// in UTC, so naive "2006-01-02 15:04:05" timestamps are stored as UTC.
func TestOpenPinsUTCSession(t *testing.T) {
	base := os.Getenv("WF_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("needs WF_TEST_DATABASE_URL (make test-pg)")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme = "postgres"
	q := u.Query()
	q.Set("TimeZone", "America/New_York")
	u.RawQuery = q.Encode()
	db, err := Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tz string
	if err := db.QueryRow(`SHOW timezone`).Scan(&tz); err != nil || tz != "UTC" {
		t.Fatalf("session timezone = %q, %v", tz, err)
	}
	var got time.Time
	if err := db.QueryRow(`SELECT $1::timestamptz`, "2026-07-02 12:04:05").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 7, 2, 12, 4, 5, 0, time.UTC)) {
		t.Fatalf("naive timestamp read back as %v", got)
	}
}

func TestOpenFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := Open(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2"); err == nil {
		t.Fatal("Open succeeded against a closed port")
	}
	if _, err := Open(ctx, "postgres://u:p@db:5432/wf\x7f"); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("unparsable DSN: err = %v", err)
	}
}
```

Run: `go test ./internal/infra/db/pgsql/`
Expected: build failure `undefined: utcDSN`, `undefined: Open`.

Create `internal/infra/db/pgsql/pgsql.go`:

```go
// Package pgsql opens Postgres databases with jackc/pgx/v5 behind
// database/sql.
package pgsql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Scheme prefixes a Postgres DATABASE_URL.
const Scheme = "postgres://"

// Open connects to a postgres:// URL with the session time zone pinned to
// UTC, sets the pool limits and pings within 10 s. It does not migrate.
//
// pgx runs in simple-protocol mode: arguments are interpolated client-side
// as Bun's driver did, nothing is prepared server-side (so PgBouncer in
// transaction mode keeps working), and a types.DateTime travels as its
// "2006-01-02 15:04:05" text, which the UTC session reads as UTC.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(utcDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("failed to parse PostgreSQL connection string: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL database: %w", err)
	}
	return db, nil
}

// utcDSN pins the session time zone to UTC. types.DateTime writes naive
// UTC wall-clock strings ("2006-01-02 15:04:05"); a TIMESTAMPTZ column
// reads those in the session zone, so any other server/role time zone would
// shift every stored timestamp by its offset. pgx sends unknown query
// parameters as startup parameters. An unparsable DSN is returned unchanged
// for pgx to reject with its own error.
func utcDSN(connStr string) string {
	u, err := url.Parse(connStr)
	if err != nil {
		return connStr
	}
	q := u.Query()
	for k := range q {
		// A user's differently-cased "TimeZone=" must not be sent
		// alongside ours.
		if strings.EqualFold(k, "timezone") {
			q.Del(k)
		}
	}
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	return u.String()
}
```

Run: `go test -count=1 ./internal/infra/db/pgsql/ -v 2>&1 | grep -E '^(---|ok)'`
Expected: `--- PASS: TestUTCDSNPinsTimeZone`, `--- SKIP: TestOpenPinsUTCSession`, `--- PASS: TestOpenFailsFast`, `ok`.

- [ ] **Step 4: TxManager and `db.Open` tests**

Create `internal/infra/db/backend/tx_test.go`:

```go
package backend_test

import (
	"context"
	"errors"
	"testing"

	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
)

func newTable(t *testing.T) *db.DB {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.Exec(t, d, `CREATE TABLE tx_probe (n INTEGER PRIMARY KEY)`)
	return d
}

func insert(ctx context.Context, d *db.DB, n int) error {
	_, err := d.Tx.Querier(ctx).ExecContext(ctx, dbtest.Rebind(d.Driver, `INSERT INTO tx_probe (n) VALUES (?)`), n)
	return err
}

func probes(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.SQL.QueryRowContext(dbtest.Ctx(t), `SELECT count(*) FROM tx_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWithTxCommits(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	if err := d.Tx.WithTx(ctx, func(ctx context.Context) error {
		if err := insert(ctx, d, 1); err != nil {
			return err
		}
		return insert(ctx, d, 2)
	}); err != nil {
		t.Fatal(err)
	}
	if n := probes(t, d); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	boom := errors.New("boom")
	err := d.Tx.WithTx(ctx, func(ctx context.Context) error {
		if err := insert(ctx, d, 1); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := probes(t, d); n != 0 {
		t.Fatalf("rows = %d after rollback", n)
	}
}

func TestWithTxRollsBackOnPanic(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed")
			}
		}()
		_ = d.Tx.WithTx(ctx, func(ctx context.Context) error {
			if err := insert(ctx, d, 1); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if n := probes(t, d); n != 0 {
		t.Fatalf("rows = %d after panic", n)
	}
	// The connection went back to the pool: the next unit of work runs.
	if err := insert(ctx, d, 3); err != nil {
		t.Fatal(err)
	}
}

// A failing nested WithTx rolls back to its savepoint only; the outer
// transaction stays usable (on Postgres a failed statement would otherwise
// abort it) and commits its own work.
func TestNestedWithTxRollsBackToSavepoint(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	if err := d.Tx.WithTx(ctx, func(ctx context.Context) error {
		if err := insert(ctx, d, 1); err != nil {
			return err
		}
		inner := d.Tx.WithTx(ctx, func(ctx context.Context) error {
			if err := insert(ctx, d, 2); err != nil {
				return err
			}
			return insert(ctx, d, 1) // duplicate key: fails the statement
		})
		if inner == nil {
			t.Error("duplicate insert succeeded")
		}
		if err := d.Tx.WithTx(ctx, func(ctx context.Context) error { return insert(ctx, d, 4) }); err != nil {
			return err
		}
		return insert(ctx, d, 5)
	}); err != nil {
		t.Fatal(err)
	}
	var got []int
	rows, err := d.SQL.QueryContext(ctx, `SELECT n FROM tx_probe ORDER BY n`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		_ = rows.Scan(&n)
		got = append(got, n)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("rows = %v, want [1 4 5]", got)
	}
}

func TestNestedPanicRollsBackEverything(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	func() {
		defer func() { _ = recover() }()
		_ = d.Tx.WithTx(ctx, func(ctx context.Context) error {
			_ = insert(ctx, d, 1)
			return d.Tx.WithTx(ctx, func(ctx context.Context) error {
				_ = insert(ctx, d, 2)
				panic("boom")
			})
		})
	}()
	if n := probes(t, d); n != 0 {
		t.Fatalf("rows = %d after nested panic", n)
	}
}

func TestQuerierOutsideTxIsThePool(t *testing.T) {
	d := newTable(t)
	if q := d.Tx.Querier(context.Background()); q != backend.DBTX(d.SQL) {
		t.Fatalf("Querier = %T, want the *sql.DB", q)
	}
}

func TestDebugSQLLogsAndDelegates(t *testing.T) {
	d := newTable(t)
	m := backend.NewTxManager(d.SQL, dbtest.Log, true)
	ctx := dbtest.Ctx(t)
	q := m.Querier(ctx)
	if _, err := q.ExecContext(ctx, dbtest.Rebind(d.Driver, `INSERT INTO tx_probe (n) VALUES (?)`), 7); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM tx_probe`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
	rows, err := q.QueryContext(ctx, `SELECT n FROM tx_probe`)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	st, err := q.PrepareContext(ctx, `SELECT n FROM tx_probe`)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
}
```

Create `internal/infra/db/db_test.go`:

```go
package db_test

import (
	"strings"
	"testing"

	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

func TestOpenMigratesAndShutsDown(t *testing.T) {
	d, err := db.Open(dbtest.Log, dbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	if d.Driver != dbtest.Driver(t) {
		t.Fatalf("driver = %q", d.Driver)
	}
	if err := d.HealthCheck(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.SQL.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("schema_migrations rows = %d, %v", n, err)
	}
	if err := d.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := d.Shutdown(); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if err := d.HealthCheck(); err == nil {
		t.Fatal("HealthCheck passed on a closed database")
	}
}

func TestOpenRejectsUnknownScheme(t *testing.T) {
	_, err := db.Open(dbtest.Log, "mysql://u:p@h/db")
	if err == nil || !strings.Contains(err.Error(), "unsupported database type") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRejectsEmptySQLitePath(t *testing.T) {
	if _, err := db.Open(dbtest.Log, "sqlite://"); err == nil {
		t.Fatal("empty path accepted")
	}
}
```

Run: `go test ./internal/infra/db/ ./internal/infra/db/backend/`
Expected: `[setup failed]` with `package winterflow/internal/infra/db/dbtest is not in std` for both packages.

- [ ] **Step 5: Implement TxManager, `db.Open` and `dbtest`**

Create `internal/infra/db/backend/tx.go`:

```go
package backend

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"winterflow/pkg/logger"
)

// TxManager owns the *sql.DB and runs units of work. A transaction travels
// in the context: WithTx stores it, Querier hands it (or the pool) to sqlc.
type TxManager struct {
	db    *sql.DB
	log   *logger.Logger
	debug bool
}

// NewTxManager wraps db. debugSQL logs every statement and its duration
// (DEBUG_SQL=true).
func NewTxManager(db *sql.DB, log *logger.Logger, debugSQL bool) *TxManager {
	return &TxManager{db: db, log: log, debug: debugSQL}
}

// DB returns the pool, for wiring and tests; repositories use Querier.
func (m *TxManager) DB() *sql.DB { return m.db }

type txState struct {
	tx        *sql.Tx
	savepoint int
}

type ctxKey struct{}

// Querier returns the transaction stored in ctx by WithTx, or the pool.
func (m *TxManager) Querier(ctx context.Context) DBTX {
	var q DBTX = m.db
	if st, ok := ctx.Value(ctxKey{}).(*txState); ok {
		q = st.tx
	}
	if m.debug {
		return loggingDBTX{inner: q, log: m.log}
	}
	return q
}

// WithTx runs fn in a transaction: commit when fn returns nil, roll back
// when it returns an error or panics (the panic is re-raised). Called again
// with a ctx that already carries a transaction, it nests through
// SAVEPOINT sp_N / RELEASE SAVEPOINT / ROLLBACK TO SAVEPOINT (same syntax on
// SQLite and Postgres), so an inner failure leaves the outer transaction
// usable. fn must issue its queries through Querier(ctx) with the ctx it is
// given: on SQLite the pool has one connection, held by the transaction.
func (m *TxManager) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if st, ok := ctx.Value(ctxKey{}).(*txState); ok {
		return m.nested(ctx, st, fn)
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(context.WithValue(ctx, ctxKey{}, &txState{tx: tx})); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			m.log.Error("Failed to rollback transaction", "error", rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		m.log.Error("Failed to commit transaction", "error", err)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func (m *TxManager) nested(ctx context.Context, st *txState, fn func(ctx context.Context) error) error {
	st.savepoint++
	name := fmt.Sprintf("sp_%d", st.savepoint)
	if _, err := st.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return fmt.Errorf("failed to create savepoint %s: %w", name, err)
	}
	defer func() {
		if p := recover(); p != nil {
			_, _ = st.tx.ExecContext(context.WithoutCancel(ctx), "ROLLBACK TO SAVEPOINT "+name)
			panic(p)
		}
	}()
	if err := fn(ctx); err != nil {
		if _, rbErr := st.tx.ExecContext(context.WithoutCancel(ctx), "ROLLBACK TO SAVEPOINT "+name); rbErr != nil {
			m.log.Error("Failed to rollback to savepoint", "savepoint", name, "error", rbErr)
		}
		return err
	}
	if _, err := st.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return fmt.Errorf("failed to release savepoint %s: %w", name, err)
	}
	return nil
}

// loggingDBTX logs each statement and how long it took (DEBUG_SQL=true).
// Arguments are not logged: they include password and token hashes.
type loggingDBTX struct {
	inner DBTX
	log   *logger.Logger
}

func (l loggingDBTX) logQuery(query string, start time.Time, err error) {
	l.log.Info("sql", "query", query, "duration", time.Since(start).String(), "error", err)
}

func (l loggingDBTX) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := l.inner.ExecContext(ctx, query, args...)
	l.logQuery(query, start, err)
	return res, err
}

func (l loggingDBTX) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	start := time.Now()
	st, err := l.inner.PrepareContext(ctx, query)
	l.logQuery(query, start, err)
	return st, err
}

func (l loggingDBTX) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.inner.QueryContext(ctx, query, args...)
	l.logQuery(query, start, err)
	return rows, err
}

func (l loggingDBTX) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := l.inner.QueryRowContext(ctx, query, args...)
	l.logQuery(query, start, row.Err())
	return row
}
```

Create `internal/infra/db/db.go` (package `db`, next to `bun.go`; no names collide):

```go
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/migrate"
	"winterflow/internal/infra/db/pgsql"
	"winterflow/internal/infra/db/sqlite"
	"winterflow/pkg/logger"
)

// migrateTimeout bounds the startup migration run, including the wait for
// another replica's Postgres advisory lock.
const migrateTimeout = 5 * time.Minute

// DB is an open, migrated database: the pool, its driver name
// (backend.SQLite or backend.Postgres) and the transaction manager every
// repository is built on.
type DB struct {
	SQL    *sql.DB
	Driver string
	Tx     *backend.TxManager

	mu     sync.Mutex
	closed bool
}

// Open connects to DATABASE_URL (sqlite://<path> or postgres://...) and
// applies pending migrations. DEBUG_SQL=true logs every statement.
func Open(log *logger.Logger, url string) (*DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancel()

	var (
		sqldb  *sql.DB
		driver string
		err    error
	)
	switch {
	case strings.HasPrefix(url, sqlite.Scheme):
		driver = backend.SQLite
		sqldb, err = sqlite.Open(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("failed to setup SQLite: %w", err)
		}
	case strings.HasPrefix(url, pgsql.Scheme):
		driver = backend.Postgres
		sqldb, err = pgsql.Open(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("failed to setup PostgreSQL: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported database type in connection string: %s", url)
	}

	if err := migrate.Migrate(ctx, sqldb, driver); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	log.Info("Database connection established successfully")
	return &DB{
		SQL:    sqldb,
		Driver: driver,
		Tx:     backend.NewTxManager(sqldb, log, os.Getenv("DEBUG_SQL") == "true"),
	}, nil
}

// HealthCheck pings the database within 5 s.
func (d *DB) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.SQL.PingContext(ctx); err != nil {
		return fmt.Errorf("database health check failed: %w", err)
	}
	return nil
}

// Shutdown closes the pool; later calls are no-ops.
func (d *DB) Shutdown() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if err := d.SQL.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}
	return nil
}
```

Create `internal/infra/db/dbtest/dbtest.go`:

```go
// Package dbtest opens migrated throwaway databases for tests: a SQLite temp
// file by default, or a fresh schema on the Postgres server named by
// WF_TEST_DATABASE_URL (the contract suite's convention). Test-only: import
// it from _test.go files.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver for the admin connection

	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Env names the Postgres server tests run against; unset means SQLite.
const Env = "WF_TEST_DATABASE_URL"

// Log is a quiet logger for tests.
var Log = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})

// Driver reports the engine this run exercises. A WF_TEST_DATABASE_URL that
// is neither postgres:// nor postgresql:// fails the test.
func Driver(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv(Env)
	if dsn == "" {
		return backend.SQLite
	}
	scheme, _, _ := strings.Cut(dsn, "://")
	if scheme != "postgres" && scheme != "postgresql" {
		t.Fatalf("%s: unsupported scheme %q (want postgres:// or postgresql://, or unset for SQLite)", Env, scheme)
	}
	return backend.Postgres
}

// URL returns a DATABASE_URL for an empty database owned by t: a SQLite temp
// file, or a new schema t_<hex> on WF_TEST_DATABASE_URL selected with
// search_path and dropped on cleanup. The Postgres URL asks for a non-UTC
// session zone on purpose: db.Open must pin UTC itself.
func URL(t testing.TB) string {
	t.Helper()
	if Driver(t) == backend.SQLite {
		return "sqlite://" + filepath.Join(t.TempDir(), "wf.sqlite")
	}
	base := os.Getenv(Env)
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(b[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema on %s: %v", Env, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", Env, err)
	}
	u.Scheme = "postgres" // db.Open accepts only postgres://
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("timezone", "America/New_York")
	u.RawQuery = q.Encode()
	return u.String()
}

// Open returns a migrated database through the production db.Open, closed
// on cleanup.
func Open(t testing.TB) *db.DB {
	t.Helper()
	d, err := db.Open(Log, URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown() })
	return d
}

// Ctx is a context that ends after 30 s, so a repository that deadlocks on
// SQLite's single connection fails the test instead of hanging it.
func Ctx(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Rebind rewrites ? placeholders to $1, $2, ... for Postgres.
func Rebind(driver, query string) string {
	if driver != backend.Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

// Exec runs a ?-placeholder statement on d, failing the test on error.
func Exec(t testing.TB, d *db.DB, query string, args ...any) {
	t.Helper()
	if _, err := d.SQL.ExecContext(Ctx(t), Rebind(d.Driver, query), args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// QueryString runs a ?-placeholder query returning one text value.
func QueryString(t testing.TB, d *db.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := d.SQL.QueryRowContext(Ctx(t), Rebind(d.Driver, query), args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s
}

// ID returns a fixed, valid UUID for small n (Postgres columns are UUID):
// ID(1) = "00000000-0000-7000-8000-000000000001". IDs sort by n.
func ID(n int) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}

// SeedOrg inserts an organization row (name = its id).
func SeedOrg(t testing.TB, d *db.DB, orgID string) {
	t.Helper()
	Exec(t, d, `INSERT INTO organizations (organization_id, name, created_at) VALUES (?, ?, ?)`,
		orgID, orgID, types.NewDateTime())
}

// SeedServer inserts an organization and a claimed server it owns.
func SeedServer(t testing.TB, d *db.DB, orgID, serverID string) {
	t.Helper()
	SeedOrg(t, d, orgID)
	Exec(t, d, `INSERT INTO servers (server_id, organization_id, name, created_at) VALUES (?, ?, 'box', ?)`,
		serverID, orgID, types.NewDateTime())
}

// SeedMember inserts a user who is an owner of orgID.
func SeedMember(t testing.TB, d *db.DB, userID, orgID string) {
	t.Helper()
	now := types.NewDateTime()
	Exec(t, d, `INSERT INTO users (user_id, name, created_at, last_seen) VALUES (?, ?, ?, ?)`, userID, userID, now, now)
	Exec(t, d, `INSERT INTO organization_users (organization_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`, orgID, userID, now)
}

// SeedApp inserts an app row on serverID.
func SeedApp(t testing.TB, d *db.DB, appID, serverID, name string) {
	t.Helper()
	Exec(t, d, `INSERT INTO apps (app_id, server_id, name, icon, color, created_at) VALUES (?, ?, ?, 'i', '#000000', ?)`,
		appID, serverID, name, types.NewDateTime())
}
```

Run: `go test -count=1 ./internal/infra/db/ ./internal/infra/db/backend/ -v 2>&1 | grep -E '^(---|ok)'`
Expected: `--- PASS` for `TestOpenMigratesAndShutsDown`, `TestOpenRejectsUnknownScheme`, `TestOpenRejectsEmptySQLitePath`, `TestUTCDSNPinsTimeZone` (Bun's copy, still in `bun_test.go`), `TestAppDomainsKeyRebuildIsAtomic` (Bun), `TestWithTxCommits`, `TestWithTxRollsBackOnError`, `TestWithTxRollsBackOnPanic`, `TestNestedWithTxRollsBackToSavepoint`, `TestNestedPanicRollsBackEverything`, `TestQuerierOutsideTxIsThePool`, `TestDebugSQLLogsAndDelegates`; two `ok` lines.

- [ ] **Step 6: The same on Postgres**

Run: `make test-pg TEST_PG_ARGS='-run "TestOpen|TestWithTx|TestNested|TestQuerier|TestDebugSQL|TestUTCDSN" -v' 2>&1 | grep -E '^(---|ok|FAIL)'`
Expected: all of the above pass, now including `--- PASS: TestOpenPinsUTCSession`; `ok` for `internal/infra/db`, `internal/infra/db/backend`, `internal/infra/db/pgsql` and the contract package.

- [ ] **Step 7: Everything still green**

Run: `gofmt -l internal && go build ./... && go vet ./... && go test ./...`
Expected: no gofmt output; all `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/infra/db/types internal/infra/db/sqlite internal/infra/db/pgsql \
  internal/infra/db/backend/tx.go internal/infra/db/backend/tx_test.go \
  internal/infra/db/db.go internal/infra/db/db_test.go internal/infra/db/dbtest
git commit -m "feat(db): TxManager, sqlite/pgsql openers, db.Open and dbtest

db.Open picks modernc.org/sqlite or pgx/v5 (simple protocol, UTC session)
by URL scheme, migrates, and hands out a savepoint-aware TxManager
(DEBUG_SQL logs statements and durations). dbtest opens a migrated SQLite
temp file or a fresh Postgres schema (WF_TEST_DATABASE_URL). DateTime.Scan
returns UTC whatever zone the driver used. Not wired yet.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: `server_domains` repository (`repository/serverdomain`)

This is the reference shape every repository task repeats: `repo.go` (row/param aliases to `sqlitegen`, the private `querier`, `Repo`, `New`, port methods, hydrate), `sqlite.go` (passthrough), `pgsql.go` (struct conversion), `repo_test.go` (runs on SQLite, and on Postgres under `make test-pg`).

**Files:**
- Create: `internal/infra/db/repository/serverdomain/repo.go`, `sqlite.go`, `pgsql.go`
- Test: `internal/infra/db/repository/serverdomain/repo_test.go` (ports `internal/infra/db/repository/server_domain_test.go`, which stays until Task 10)

**Interfaces:**
- Consumes: `backend.TxManager`, `backend.DBTX`, `backend.SQLite`, `backend.Postgres`; `sqlitegen`/`pgsqlgen` `ListServerDomains`, `UpsertServerDomain`, `InsertServerDomain`, `DeleteServerDomain`, `DeleteServerDomainsByServer` and types `ServerDomain`, `UpsertServerDomainParams`, `InsertServerDomainParams`, `DeleteServerDomainParams`; `dbtest.Open`, `dbtest.Ctx`, `dbtest.ID`, `dbtest.SeedServer`, `dbtest.Exec`, `dbtest.QueryString`, `dbtest.Log`.
- Produces: `package serverdomain`; `type Repo struct{...}`; `func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error)` (unknown driver → error); `*Repo` implements `port.ServerDomainRepository` (`ListForServer`, `Upsert`, `Delete`, `ReplaceForServer`).

Bun semantics this must keep (from `repository/server_domain.go`): list ordered by `name ASC`, `NotAfter` returned in UTC, empty list is a non-nil empty slice; `Upsert` is `INSERT … ON CONFLICT (server_id, name) DO UPDATE SET tls, not_after, issuer, updated_at` with `updated_at = time.Now().UTC()` and `not_after` converted to UTC; `Delete` is silent when nothing matches; `ReplaceForServer` is one transaction that reads the current rows **inside** it and writes nothing when `sameDomains` says the answer is unchanged, else deletes the server's rows and inserts the new ones; `sameDomains` moves verbatim.

SQL this repository runs (created in Task 3 — do not edit here):

`internal/infra/db/sqlc/query/sqlite/server_domains.sql`
```sql
-- The server_domains index (mirrors each agent's domain registrations).

-- name: ListServerDomains :many
SELECT * FROM server_domains WHERE server_id = ? ORDER BY name ASC;

-- name: UpsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET tls = excluded.tls,
    not_after = excluded.not_after,
    issuer = excluded.issuer,
    updated_at = excluded.updated_at;

-- name: InsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteServerDomain :exec
DELETE FROM server_domains WHERE server_id = ? AND name = ?;

-- name: DeleteServerDomainsByServer :exec
DELETE FROM server_domains WHERE server_id = ?;
```

`internal/infra/db/sqlc/query/pgsql/server_domains.sql`
```sql
-- The server_domains index (mirrors each agent's domain registrations).

-- name: ListServerDomains :many
SELECT * FROM server_domains WHERE server_id = $1 ORDER BY name ASC;

-- name: UpsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (server_id, name) DO UPDATE
SET tls = EXCLUDED.tls,
    not_after = EXCLUDED.not_after,
    issuer = EXCLUDED.issuer,
    updated_at = EXCLUDED.updated_at;

-- name: InsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: DeleteServerDomain :exec
DELETE FROM server_domains WHERE server_id = $1 AND name = $2;

-- name: DeleteServerDomainsByServer :exec
DELETE FROM server_domains WHERE server_id = $1;
```

- [ ] **Step 1: Write the tests**

Create `internal/infra/db/repository/serverdomain/repo_test.go` (the Bun-era cases with UUID ids, plus atomicity, the stored timestamp format and the unknown driver; `TestServerDomainsReplaceForServer` uses names that sort the same under SQLite's binary and Postgres's locale collation — see Decision 11):

```go
package serverdomain

import (
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestServerDomainsUpsertListDelete(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)

	if err := repo.Upsert(ctx, srv1, model.RegisteredDomain{Name: "*.twillingate.app", TLS: "auto"}); err != nil {
		t.Fatal(err)
	}
	// Replacing the certificate updates the row in place.
	if err := repo.Upsert(ctx, srv1, model.RegisteredDomain{Name: "*.twillingate.app", TLS: "certificate", NotAfter: &notAfter, Issuer: "CF Origin"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, srv1, model.RegisteredDomain{Name: "twillingate.app", TLS: "off"}); err != nil {
		t.Fatal(err)
	}
	// The same name on another server is its own row.
	if err := repo.Upsert(ctx, srv2, model.RegisteredDomain{Name: "*.twillingate.app", TLS: "off"}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListForServer(ctx, srv1)
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if got[0].Name != "*.twillingate.app" || got[0].TLS != "certificate" || got[0].Issuer != "CF Origin" ||
		got[0].NotAfter == nil || !got[0].NotAfter.Equal(notAfter) || got[0].NotAfter.Location() != time.UTC {
		t.Errorf("certificate row = %+v", got[0])
	}
	if got[1].Name != "twillingate.app" || got[1].NotAfter != nil {
		t.Errorf("off row = %+v", got[1])
	}

	if err := repo.Delete(ctx, srv1, "twillingate.app"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, srv1); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, srv2); len(got) != 1 || got[0].TLS != "off" {
		t.Fatalf("other server touched: %+v", got)
	}
}

func TestServerDomainsReplaceForServer(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	_ = repo.Upsert(ctx, srv1, model.RegisteredDomain{Name: "old.app", TLS: "auto"})
	_ = repo.Upsert(ctx, srv2, model.RegisteredDomain{Name: "keep.app", TLS: "auto"})

	// Names whose order is the same under SQLite's binary collation and a
	// Postgres locale collation (which ignores punctuation such as "*.").
	if err := repo.ReplaceForServer(ctx, srv1, []model.RegisteredDomain{
		{Name: "b.app", TLS: "off"}, {Name: "a.app", TLS: "auto"},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.ListForServer(ctx, srv1)
	if len(got) != 2 || got[0].Name != "a.app" || got[1].Name != "b.app" {
		t.Fatalf("after replace: %+v", got)
	}
	if err := repo.ReplaceForServer(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, srv1); len(got) != 0 {
		t.Fatalf("empty replace left %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, srv2); len(got) != 1 {
		t.Fatalf("other server touched: %+v", got)
	}
}

// A failing insert rolls the whole replace back: the old rows survive.
func TestServerDomainsReplaceIsAtomic(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	_ = repo.Upsert(ctx, srv1, model.RegisteredDomain{Name: "old.app", TLS: "auto"})
	dup := []model.RegisteredDomain{{Name: "x.app", TLS: "auto"}, {Name: "x.app", TLS: "off"}}
	if err := repo.ReplaceForServer(ctx, srv1, dup); err == nil {
		t.Fatal("duplicate names accepted")
	}
	if got, _ := repo.ListForServer(ctx, srv1); len(got) != 1 || got[0].Name != "old.app" {
		t.Fatalf("after failed replace: %+v", got)
	}
}

// TestServerDomainsCascadeWithServer: removing a server drops its index.
func TestServerDomainsCascadeWithServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	_ = repo.Upsert(ctx, srv1, model.RegisteredDomain{Name: "a.app", TLS: "auto"})
	dbtest.Exec(t, d, `DELETE FROM servers WHERE server_id = ?`, srv1)
	if got, _ := repo.ListForServer(ctx, srv1); len(got) != 0 {
		t.Fatalf("rows survived their server: %+v", got)
	}
}

// TestServerDomainsReplaceSkipsUnchanged: an agent reconnect re-reports the
// same registrations; that must not rewrite the rows.
func TestServerDomainsReplaceSkipsUnchanged(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	rows := []model.RegisteredDomain{
		{Name: "a.app", TLS: model.DomainTLSAuto},
		{Name: "*.b.app", TLS: model.DomainTLSCertificate, NotAfter: &notAfter, Issuer: "CF"},
	}
	if err := repo.ReplaceForServer(ctx, srv1, rows); err != nil {
		t.Fatal(err)
	}
	stamps := func() map[string]string {
		out := map[string]string{}
		for _, name := range []string{"a.app", "*.b.app"} {
			out[name] = dbtest.QueryString(t, d, `SELECT CAST(updated_at AS TEXT) FROM server_domains WHERE server_id = ? AND name = ?`, srv1, name)
		}
		return out
	}
	before := stamps()
	// Force the clock past the stored second, then re-report in another order.
	time.Sleep(1100 * time.Millisecond)
	if err := repo.ReplaceForServer(ctx, srv1, []model.RegisteredDomain{rows[1], rows[0]}); err != nil {
		t.Fatal(err)
	}
	after := stamps()
	for name, at := range before {
		if after[name] != at {
			t.Errorf("%s rewritten although unchanged: %v -> %v", name, at, after[name])
		}
	}
	// A changed mode is written.
	if err := repo.ReplaceForServer(ctx, srv1, []model.RegisteredDomain{{Name: "a.app", TLS: model.DomainTLSOff}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, srv1); len(got) != 1 || got[0].TLS != model.DomainTLSOff {
		t.Fatalf("changed list not written: %+v", got)
	}
}

// Timestamps keep the Bun-era storage format: naive UTC
// "2006-01-02 15:04:05" text (TIMESTAMP on both engines here).
func TestServerDomainsStoredTimestampFormat(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC+3", 3*3600))
	if err := repo.Upsert(ctx, srv1, model.RegisteredDomain{Name: "a.app", TLS: "certificate", NotAfter: &notAfter}); err != nil {
		t.Fatal(err)
	}
	if got := dbtest.QueryString(t, d, `SELECT CAST(not_after AS TEXT) FROM server_domains WHERE name = 'a.app'`); got != "2027-01-02 00:04:05" {
		t.Fatalf("stored not_after = %q, want 2027-01-02 00:04:05", got)
	}
}

func TestSameDomains(t *testing.T) {
	t1 := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	d := func(name string, tls model.DomainTLS, na *time.Time, issuer string) model.RegisteredDomain {
		return model.RegisteredDomain{Name: name, TLS: tls, NotAfter: na, Issuer: issuer}
	}
	base := []model.RegisteredDomain{d("a.app", "auto", nil, ""), d("b.app", "certificate", &t1, "CF")}
	cases := map[string]struct {
		other []model.RegisteredDomain
		same  bool
	}{
		"reordered":   {[]model.RegisteredDomain{base[1], base[0]}, true},
		"fewer":       {base[:1], false},
		"renamed":     {[]model.RegisteredDomain{base[0], d("c.app", "certificate", &t1, "CF")}, false},
		"mode":        {[]model.RegisteredDomain{d("a.app", "off", nil, ""), base[1]}, false},
		"issuer":      {[]model.RegisteredDomain{base[0], d("b.app", "certificate", &t1, "LE")}, false},
		"expiry":      {[]model.RegisteredDomain{base[0], d("b.app", "certificate", &t2, "CF")}, false},
		"expiry gone": {[]model.RegisteredDomain{base[0], d("b.app", "certificate", nil, "CF")}, false},
		"sub-second":  {[]model.RegisteredDomain{base[0], d("b.app", "certificate", ptr(t1.Add(time.Millisecond)), "CF")}, true},
	}
	for name, tc := range cases {
		if got := sameDomains(base, tc.other); got != tc.same {
			t.Errorf("%s: sameDomains = %v, want %v", name, got, tc.same)
		}
	}
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/db/repository/serverdomain/`
Expected: build failure listing `undefined: Repo`, `undefined: New` and `undefined: sameDomains`.

- [ ] **Step 3: Implement**

Create `internal/infra/db/repository/serverdomain/repo.go`:

```go
// Package serverdomain implements port.ServerDomainRepository: the API's
// index of each agent's domain registrations (server_domains). The agent's
// domains/ dir stays authoritative; every method here is cache maintenance.
package serverdomain

import (
	"context"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	domainRow    = sqlitegen.ServerDomain
	upsertParams = sqlitegen.UpsertServerDomainParams
	insertParams = sqlitegen.InsertServerDomainParams
	deleteParams = sqlitegen.DeleteServerDomainParams
)

type querier interface {
	ListServerDomains(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error)
	UpsertServerDomain(ctx context.Context, db backend.DBTX, p upsertParams) error
	InsertServerDomain(ctx context.Context, db backend.DBTX, p insertParams) error
	DeleteServerDomain(ctx context.Context, db backend.DBTX, p deleteParams) error
	DeleteServerDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error
}

// Repo is the server_domains repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.ServerDomainRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("serverdomain repository: unknown database driver %q", driver)
}

// ListForServer returns the server's registrations sorted by name.
func (r *Repo) ListForServer(ctx context.Context, serverID string) ([]model.RegisteredDomain, error) {
	rows, err := r.q.ListServerDomains(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := make([]model.RegisteredDomain, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// Upsert records one registration (domain.create confirmed by the agent).
func (r *Repo) Upsert(ctx context.Context, serverID string, d model.RegisteredDomain) error {
	return r.q.UpsertServerDomain(ctx, r.tx.Querier(ctx), upsertParams(rowFor(serverID, d)))
}

// Delete drops one registration (domain.delete confirmed by the agent).
func (r *Repo) Delete(ctx context.Context, serverID, name string) error {
	return r.q.DeleteServerDomain(ctx, r.tx.Querier(ctx), deleteParams{ServerID: serverID, Name: name})
}

// ReplaceForServer makes the index mirror a domain.list answer. An answer
// equal to the stored rows (the common case: every agent reconnect sends
// one) writes nothing.
func (r *Repo) ReplaceForServer(ctx context.Context, serverID string, ds []model.RegisteredDomain) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		current, err := r.ListForServer(ctx, serverID)
		if err != nil {
			return err
		}
		if sameDomains(current, ds) {
			return nil
		}
		if err := r.q.DeleteServerDomainsByServer(ctx, r.tx.Querier(ctx), serverID); err != nil {
			return err
		}
		for _, d := range ds {
			if err := r.q.InsertServerDomain(ctx, r.tx.Querier(ctx), rowFor(serverID, d)); err != nil {
				return err
			}
		}
		return nil
	})
}

// rowFor is the row a registration is stored as: expiry in UTC, updated now.
func rowFor(serverID string, d model.RegisteredDomain) insertParams {
	p := insertParams{
		ServerID:  serverID,
		Name:      d.Name,
		Tls:       string(d.TLS),
		Issuer:    d.Issuer,
		UpdatedAt: types.DateTime(time.Now().UTC()),
	}
	if d.NotAfter != nil {
		na := types.DateTime(d.NotAfter.UTC())
		p.NotAfter = &na
	}
	return p
}

func hydrate(row domainRow) model.RegisteredDomain {
	d := model.RegisteredDomain{Name: row.Name, TLS: model.DomainTLS(row.Tls), Issuer: row.Issuer}
	if row.NotAfter != nil {
		na := row.NotAfter.Time().UTC()
		d.NotAfter = &na
	}
	return d
}

// sameDomains reports whether two registration lists hold the same rows,
// in any order (expiries compared to the second, as stored).
func sameDomains(a, b []model.RegisteredDomain) bool {
	if len(a) != len(b) {
		return false
	}
	byName := make(map[string]model.RegisteredDomain, len(a))
	for _, d := range a {
		byName[d.Name] = d
	}
	for _, d := range b {
		o, ok := byName[d.Name]
		if !ok || o.TLS != d.TLS || o.Issuer != d.Issuer || (o.NotAfter == nil) != (d.NotAfter == nil) ||
			(o.NotAfter != nil && !o.NotAfter.Truncate(time.Second).Equal(d.NotAfter.Truncate(time.Second))) {
			return false
		}
	}
	return true
}
```

Create `internal/infra/db/repository/serverdomain/sqlite.go`:

```go
package serverdomain

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListServerDomains(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error) {
	return sqlitegen.New(db).ListServerDomains(ctx, serverID)
}

func (sqliteQuerier) UpsertServerDomain(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return sqlitegen.New(db).UpsertServerDomain(ctx, p)
}

func (sqliteQuerier) InsertServerDomain(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertServerDomain(ctx, p)
}

func (sqliteQuerier) DeleteServerDomain(ctx context.Context, db backend.DBTX, p deleteParams) error {
	return sqlitegen.New(db).DeleteServerDomain(ctx, p)
}

func (sqliteQuerier) DeleteServerDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteServerDomainsByServer(ctx, serverID)
}
```

Create `internal/infra/db/repository/serverdomain/pgsql.go`:

```go
package serverdomain

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListServerDomains(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error) {
	rows, err := pgsqlgen.New(db).ListServerDomains(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]domainRow, len(rows))
	for i, row := range rows {
		out[i] = domainRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) UpsertServerDomain(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return pgsqlgen.New(db).UpsertServerDomain(ctx, pgsqlgen.UpsertServerDomainParams(p))
}

func (pgsqlQuerier) InsertServerDomain(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertServerDomain(ctx, pgsqlgen.InsertServerDomainParams(p))
}

func (pgsqlQuerier) DeleteServerDomain(ctx context.Context, db backend.DBTX, p deleteParams) error {
	return pgsqlgen.New(db).DeleteServerDomain(ctx, pgsqlgen.DeleteServerDomainParams(p))
}

func (pgsqlQuerier) DeleteServerDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteServerDomainsByServer(ctx, serverID)
}
```

- [ ] **Step 4: Run on SQLite**

Run: `go test -count=1 ./internal/infra/db/repository/serverdomain/ -v 2>&1 | grep -E '^(---|ok)'`
Expected: `--- PASS` for `TestNewRejectsUnknownDriver`, `TestServerDomainsUpsertListDelete`, `TestServerDomainsReplaceForServer`, `TestServerDomainsReplaceIsAtomic`, `TestServerDomainsCascadeWithServer`, `TestServerDomainsReplaceSkipsUnchanged` (sleeps 1.1 s by design), `TestServerDomainsStoredTimestampFormat`, `TestSameDomains`; `ok`.

- [ ] **Step 5: Run on Postgres**

Run: `make test-pg TEST_PG_ARGS='-run "TestServerDomains|TestSameDomains|TestNewRejectsUnknownDriver" -v' 2>&1 | grep -E 'serverdomain|^--- FAIL'`
Expected: `ok  winterflow/internal/infra/db/repository/serverdomain`, no `--- FAIL`.

- [ ] **Step 6: Everything still green**

Run: `gofmt -l internal && go build ./... && go vet ./... && go test ./...`
Expected: no gofmt output; all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db/repository/serverdomain
git commit -m "refactor(db): server_domains repository on sqlc

Same behaviour as the Bun repository (ordering, upsert columns, unchanged
reports write nothing), now one package per aggregate with per-engine SQL.
Tests run on SQLite and, under make test-pg, on Postgres. Not wired yet.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: `app_domains` repository (`repository/appdomain`)

**Files:**
- Create: `internal/infra/db/repository/appdomain/repo.go`, `sqlite.go`, `pgsql.go`
- Test: `internal/infra/db/repository/appdomain/repo_test.go` (ports `internal/infra/db/repository/app_domain_test.go`, which stays until Task 10)

**Interfaces:**
- Consumes: `backend.TxManager`/`DBTX`/driver constants; `sqlitegen`/`pgsqlgen` `GetDomainClaim`, `InsertAppDomain`, `DeleteAppDomainsByApp`, `DeleteAppDomainsByServer`, `ListAppDomainsByServer`, `ListAppDomainNames` and types `AppDomain`, `GetDomainClaimRow`, `GetDomainClaimParams`, `InsertAppDomainParams`, `ListAppDomainNamesParams`; `dbtest.Open`, `Ctx`, `ID`, `SeedServer`, `SeedApp`, `QueryString`, `Log`.
- Produces: `package appdomain`; `func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error)`; `*Repo` implements `port.AppDomainRepository` (`FindClaims`, `ReplaceForApp`, `DeleteForApp`, `ReplaceForServer`, `ListForServer`, `ListForApp`).

Bun semantics this must keep (from `repository/app_domain.go`):
- `rowsFor`: routes plus domain-level redirects (path redirects produce no row); `updated_at = time.Now().UTC()`; a route stores `target = ''`, `code = 0`; a redirect stores `upstream_port = 0` (Bun omitted those zero-valued `default:` columns, so the column defaults applied — same values).
- `FindClaims`: empty `domains` → `nil, nil`; otherwise rows on `serverID` for any of the domains, minus `excludeAppID`'s own (when non-empty), sorted by domain, each with `COALESCE(apps.name, '')` (LEFT JOIN — a claim whose app row is gone has an empty name); no claims → `nil`. Implemented as one primary-key lookup per distinct domain (Decision 3).
- `ReplaceForApp`: `nil` ingress → no-op; otherwise one transaction: delete **every** row of the app (`WHERE app_id = ?`, all servers — as Bun did), insert the new rows. `DeleteForApp`: delete every row of the app.
- `ReplaceForServer`: apps sorted by id, first claim of a domain wins (dedup before inserting), one transaction: delete the server's rows, insert.
- `ListForServer`: ordered by domain, grouped by app id, always a non-nil map. `ListForApp`: hostnames of `appID` on `serverID`, ordered.

SQL this repository runs (created in Task 3 — do not edit here):

`internal/infra/db/sqlc/query/sqlite/app_domains.sql`
```sql
-- The app_domains ingress index (rebuildable from the agents' app configs).

-- name: GetDomainClaim :one
SELECT d.domain, d.app_id, d.server_id, COALESCE(a.name, '') AS app_name
FROM app_domains AS d
LEFT JOIN apps AS a ON a.app_id = d.app_id
WHERE d.server_id = ? AND d.domain = ?;

-- name: InsertAppDomain :exec
INSERT INTO app_domains (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAppDomainsByApp :exec
DELETE FROM app_domains WHERE app_id = ?;

-- name: DeleteAppDomainsByServer :exec
DELETE FROM app_domains WHERE server_id = ?;

-- name: ListAppDomainsByServer :many
SELECT * FROM app_domains WHERE server_id = ? ORDER BY domain ASC;

-- name: ListAppDomainNames :many
SELECT domain FROM app_domains
WHERE server_id = ? AND app_id = ?
ORDER BY domain ASC;
```

`internal/infra/db/sqlc/query/pgsql/app_domains.sql`
```sql
-- The app_domains ingress index (rebuildable from the agents' app configs).

-- name: GetDomainClaim :one
SELECT d.domain, d.app_id, d.server_id, COALESCE(a.name, '') AS app_name
FROM app_domains AS d
LEFT JOIN apps AS a ON a.app_id = d.app_id
WHERE d.server_id = $1 AND d.domain = $2;

-- name: InsertAppDomain :exec
INSERT INTO app_domains (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: DeleteAppDomainsByApp :exec
DELETE FROM app_domains WHERE app_id = $1;

-- name: DeleteAppDomainsByServer :exec
DELETE FROM app_domains WHERE server_id = $1;

-- name: ListAppDomainsByServer :many
SELECT * FROM app_domains WHERE server_id = $1 ORDER BY domain ASC;

-- name: ListAppDomainNames :many
SELECT domain FROM app_domains
WHERE server_id = $1 AND app_id = $2
ORDER BY domain ASC;
```

- [ ] **Step 1: Write the tests**

Create `internal/infra/db/repository/appdomain/repo_test.go` (apps are seeded with `dbtest.SeedApp`, not through the app repository):

```go
package appdomain

import (
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2       = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2       = dbtest.ID(11), dbtest.ID(12)
	app1, app2, app3 = dbtest.ID(21), dbtest.ID(22), dbtest.ID(23)
	appA, appZ       = dbtest.ID(31), dbtest.ID(39) // appA sorts first
	appNew           = dbtest.ID(40)                // never stored
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func ingressOf(domains ...model.IngressDomain) *model.Ingress {
	return &model.Ingress{Domains: domains, Redirects: []model.IngressRedirect{}}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestReplaceAndFindClaims(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, app1, srv1, "Ghost")
	dbtest.SeedApp(t, d, app2, srv1, "Blog2")

	ing := &model.Ingress{
		Domains: []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true}},
		Redirects: []model.IngressRedirect{
			{Domain: "www.example.com", To: "https://blog.example.com", Code: 301, SSL: true},
			// Path rule: must NOT produce a row.
			{Domain: "blog.example.com", Path: "/old/*", To: "https://blog.example.com/new", Code: 302},
		},
	}
	if err := repo.ReplaceForApp(ctx, app1, srv1, ing); err != nil {
		t.Fatal(err)
	}

	// Another app asking for a held domain conflicts, with names attached;
	// a domain listed twice is reported once.
	claims, err := repo.FindClaims(ctx, srv1, []string{"blog.example.com", "free.example.com", "blog.example.com"}, app2)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Domain != "blog.example.com" || claims[0].AppName != "Ghost" || claims[0].AppID != app1 || claims[0].ServerID != srv1 {
		t.Fatalf("claims = %+v", claims)
	}

	// Claims come back sorted by domain.
	claims, _ = repo.FindClaims(ctx, srv1, []string{"www.example.com", "blog.example.com"}, "")
	if len(claims) != 2 || claims[0].Domain != "blog.example.com" || claims[1].Domain != "www.example.com" {
		t.Fatalf("unsorted claims: %+v", claims)
	}

	// The owner re-saving is not a conflict.
	claims, _ = repo.FindClaims(ctx, srv1, []string{"blog.example.com"}, app1)
	if len(claims) != 0 {
		t.Fatalf("own claims reported as conflicts: %+v", claims)
	}
	if claims, err := repo.FindClaims(ctx, srv1, nil, app1); claims != nil || err != nil {
		t.Fatalf("no domains = %+v, %v", claims, err)
	}

	// Replace shrinks: dropping the redirect frees www.
	if err := repo.ReplaceForApp(ctx, app1, srv1, ingressOf(model.IngressDomain{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true})); err != nil {
		t.Fatal(err)
	}
	claims, _ = repo.FindClaims(ctx, srv1, []string{"www.example.com"}, app2)
	if len(claims) != 0 {
		t.Fatalf("stale row survived replace: %+v", claims)
	}

	// nil ingress = no-op (old client edit must not wipe rows).
	if err := repo.ReplaceForApp(ctx, app1, srv1, nil); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListForServer(ctx, srv1)
	if err != nil || len(list[app1]) != 1 || !list[app1][0].SSL || list[app1][0].Kind != "route" {
		t.Fatalf("ListForServer after nil replace = %+v, %v", list, err)
	}

	// Empty (non-nil) ingress deletes rows.
	if err := repo.ReplaceForApp(ctx, app1, srv1, &model.Ingress{}); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListForServer(ctx, srv1)
	if len(list[app1]) != 0 {
		t.Fatalf("rows survived empty replace: %+v", list)
	}
}

// A claim whose app row is gone still reports, with an empty name.
func TestFindClaimsWithoutAppRow(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if err := repo.ReplaceForApp(ctx, app3, srv1, ingressOf(model.IngressDomain{Domain: "orphan.example.com", UpstreamPort: 80})); err != nil {
		t.Fatal(err)
	}
	claims, err := repo.FindClaims(ctx, srv1, []string{"orphan.example.com"}, "")
	if err != nil || len(claims) != 1 || claims[0].AppName != "" || claims[0].AppID != app3 {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
}

// ReplaceForApp is one transaction: a colliding insert keeps the old rows.
func TestReplaceForAppIsAtomic(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, app1, srv1, "One")
	dbtest.SeedApp(t, d, app2, srv1, "Two")
	_ = repo.ReplaceForApp(ctx, app1, srv1, ingressOf(model.IngressDomain{Domain: "one.example.com", UpstreamPort: 81}))
	_ = repo.ReplaceForApp(ctx, app2, srv1, ingressOf(model.IngressDomain{Domain: "two.example.com", UpstreamPort: 82}))
	// app1 now asks for app2's hostname: the insert collides on the key.
	err := repo.ReplaceForApp(ctx, app1, srv1, ingressOf(model.IngressDomain{Domain: "two.example.com", UpstreamPort: 81}))
	if err == nil {
		t.Fatal("colliding replace succeeded")
	}
	if got, _ := repo.ListForApp(ctx, srv1, app1); len(got) != 1 || got[0] != "one.example.com" {
		t.Fatalf("app1 rows after failed replace = %v", got)
	}
}

func TestDeleteForAppAndReplaceForServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, app1, srv1, "One")
	dbtest.SeedApp(t, d, app2, srv1, "Two")

	_ = repo.ReplaceForApp(ctx, app1, srv1, ingressOf(model.IngressDomain{Domain: "one.example.com", UpstreamPort: 81}))
	_ = repo.ReplaceForApp(ctx, app2, srv1, ingressOf(model.IngressDomain{Domain: "two.example.com", UpstreamPort: 82}))

	if err := repo.DeleteForApp(ctx, app1); err != nil {
		t.Fatal(err)
	}
	list, _ := repo.ListForServer(ctx, srv1)
	if len(list[app1]) != 0 || len(list[app2]) != 1 {
		t.Fatalf("after delete: %+v", list)
	}

	// Reconcile: agent now reports app2 without ingress and app3 with one.
	repApps := []model.App{
		{ID: app2, Name: "Two"},
		{ID: app3, Name: "Three", Ingress: ingressOf(model.IngressDomain{Domain: "three.example.com", UpstreamPort: 83})},
	}
	if err := repo.ReplaceForServer(ctx, srv1, repApps); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListForServer(ctx, srv1)
	if len(list[app2]) != 0 || len(list[app3]) != 1 || list[app3][0].Domain != "three.example.com" {
		t.Fatalf("after reconcile: %+v", list)
	}
}

// TestReplaceForServerDedupsDuplicateDomain: a concurrent-save race can
// leave two apps on disk both claiming one domain (BuildConfig resolves it
// by sorted AppID, first claim wins). ReplaceForServer must mirror that so
// exactly one row survives and the reconcile still succeeds.
func TestReplaceForServerDedupsDuplicateDomain(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, appA, srv1, "Alpha")
	dbtest.SeedApp(t, d, appZ, srv1, "Zulu")

	repApps := []model.App{
		{ID: appZ, Name: "Zulu", Ingress: ingressOf(model.IngressDomain{Domain: "dup.example.com", UpstreamPort: 90})},
		{ID: appA, Name: "Alpha", Ingress: ingressOf(model.IngressDomain{Domain: "dup.example.com", UpstreamPort: 91})},
	}
	if err := repo.ReplaceForServer(ctx, srv1, repApps); err != nil {
		t.Fatalf("ReplaceForServer with duplicate domain returned error: %v", err)
	}
	list, err := repo.ListForServer(ctx, srv1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list[appZ]) != 0 {
		t.Fatalf("later app in sort order kept the domain: %+v", list)
	}
	if len(list[appA]) != 1 || list[appA][0].Domain != "dup.example.com" {
		t.Fatalf("sorted-first app did not keep the domain: %+v", list)
	}
}

// TestHostnamesAreUniquePerServer: two servers may index the same hostname
// (DNS round robin); claims are looked up on one server only.
func TestHostnamesAreUniquePerServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedApp(t, d, app1, srv1, "One")
	dbtest.SeedApp(t, d, app2, srv2, "Two")
	shared := ingressOf(model.IngressDomain{Domain: "shop.example.com", UpstreamPort: 80})
	if err := repo.ReplaceForApp(ctx, app1, srv1, shared); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceForApp(ctx, app2, srv2, shared); err != nil {
		t.Fatalf("second server could not index the same hostname: %v", err)
	}
	for server, owner := range map[string]string{srv1: "One", srv2: "Two"} {
		claims, err := repo.FindClaims(ctx, server, []string{"shop.example.com"}, appNew)
		if err != nil || len(claims) != 1 || claims[0].AppName != owner || claims[0].ServerID != server {
			t.Fatalf("%s claims = %+v, %v", server, claims, err)
		}
	}
	// A reconcile of one server leaves the other's row alone.
	if err := repo.ReplaceForServer(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if claims, _ := repo.FindClaims(ctx, srv2, []string{"shop.example.com"}, ""); len(claims) != 1 {
		t.Fatalf("srv2 row lost: %+v", claims)
	}
}

func TestListForApp(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, app1, srv1, "Ghost")
	dbtest.SeedApp(t, d, app2, srv1, "Blog")
	ing := &model.Ingress{
		Domains:   []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 1}},
		Redirects: []model.IngressRedirect{{Domain: "www.example.com", To: "https://blog.example.com", Code: 301}},
	}
	if err := repo.ReplaceForApp(ctx, app1, srv1, ing); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceForApp(ctx, app2, srv1, ingressOf(model.IngressDomain{Domain: "other.example.com", UpstreamPort: 2})); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListForApp(ctx, srv1, app1)
	if err != nil || len(got) != 2 || got[0] != "blog.example.com" || got[1] != "www.example.com" {
		t.Fatalf("ListForApp = %v, %v", got, err)
	}
	if got, _ := repo.ListForApp(ctx, srv2, app1); len(got) != 0 {
		t.Fatalf("another server's rows: %v", got)
	}
}

// The stored row keeps every column the Bun model wrote.
func TestStoredRedirectRow(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	ing := &model.Ingress{Redirects: []model.IngressRedirect{{Domain: "www.example.com", To: "https://x.example.com", Code: 308, SSL: true}}}
	if err := repo.ReplaceForApp(ctx, app1, srv1, ing); err != nil {
		t.Fatal(err)
	}
	got := dbtest.QueryString(t, d, `SELECT kind || '|' || target || '|' || CAST(code AS TEXT) || '|' || CAST(upstream_port AS TEXT) FROM app_domains WHERE domain = 'www.example.com'`)
	if got != "redirect|https://x.example.com|308|0" {
		t.Fatalf("stored redirect row = %q", got)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/db/repository/appdomain/`
Expected: build failure listing `undefined: Repo` and `undefined: New`.

- [ ] **Step 3: Implement**

Create `internal/infra/db/repository/appdomain/repo.go`:

```go
// Package appdomain implements port.AppDomainRepository: the app_domains
// ingress index, one row per hostname an app claims. The agent filesystem
// stays authoritative; every method here is cache maintenance.
package appdomain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

type (
	domainRow    = sqlitegen.AppDomain
	claimRow     = sqlitegen.GetDomainClaimRow
	claimParams  = sqlitegen.GetDomainClaimParams
	insertParams = sqlitegen.InsertAppDomainParams
	namesParams  = sqlitegen.ListAppDomainNamesParams
)

type querier interface {
	GetDomainClaim(ctx context.Context, db backend.DBTX, p claimParams) (claimRow, error)
	InsertAppDomain(ctx context.Context, db backend.DBTX, p insertParams) error
	DeleteAppDomainsByApp(ctx context.Context, db backend.DBTX, appID string) error
	DeleteAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error
	ListAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error)
	ListAppDomainNames(ctx context.Context, db backend.DBTX, p namesParams) ([]string, error)
}

// Repo is the app_domains repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.AppDomainRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("appdomain repository: unknown database driver %q", driver)
}

// rowsFor flattens an ingress into index rows: routes plus domain-level
// redirect sources. Path rules produce no rows (they cannot conflict).
func rowsFor(appID, serverID string, ing *model.Ingress) []insertParams {
	if ing == nil {
		return nil
	}
	now := types.DateTime(time.Now().UTC())
	var rows []insertParams
	for _, d := range ing.Domains {
		rows = append(rows, insertParams{
			Domain: d.Domain, AppID: appID, ServerID: serverID,
			Kind: "route", Ssl: d.SSL, UpstreamPort: int64(d.UpstreamPort), UpdatedAt: now,
		})
	}
	for _, r := range ing.Redirects {
		if r.Path != "" {
			continue
		}
		rows = append(rows, insertParams{
			Domain: r.Domain, AppID: appID, ServerID: serverID,
			Kind: "redirect", Ssl: r.SSL, Target: r.To, Code: int64(r.Code), UpdatedAt: now,
		})
	}
	return rows
}

// FindClaims returns the rows on serverID holding any of domains, minus
// excludeAppID's own, sorted by domain, each with its app's name ("" when
// the app row is gone). (server_id, domain) is the key, so each domain has
// at most one claim: one keyed lookup per distinct domain.
func (r *Repo) FindClaims(ctx context.Context, serverID string, domains []string, excludeAppID string) ([]model.DomainClaim, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	uniq := make([]string, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, d := range domains {
		if !seen[d] {
			seen[d] = true
			uniq = append(uniq, d)
		}
	}
	sort.Strings(uniq)
	var claims []model.DomainClaim
	for _, d := range uniq {
		row, err := r.q.GetDomainClaim(ctx, r.tx.Querier(ctx), claimParams{ServerID: serverID, Domain: d})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if excludeAppID != "" && row.AppID == excludeAppID {
			continue
		}
		claims = append(claims, model.DomainClaim{Domain: row.Domain, AppID: row.AppID, AppName: row.AppName, ServerID: row.ServerID})
	}
	return claims, nil
}

// ReplaceForApp deletes the app's rows (on every server) and inserts the new
// set, in one transaction. nil ingress = no-op (old clients).
func (r *Repo) ReplaceForApp(ctx context.Context, appID, serverID string, ing *model.Ingress) error {
	if ing == nil {
		// Old client / config without the key: leave the index alone.
		return nil
	}
	rows := rowsFor(appID, serverID, ing)
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.q.DeleteAppDomainsByApp(ctx, r.tx.Querier(ctx), appID); err != nil {
			return err
		}
		return r.insert(ctx, rows)
	})
}

// DeleteForApp drops every row of the app.
func (r *Repo) DeleteForApp(ctx context.Context, appID string) error {
	return r.q.DeleteAppDomainsByApp(ctx, r.tx.Querier(ctx), appID)
}

// ReplaceForServer rebuilds the whole server's index from the agent's
// reported apps (apps.list reconcile). (server_id, domain) is the PK, so two
// apps claiming the same domain would collide on INSERT and roll back the
// whole reconcile, leaving the table stale forever with no way to self-heal.
// The agent can legitimately reach that state for a moment (BuildConfig in
// internal/infra/ingress/caddy/config.go accepts a concurrent-save race and
// resolves it by processing apps in sorted-AppID order, first claim wins).
// Dedup here the same way so the DB index always agrees with what Caddy is
// actually serving, and the INSERT never collides.
func (r *Repo) ReplaceForServer(ctx context.Context, serverID string, apps []model.App) error {
	sorted := make([]model.App, len(apps))
	copy(sorted, apps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var rows []insertParams
	claimed := map[string]bool{}
	for _, a := range sorted {
		for _, row := range rowsFor(a.ID, serverID, a.Ingress) {
			if claimed[row.Domain] {
				continue
			}
			claimed[row.Domain] = true
			rows = append(rows, row)
		}
	}
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.q.DeleteAppDomainsByServer(ctx, r.tx.Querier(ctx), serverID); err != nil {
			return err
		}
		return r.insert(ctx, rows)
	})
}

func (r *Repo) insert(ctx context.Context, rows []insertParams) error {
	for _, row := range rows {
		if err := r.q.InsertAppDomain(ctx, r.tx.Querier(ctx), row); err != nil {
			return err
		}
	}
	return nil
}

// ListForServer returns display rows grouped by app id (always non-nil).
func (r *Repo) ListForServer(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error) {
	rows, err := r.q.ListAppDomainsByServer(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := map[string][]model.AppDomainInfo{}
	for _, row := range rows {
		out[row.AppID] = append(out[row.AppID], model.AppDomainInfo{Domain: row.Domain, SSL: row.Ssl, Kind: row.Kind})
	}
	return out, nil
}

// ListForApp returns the hostnames appID holds on serverID, sorted.
func (r *Repo) ListForApp(ctx context.Context, serverID, appID string) ([]string, error) {
	return r.q.ListAppDomainNames(ctx, r.tx.Querier(ctx), namesParams{ServerID: serverID, AppID: appID})
}
```

Create `internal/infra/db/repository/appdomain/sqlite.go`:

```go
package appdomain

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) GetDomainClaim(ctx context.Context, db backend.DBTX, p claimParams) (claimRow, error) {
	return sqlitegen.New(db).GetDomainClaim(ctx, p)
}

func (sqliteQuerier) InsertAppDomain(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertAppDomain(ctx, p)
}

func (sqliteQuerier) DeleteAppDomainsByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return sqlitegen.New(db).DeleteAppDomainsByApp(ctx, appID)
}

func (sqliteQuerier) DeleteAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteAppDomainsByServer(ctx, serverID)
}

func (sqliteQuerier) ListAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error) {
	return sqlitegen.New(db).ListAppDomainsByServer(ctx, serverID)
}

func (sqliteQuerier) ListAppDomainNames(ctx context.Context, db backend.DBTX, p namesParams) ([]string, error) {
	return sqlitegen.New(db).ListAppDomainNames(ctx, p)
}
```

Create `internal/infra/db/repository/appdomain/pgsql.go`:

```go
package appdomain

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) GetDomainClaim(ctx context.Context, db backend.DBTX, p claimParams) (claimRow, error) {
	row, err := pgsqlgen.New(db).GetDomainClaim(ctx, pgsqlgen.GetDomainClaimParams(p))
	return claimRow(row), err
}

func (pgsqlQuerier) InsertAppDomain(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertAppDomain(ctx, pgsqlgen.InsertAppDomainParams(p))
}

func (pgsqlQuerier) DeleteAppDomainsByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return pgsqlgen.New(db).DeleteAppDomainsByApp(ctx, appID)
}

func (pgsqlQuerier) DeleteAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteAppDomainsByServer(ctx, serverID)
}

func (pgsqlQuerier) ListAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error) {
	rows, err := pgsqlgen.New(db).ListAppDomainsByServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]domainRow, len(rows))
	for i, row := range rows {
		out[i] = domainRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListAppDomainNames(ctx context.Context, db backend.DBTX, p namesParams) ([]string, error) {
	return pgsqlgen.New(db).ListAppDomainNames(ctx, pgsqlgen.ListAppDomainNamesParams(p))
}
```

- [ ] **Step 4: Run on SQLite**

Run: `go test -count=1 ./internal/infra/db/repository/appdomain/ -v 2>&1 | grep -E '^(---|ok)'`
Expected: `--- PASS` for `TestNewRejectsUnknownDriver`, `TestReplaceAndFindClaims`, `TestFindClaimsWithoutAppRow`, `TestReplaceForAppIsAtomic`, `TestDeleteForAppAndReplaceForServer`, `TestReplaceForServerDedupsDuplicateDomain`, `TestHostnamesAreUniquePerServer`, `TestListForApp`, `TestStoredRedirectRow`; `ok`.

- [ ] **Step 5: Run on Postgres**

Run: `make test-pg TEST_PG_ARGS='-run "TestReplace|TestFindClaims|TestDeleteForApp|TestHostnames|TestListForApp|TestStoredRedirectRow|TestNewRejectsUnknownDriver" -v' 2>&1 | grep -E 'appdomain|^--- FAIL'`
Expected: `ok  winterflow/internal/infra/db/repository/appdomain`, no `--- FAIL`.

- [ ] **Step 6: Everything still green**

Run: `gofmt -l internal && go build ./... && go vet ./... && go test ./...`
Expected: no gofmt output; all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db/repository/appdomain
git commit -m "refactor(db): app_domains repository on sqlc

FindClaims looks each distinct domain up by its (server_id, domain) key
instead of a variable IN list, which sqlc cannot express on both engines;
results, ordering and the replace/dedup semantics are unchanged.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: `apps` repository (`repository/app`)

**Files:**
- Create: `internal/infra/db/repository/app/repo.go`, `sqlite.go`, `pgsql.go`
- Test: `internal/infra/db/repository/app/repo_test.go` (ports `internal/infra/db/repository/app_test.go`, which stays until Task 10)

**Interfaces:**
- Consumes: `backend.TxManager`/`DBTX`/driver constants; `sqlitegen`/`pgsqlgen` `ListAppsByServer`, `ListAppIDsByServer`, `GetApp`, `UpsertApp`, `DeleteApp`, `RenameApp` and types `App`, `UpsertAppParams`, `RenameAppParams`; `types.NewDateTime`; `dbtest.Open`, `Ctx`, `ID`, `SeedServer`, `QueryString`, `Log`.
- Produces: `package app`; `func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error)`; `*Repo` implements `port.AppRepository` (`GetApps`, `GetApp`, `SaveApp`, `DeleteApp`, `RenameApp`, `SyncApps`).

Bun semantics this must keep (from `repository/app.go`):
- `GetApps`: `WHERE server_id = ? ORDER BY created_at ASC, app_id ASC` (the tie-break keeps both engines in the same order), non-nil empty slice.
- `GetApp`: `sql.ErrNoRows` → `model.ErrAppNotFound`; a NULL `template_id` hydrates to `""`.
- `SaveApp`: zero `CreatedAt` → now; `TemplateID == ""` → NULL; `INSERT … ON CONFLICT (app_id) DO UPDATE SET name, version, icon, color, template_id` — an existing row keeps its `server_id` and `created_at`.
- `DeleteApp`, `RenameApp`: no error when the app does not exist (Bun never checked affected rows).
- `SyncApps`: one transaction; every reported app is upserted with `ServerID` forced to `serverID`; then every app of that server that was not reported is deleted (Decision 4 — Bun used `NOT IN (...)`, or deleted all when the report was empty).

SQL this repository runs (created in Task 3 — do not edit here):

`internal/infra/db/sqlc/query/sqlite/apps.sql`
```sql
-- The apps catalog (a reconciled cache of what agents report).

-- name: ListAppsByServer :many
SELECT * FROM apps
WHERE server_id = ?
ORDER BY created_at ASC, app_id ASC;

-- name: ListAppIDsByServer :many
SELECT app_id FROM apps WHERE server_id = ?;

-- name: GetApp :one
SELECT * FROM apps WHERE app_id = ?;

-- name: UpsertApp :exec
INSERT INTO apps (app_id, server_id, template_id, name, version, icon, color, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (app_id) DO UPDATE
SET name = excluded.name,
    version = excluded.version,
    icon = excluded.icon,
    color = excluded.color,
    template_id = excluded.template_id;

-- name: DeleteApp :exec
DELETE FROM apps WHERE app_id = ?;

-- name: RenameApp :exec
UPDATE apps SET name = ? WHERE app_id = ?;
```

`internal/infra/db/sqlc/query/pgsql/apps.sql`
```sql
-- The apps catalog (a reconciled cache of what agents report).

-- name: ListAppsByServer :many
SELECT * FROM apps
WHERE server_id = $1
ORDER BY created_at ASC, app_id ASC;

-- name: ListAppIDsByServer :many
SELECT app_id FROM apps WHERE server_id = $1;

-- name: GetApp :one
SELECT * FROM apps WHERE app_id = $1;

-- name: UpsertApp :exec
INSERT INTO apps (app_id, server_id, template_id, name, version, icon, color, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (app_id) DO UPDATE
SET name = EXCLUDED.name,
    version = EXCLUDED.version,
    icon = EXCLUDED.icon,
    color = EXCLUDED.color,
    template_id = EXCLUDED.template_id;

-- name: DeleteApp :exec
DELETE FROM apps WHERE app_id = $1;

-- name: RenameApp :exec
UPDATE apps SET name = $1 WHERE app_id = $2;
```

- [ ] **Step 1: Write the tests**

Create `internal/infra/db/repository/app/repo_test.go`:

```go
package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	appA, appB = dbtest.ID(21), dbtest.ID(22) // appA < appB < appC
	appC       = dbtest.ID(23)
	tmpl       = dbtest.ID(90)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	// Apps reference a server row; seed one.
	dbtest.SeedServer(t, d, org1, srv1)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestAppCRUDRoundTrip(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)

	a := model.App{ID: appA, ServerID: srv1, Name: "grafana", Version: "1", Icon: "chart", Color: "#123456", TemplateID: tmpl}
	if err := repo.SaveApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetApp(ctx, appA)
	if err != nil || got.Name != "grafana" || got.ServerID != srv1 || got.TemplateID != tmpl || got.CreatedAt.IsZero() {
		t.Fatalf("GetApp = %+v, %v", got, err)
	}

	// Upsert on conflict: same id, new name, template cleared.
	a.Name, a.TemplateID = "grafana-2", ""
	if err := repo.SaveApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetApp(ctx, appA)
	if got.Name != "grafana-2" || got.TemplateID != "" {
		t.Fatalf("upsert = %+v", got)
	}

	if err := repo.RenameApp(ctx, appA, "renamed"); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetApp(ctx, appA)
	if got.Name != "renamed" {
		t.Fatalf("renamed = %q", got.Name)
	}

	list, err := repo.GetApps(ctx, srv1)
	if err != nil || len(list) != 1 {
		t.Fatalf("GetApps = %+v, %v", list, err)
	}

	if err := repo.DeleteApp(ctx, appA); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetApp(ctx, appA); !errors.Is(err, model.ErrAppNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
}

// The upsert keeps the stored server_id and created_at: only name,
// version, icon, color and template_id change.
func TestSaveAppKeepsServerAndCreatedAt(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org2, srv2)
	first := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := repo.SaveApp(ctx, model.App{ID: appA, ServerID: srv1, Name: "a", CreatedAt: first}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveApp(ctx, model.App{ID: appA, ServerID: srv2, Name: "b", Version: "2", Icon: "x", Color: "#ffffff", CreatedAt: first.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetApp(ctx, appA)
	if got.ServerID != srv1 || !got.CreatedAt.Equal(first) || got.Name != "b" || got.Version != "2" || got.Icon != "x" || got.Color != "#ffffff" {
		t.Fatalf("after second save = %+v", got)
	}
}

func TestSyncAppsMirrorsAgentState(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org2, srv2)
	for _, a := range []model.App{
		{ID: appA, ServerID: srv1, Name: "keep"},
		{ID: appB, ServerID: srv1, Name: "drop"},
		{ID: appC, ServerID: srv2, Name: "other server"},
	} {
		if err := repo.SaveApp(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	newApp := dbtest.ID(24)
	// Agent reports: keep (renamed) + a new app; "drop" is gone.
	if err := repo.SyncApps(ctx, srv1, []model.App{
		{ID: appA, Name: "keep-updated"},
		{ID: newApp, Name: "brand-new"},
	}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.GetApps(ctx, srv1)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, a := range list {
		names[a.ID] = a.Name
	}
	if len(list) != 2 || names[appA] != "keep-updated" || names[newApp] != "brand-new" {
		t.Fatalf("after sync: %v", names)
	}
	if other, _ := repo.GetApps(ctx, srv2); len(other) != 1 {
		t.Fatalf("another server's apps touched: %+v", other)
	}
}

func TestSyncAppsEmptyReportDeletesAll(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if err := repo.SaveApp(ctx, model.App{ID: appA, ServerID: srv1, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SyncApps(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := repo.GetApps(ctx, srv1); len(list) != 0 {
		t.Fatalf("want empty after empty sync, got %v", list)
	}
}

// GetApps breaks created_at ties (second precision) by app id, so both
// databases list apps saved in the same second in the same order.
func TestGetAppsOrdersTiesByID(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{appB, appA, appC} {
		if err := repo.SaveApp(ctx, model.App{ID: id, ServerID: srv1, Name: id, Icon: "i", Color: "#000000", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.GetApps(ctx, srv1)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range list {
		got = append(got, a.ID)
	}
	if strings.Join(got, ",") != strings.Join([]string{appA, appB, appC}, ",") {
		t.Fatalf("order = %v", got)
	}
}

// created_at is stored as naive UTC "2006-01-02 15:04:05" text on SQLite
// (the Bun-era format) and read back as the same instant in UTC.
func TestCreatedAtStorage(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	at := time.Date(2026, 7, 2, 15, 4, 5, 0, time.FixedZone("UTC+3", 3*3600))
	if err := repo.SaveApp(ctx, model.App{ID: appA, ServerID: srv1, Name: "a", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if d.Driver == "sqlite" {
		if raw := dbtest.QueryString(t, d, `SELECT CAST(created_at AS TEXT) FROM apps`); raw != "2026-07-02 12:04:05" {
			t.Fatalf("stored created_at = %q", raw)
		}
	}
	got, _ := repo.GetApp(ctx, appA)
	if !got.CreatedAt.Equal(at) || got.CreatedAt.Location() != time.UTC {
		t.Fatalf("read back %v (%v)", got.CreatedAt, got.CreatedAt.Location())
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/db/repository/app/`
Expected: build failure listing `undefined: Repo` and `undefined: New`.

- [ ] **Step 3: Implement**

Create `internal/infra/db/repository/app/repo.go`:

```go
// Package app implements port.AppRepository: the DB catalog of apps, a
// reconciled cache of what each agent reports (the agent filesystem is the
// source of truth).
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

type (
	appRow       = sqlitegen.App
	upsertParams = sqlitegen.UpsertAppParams
	renameParams = sqlitegen.RenameAppParams
)

type querier interface {
	ListAppsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]appRow, error)
	ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error)
	GetApp(ctx context.Context, db backend.DBTX, appID string) (appRow, error)
	UpsertApp(ctx context.Context, db backend.DBTX, p upsertParams) error
	DeleteApp(ctx context.Context, db backend.DBTX, appID string) error
	RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error
}

// Repo is the apps repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.AppRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("app repository: unknown database driver %q", driver)
}

// GetApps lists a server's apps by created_at, ties (second precision)
// broken by the time-ordered UUIDv7 id so both engines agree.
func (r *Repo) GetApps(ctx context.Context, serverID string) ([]model.App, error) {
	rows, err := r.q.ListAppsByServer(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := make([]model.App, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// GetApp returns one app; model.ErrAppNotFound when there is none.
func (r *Repo) GetApp(ctx context.Context, appID string) (model.App, error) {
	row, err := r.q.GetApp(ctx, r.tx.Querier(ctx), appID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.App{}, model.ErrAppNotFound
		}
		return model.App{}, err
	}
	return hydrate(row), nil
}

// SaveApp upserts the catalog record. An existing row keeps its server_id
// and created_at; name, version, icon, color and template_id are replaced.
func (r *Repo) SaveApp(ctx context.Context, a model.App) error {
	return r.q.UpsertApp(ctx, r.tx.Querier(ctx), upsertFor(a))
}

// DeleteApp removes the app row (no error when it does not exist).
func (r *Repo) DeleteApp(ctx context.Context, appID string) error {
	return r.q.DeleteApp(ctx, r.tx.Querier(ctx), appID)
}

// RenameApp sets the app's name (no error when it does not exist).
func (r *Repo) RenameApp(ctx context.Context, appID, name string) error {
	return r.q.RenameApp(ctx, r.tx.Querier(ctx), renameParams{Name: name, AppID: appID})
}

// SyncApps makes the DB mirror the agent's reported apps for a server:
// upsert the reported ones and delete the server's rows the agent no longer
// has, in one transaction.
func (r *Repo) SyncApps(ctx context.Context, serverID string, apps []model.App) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		keep := make(map[string]bool, len(apps))
		for _, a := range apps {
			a.ServerID = serverID
			if err := r.q.UpsertApp(ctx, r.tx.Querier(ctx), upsertFor(a)); err != nil {
				return err
			}
			keep[a.ID] = true
		}
		ids, err := r.q.ListAppIDsByServer(ctx, r.tx.Querier(ctx), serverID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if keep[id] {
				continue
			}
			if err := r.q.DeleteApp(ctx, r.tx.Querier(ctx), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// upsertFor maps an app to its row: a zero CreatedAt becomes now, an empty
// TemplateID NULL.
func upsertFor(a model.App) upsertParams {
	p := upsertParams{
		AppID:     a.ID,
		ServerID:  a.ServerID,
		Name:      a.Name,
		Version:   a.Version,
		Icon:      a.Icon,
		Color:     a.Color,
		CreatedAt: types.DateTime(a.CreatedAt),
	}
	if a.CreatedAt.IsZero() {
		p.CreatedAt = types.NewDateTime()
	}
	if a.TemplateID != "" {
		id := a.TemplateID
		p.TemplateID = &id
	}
	return p
}

func hydrate(row appRow) model.App {
	a := model.App{
		ID:        row.AppID,
		ServerID:  row.ServerID,
		Version:   row.Version,
		Name:      row.Name,
		Icon:      row.Icon,
		Color:     row.Color,
		CreatedAt: row.CreatedAt.Time(),
	}
	if row.TemplateID != nil {
		a.TemplateID = *row.TemplateID
	}
	return a
}
```

Create `internal/infra/db/repository/app/sqlite.go`:

```go
package app

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListAppsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]appRow, error) {
	return sqlitegen.New(db).ListAppsByServer(ctx, serverID)
}

func (sqliteQuerier) ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return sqlitegen.New(db).ListAppIDsByServer(ctx, serverID)
}

func (sqliteQuerier) GetApp(ctx context.Context, db backend.DBTX, appID string) (appRow, error) {
	return sqlitegen.New(db).GetApp(ctx, appID)
}

func (sqliteQuerier) UpsertApp(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return sqlitegen.New(db).UpsertApp(ctx, p)
}

func (sqliteQuerier) DeleteApp(ctx context.Context, db backend.DBTX, appID string) error {
	return sqlitegen.New(db).DeleteApp(ctx, appID)
}

func (sqliteQuerier) RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error {
	return sqlitegen.New(db).RenameApp(ctx, p)
}
```

Create `internal/infra/db/repository/app/pgsql.go`:

```go
package app

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListAppsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]appRow, error) {
	rows, err := pgsqlgen.New(db).ListAppsByServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]appRow, len(rows))
	for i, row := range rows {
		out[i] = appRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return pgsqlgen.New(db).ListAppIDsByServer(ctx, serverID)
}

func (pgsqlQuerier) GetApp(ctx context.Context, db backend.DBTX, appID string) (appRow, error) {
	row, err := pgsqlgen.New(db).GetApp(ctx, appID)
	return appRow(row), err
}

func (pgsqlQuerier) UpsertApp(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return pgsqlgen.New(db).UpsertApp(ctx, pgsqlgen.UpsertAppParams(p))
}

func (pgsqlQuerier) DeleteApp(ctx context.Context, db backend.DBTX, appID string) error {
	return pgsqlgen.New(db).DeleteApp(ctx, appID)
}

func (pgsqlQuerier) RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error {
	return pgsqlgen.New(db).RenameApp(ctx, pgsqlgen.RenameAppParams(p))
}
```

- [ ] **Step 4: Run on SQLite**

Run: `go test -count=1 ./internal/infra/db/repository/app/ -v 2>&1 | grep -E '^(---|ok)'`
Expected: `--- PASS` for `TestNewRejectsUnknownDriver`, `TestAppCRUDRoundTrip`, `TestSaveAppKeepsServerAndCreatedAt`, `TestSyncAppsMirrorsAgentState`, `TestSyncAppsEmptyReportDeletesAll`, `TestGetAppsOrdersTiesByID`, `TestCreatedAtStorage`; `ok`.

- [ ] **Step 5: Run on Postgres**

Run: `make test-pg TEST_PG_ARGS='-run "TestAppCRUD|TestSaveApp|TestSyncApps|TestGetAppsOrders|TestCreatedAtStorage|TestNewRejectsUnknownDriver" -v' 2>&1 | grep -E 'repository/app\s|^--- FAIL'`
Expected: `ok  winterflow/internal/infra/db/repository/app`, no `--- FAIL` (`TestCreatedAtStorage` checks the UTC location of the read-back value on Postgres; it fails without Task 4's `DateTime.Scan` change).

- [ ] **Step 6: Everything still green**

Run: `gofmt -l internal && go build ./... && go vet ./... && go test ./...`
Expected: no gofmt output; all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db/repository/app
git commit -m "refactor(db): apps repository on sqlc

The upsert keeps server_id and created_at as before; SyncApps deletes the
apps the agent no longer reports by listing the server's ids inside the
transaction instead of a variable NOT IN list.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: `servers` repository (`repository/server`)

**Files:**
- Create: `internal/infra/db/repository/server/repo.go`, `sqlite.go`, `pgsql.go`
- Test: `internal/infra/db/repository/server/repo_test.go` (ports `internal/infra/db/repository/server_test.go`, which stays until Task 10, and adds the registration/claim cases the Bun suite left to the handler and contract tests)

**Interfaces:**
- Consumes: `backend.TxManager`/`DBTX`/driver constants; `sqlitegen`/`pgsqlgen` `ListServersByUser`, `ListCapabilitiesByUser`, `ListFeaturesByUser`, `ListServerUserIDs`, `UserOwnsServer`, `HasAnyServer`, `GetFirstServerID`, `InsertServer`, `TouchServerLastSeen`, `UpsertCapability`, `UpsertFeature`, `GetCapability`, `InsertRegistration`, `DeleteRegistration`, `DeleteAllRegistrations`, `GetRegistrationByCode`, `GetLatestRegistrationCode`, `InsertCertificate` and their types; `dto.ServerRegistrationDTO`, `dto.ClaimServerDTO`; `dbtest.Open`, `Ctx`, `ID`, `SeedOrg`, `SeedServer`, `SeedMember`, `Exec`, `QueryString`, `Log`.
- Produces: `package server`; `func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error)`; `*Repo` implements `port.ServerRepository` and additionally `func (r *Repo) FirstServerID(ctx context.Context) (string, bool, error)` (standalone uses it through the concrete type).

Bun semantics this must keep (from `repository/server.go`):
- `GetServers`: servers of every organization the user belongs to, `ORDER BY created_at ASC, server_id ASC`; each with its capabilities (`[]model.Capability`, now ordered by name — Decision 5) and features (`map[string]bool`); both stay `nil` when the server has none (JSON `omitempty`); `last_seen` NULL → `LastSeenAt nil`.
- `RegisterServer`: delete the server's existing registration, then insert the new one — **not** in a transaction, as before; the PEM is stored base64 (`StdEncoding`); `created_at` = now.
- `GetServerUserIDs`: user ids of the members of the organization owning the server; unknown server → empty, no error. `UserOwnsServer`, `HasAnyServer`: `EXISTS`.
- `FirstServerID`: none → `("", false, nil)`; now ordered by `created_at, server_id` (Decision 6).
- `TouchLastSeen`: `UPDATE servers SET last_seen = now` (no-op for an unknown id).
- `SaveCapabilities`: one transaction, one `now` for every row; capabilities upsert `value, updated_at`; features upsert `is_enabled`.
- `GetCapability`: missing → `("", false, nil)`. `ClearPendingRegistrations`: delete every registration.
- `ClaimServer`: registration looked up by code outside the transaction; none → `model.ErrInvalidRegistrationCode`; `expires_at` before now → `model.ErrRegistrationCodeExpired`; one transaction: insert the server (organization from the DTO, name = registration hostname, `last_seen` NULL), insert its certificate (`is_active` true, the registration's base64 text and certificate expiry), delete the registration. The returned `model.Server` has no capabilities and `CreatedAt` = the unrounded local `time.Now()` that was stored (the contract suite's parity transcript depends on that zone handling, see `contract/main_test.go`).
- `PendingRegistrationCode`: newest registration by `created_at DESC`; none → `("", false, nil)`.

SQL this repository runs (created in Task 3 — do not edit here):

`internal/infra/db/sqlc/query/sqlite/servers.sql`
```sql
-- Servers, their capabilities/features, registrations and certificates.

-- name: ListServersByUser :many
SELECT * FROM servers
WHERE organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY created_at ASC, server_id ASC;

-- name: ListCapabilitiesByUser :many
SELECT c.server_id, c.name, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY c.server_id ASC, c.name ASC;

-- name: ListFeaturesByUser :many
SELECT f.server_id, f.name, f.is_enabled
FROM server_features AS f
JOIN servers AS s ON s.server_id = f.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY f.server_id ASC, f.name ASC;

-- name: ListServerUserIDs :many
SELECT user_id FROM organization_users
WHERE organization_id IN (SELECT organization_id FROM servers WHERE server_id = ?);

-- name: UserOwnsServer :one
SELECT EXISTS (
    SELECT 1 FROM servers
    WHERE server_id = ?
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
);

-- name: HasAnyServer :one
SELECT EXISTS (SELECT 1 FROM servers);

-- name: GetFirstServerID :one
SELECT server_id FROM servers ORDER BY created_at ASC, server_id ASC LIMIT 1;

-- name: InsertServer :exec
INSERT INTO servers (server_id, organization_id, name, created_at)
VALUES (?, ?, ?, ?);

-- name: TouchServerLastSeen :exec
UPDATE servers SET last_seen = ? WHERE server_id = ?;

-- name: UpsertCapability :exec
INSERT INTO server_capabilities (server_id, name, value, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET value = excluded.value, updated_at = excluded.updated_at;

-- name: UpsertFeature :exec
INSERT INTO server_features (server_id, name, is_enabled)
VALUES (?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET is_enabled = excluded.is_enabled;

-- name: GetCapability :one
SELECT value FROM server_capabilities WHERE server_id = ? AND name = ?;

-- name: InsertRegistration :exec
INSERT INTO server_registrations (server_id, certificate_id, hostname, code, expires_at, certificate, certificate_expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteRegistration :exec
DELETE FROM server_registrations WHERE server_id = ?;

-- name: DeleteAllRegistrations :exec
DELETE FROM server_registrations;

-- name: GetRegistrationByCode :one
SELECT * FROM server_registrations WHERE code = ?;

-- name: GetLatestRegistrationCode :one
SELECT code FROM server_registrations ORDER BY created_at DESC LIMIT 1;

-- name: InsertCertificate :exec
INSERT INTO server_certificates (certificate_id, server_id, certificate, is_active, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?);
```

`internal/infra/db/sqlc/query/pgsql/servers.sql`
```sql
-- Servers, their capabilities/features, registrations and certificates.

-- name: ListServersByUser :many
SELECT * FROM servers
WHERE organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $1)
ORDER BY created_at ASC, server_id ASC;

-- name: ListCapabilitiesByUser :many
SELECT c.server_id, c.name, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $1)
ORDER BY c.server_id ASC, c.name ASC;

-- name: ListFeaturesByUser :many
SELECT f.server_id, f.name, f.is_enabled
FROM server_features AS f
JOIN servers AS s ON s.server_id = f.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $1)
ORDER BY f.server_id ASC, f.name ASC;

-- name: ListServerUserIDs :many
SELECT user_id FROM organization_users
WHERE organization_id IN (SELECT organization_id FROM servers WHERE server_id = $1);

-- name: UserOwnsServer :one
SELECT EXISTS (
    SELECT 1 FROM servers
    WHERE server_id = $1
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $2)
);

-- name: HasAnyServer :one
SELECT EXISTS (SELECT 1 FROM servers);

-- name: GetFirstServerID :one
SELECT server_id FROM servers ORDER BY created_at ASC, server_id ASC LIMIT 1;

-- name: InsertServer :exec
INSERT INTO servers (server_id, organization_id, name, created_at)
VALUES ($1, $2, $3, $4);

-- name: TouchServerLastSeen :exec
UPDATE servers SET last_seen = $1 WHERE server_id = $2;

-- name: UpsertCapability :exec
INSERT INTO server_capabilities (server_id, name, value, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (server_id, name) DO UPDATE
SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at;

-- name: UpsertFeature :exec
INSERT INTO server_features (server_id, name, is_enabled)
VALUES ($1, $2, $3)
ON CONFLICT (server_id, name) DO UPDATE
SET is_enabled = EXCLUDED.is_enabled;

-- name: GetCapability :one
SELECT value FROM server_capabilities WHERE server_id = $1 AND name = $2;

-- name: InsertRegistration :exec
INSERT INTO server_registrations (server_id, certificate_id, hostname, code, expires_at, certificate, certificate_expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: DeleteRegistration :exec
DELETE FROM server_registrations WHERE server_id = $1;

-- name: DeleteAllRegistrations :exec
DELETE FROM server_registrations;

-- name: GetRegistrationByCode :one
SELECT * FROM server_registrations WHERE code = $1;

-- name: GetLatestRegistrationCode :one
SELECT code FROM server_registrations ORDER BY created_at DESC LIMIT 1;

-- name: InsertCertificate :exec
INSERT INTO server_certificates (certificate_id, server_id, certificate, is_active, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6);
```

- [ ] **Step 1: Write the tests**

Create `internal/infra/db/repository/server/repo_test.go`:

```go
package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2       = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2       = dbtest.ID(11), dbtest.ID(12)
	alice, bob, eve  = dbtest.ID(21), dbtest.ID(22), dbtest.ID(23)
	pendingSrv, cert = dbtest.ID(31), dbtest.ID(32)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestGetServerUserIDsReturnsOrgMembers(t *testing.T) {
	repo, d := newRepo(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedMember(t, d, alice, org1)
	dbtest.SeedMember(t, d, bob, org1)
	dbtest.SeedMember(t, d, eve, org2)

	got, err := repo.GetServerUserIDs(dbtest.Ctx(t), srv1)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != alice || got[1] != bob {
		t.Fatalf("want [alice bob], got %v", got)
	}
}

func TestGetServerUserIDsUnknownServerIsEmpty(t *testing.T) {
	repo, _ := newRepo(t)
	got, err := repo.GetServerUserIDs(dbtest.Ctx(t), srv1)
	if err != nil || len(got) != 0 {
		t.Fatalf("want empty, got %v, %v", got, err)
	}
}

func TestGetServersIncludesCapabilitiesAndFeatures(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedMember(t, d, alice, org1)
	if err := repo.SaveCapabilities(ctx, srv1,
		map[string]string{"server_ip": "203.0.113.7", "system_cpu_cores": "8"}, map[string]bool{"ingress": true}); err != nil {
		t.Fatal(err)
	}
	// Upsert: a second report replaces values.
	if err := repo.SaveCapabilities(ctx, srv1, map[string]string{"system_cpu_cores": "4"}, map[string]bool{"ingress": false}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveCapabilities(ctx, srv2, map[string]string{"server_ip": "198.51.100.1"}, nil); err != nil {
		t.Fatal(err)
	}

	servers, err := repo.GetServers(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].ID != srv1 || servers[0].OrganizationID != org1 || servers[0].LastSeenAt != nil {
		t.Fatalf("servers = %+v", servers)
	}
	caps := servers[0].Capabilities
	if len(caps) != 2 || caps[0].Name != "server_ip" || caps[0].Value != "203.0.113.7" || caps[1].Name != "system_cpu_cores" || caps[1].Value != "4" {
		t.Fatalf("capabilities = %+v", caps)
	}
	if enabled, ok := servers[0].Features["ingress"]; !ok || enabled {
		t.Fatalf("features = %v", servers[0].Features)
	}
	// The JSON the handler sends must expose name/value in lower case.
	raw, _ := json.Marshal(servers[0])
	if !strings.Contains(string(raw), `"name":"server_ip"`) || !strings.Contains(string(raw), `"value":"203.0.113.7"`) {
		t.Fatalf("serialized server = %s", raw)
	}
}

// A server with no capabilities or features reports nil for both, so the
// JSON omits them (omitempty), as before.
func TestGetServersWithoutCapabilitiesOmitsThem(t *testing.T) {
	repo, d := newRepo(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedMember(t, d, alice, org1)
	servers, err := repo.GetServers(dbtest.Ctx(t), alice)
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers = %+v, %v", servers, err)
	}
	if servers[0].Capabilities != nil || servers[0].Features != nil {
		t.Fatalf("want nil capabilities/features, got %+v", servers[0])
	}
	if servers, _ := repo.GetServers(dbtest.Ctx(t), eve); len(servers) != 0 {
		t.Fatalf("non-member sees %+v", servers)
	}
}

func TestOwnershipAndPresence(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	if has, err := repo.HasAnyServer(ctx); err != nil || has {
		t.Fatalf("HasAnyServer on empty = %v, %v", has, err)
	}
	if _, ok, err := repo.FirstServerID(ctx); ok || err != nil {
		t.Fatalf("FirstServerID on empty = %v, %v", ok, err)
	}
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedMember(t, d, alice, org1)
	dbtest.SeedOrg(t, d, org2)
	dbtest.SeedMember(t, d, eve, org2)

	if has, _ := repo.HasAnyServer(ctx); !has {
		t.Fatal("HasAnyServer = false with a server")
	}
	if id, ok, err := repo.FirstServerID(ctx); id != srv1 || !ok || err != nil {
		t.Fatalf("FirstServerID = %q, %v, %v", id, ok, err)
	}
	for _, c := range []struct {
		user, server string
		want         bool
	}{{alice, srv1, true}, {eve, srv1, false}, {alice, srv2, false}} {
		if got, err := repo.UserOwnsServer(ctx, c.user, c.server); got != c.want || err != nil {
			t.Errorf("UserOwnsServer(%s, %s) = %v, %v", c.user, c.server, got, err)
		}
	}
}

func TestTouchLastSeenAndGetCapability(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedMember(t, d, alice, org1)
	if err := repo.TouchLastSeen(ctx, srv1); err != nil {
		t.Fatal(err)
	}
	if err := repo.TouchLastSeen(ctx, srv2); err != nil {
		t.Fatalf("unknown server: %v", err)
	}
	servers, _ := repo.GetServers(ctx, alice)
	if servers[0].LastSeenAt == nil || time.Since(*servers[0].LastSeenAt) > time.Minute || servers[0].LastSeenAt.Location() != time.UTC {
		t.Fatalf("last_seen = %v", servers[0].LastSeenAt)
	}
	if _, ok, err := repo.GetCapability(ctx, srv1, "public_key"); ok || err != nil {
		t.Fatalf("missing capability = %v, %v", ok, err)
	}
	_ = repo.SaveCapabilities(ctx, srv1, map[string]string{"public_key": "BASE64"}, nil)
	if v, ok, err := repo.GetCapability(ctx, srv1, "public_key"); v != "BASE64" || !ok || err != nil {
		t.Fatalf("GetCapability = %q, %v, %v", v, ok, err)
	}
}

func register(t *testing.T, repo *Repo, code string, expires time.Time) {
	t.Helper()
	if err := repo.RegisterServer(dbtest.Ctx(t), dto.ServerRegistrationDTO{
		ServerID: pendingSrv, CertificateID: cert, Hostname: "box", Code: code,
		ExpiresAt: expires, Certificate: []byte("PEM"), CertificateExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterAndClaimServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedMember(t, d, alice, org1)

	if _, ok, err := repo.PendingRegistrationCode(ctx); ok || err != nil {
		t.Fatalf("pending on empty = %v, %v", ok, err)
	}
	register(t, repo, "111111", time.Now().Add(time.Hour))
	// Registering the same server again replaces its registration.
	register(t, repo, "222222", time.Now().Add(time.Hour))
	if code, ok, err := repo.PendingRegistrationCode(ctx); code != "222222" || !ok || err != nil {
		t.Fatalf("pending = %q, %v, %v", code, ok, err)
	}
	if got := dbtest.QueryString(t, d, `SELECT certificate FROM server_registrations`); got != base64.StdEncoding.EncodeToString([]byte("PEM")) {
		t.Fatalf("stored certificate = %q", got)
	}

	if _, err := repo.ClaimServer(ctx, dto.ClaimServerDTO{Code: "111111", OrganizationID: org1}); !errors.Is(err, model.ErrInvalidRegistrationCode) {
		t.Fatalf("replaced code: err = %v", err)
	}
	srv, err := repo.ClaimServer(ctx, dto.ClaimServerDTO{Code: "222222", OrganizationID: org1})
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID != pendingSrv || srv.OrganizationID != org1 || srv.Name != "box" || srv.CreatedAt.IsZero() || srv.Capabilities != nil {
		t.Fatalf("claimed = %+v", srv)
	}
	if owns, _ := repo.UserOwnsServer(ctx, alice, pendingSrv); !owns {
		t.Fatal("claimed server not owned by the org")
	}
	if got := dbtest.QueryString(t, d, `SELECT certificate FROM server_certificates WHERE certificate_id = ?`, cert); got != base64.StdEncoding.EncodeToString([]byte("PEM")) {
		t.Fatalf("certificate = %q", got)
	}
	if _, ok, _ := repo.PendingRegistrationCode(ctx); ok {
		t.Fatal("registration survived the claim")
	}
}

func TestClaimServerRejectsExpiredCode(t *testing.T) {
	repo, d := newRepo(t)
	dbtest.SeedOrg(t, d, org1)
	register(t, repo, "333333", time.Now().Add(-time.Minute))
	if _, err := repo.ClaimServer(dbtest.Ctx(t), dto.ClaimServerDTO{Code: "333333", OrganizationID: org1}); !errors.Is(err, model.ErrRegistrationCodeExpired) {
		t.Fatalf("err = %v, want ErrRegistrationCodeExpired", err)
	}
}

// A claim that fails half-way (the certificate id is already taken, after
// the server row went in) rolls back: no server, registration kept.
func TestClaimServerIsAtomic(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.Exec(t, d, `INSERT INTO server_certificates (certificate_id, server_id, certificate, expires_at, created_at)
		VALUES (?, ?, 'x', '2030-01-01 00:00:00', '2026-01-01 00:00:00')`, cert, srv1)
	register(t, repo, "444444", time.Now().Add(time.Hour))
	if _, err := repo.ClaimServer(ctx, dto.ClaimServerDTO{Code: "444444", OrganizationID: org1}); err == nil {
		t.Fatal("claim with a taken certificate id succeeded")
	}
	if n := dbtest.QueryString(t, d, `SELECT CAST(count(*) AS TEXT) FROM servers WHERE server_id = ?`, pendingSrv); n != "0" {
		t.Fatal("the server row survived the failed claim")
	}
	if code, ok, _ := repo.PendingRegistrationCode(ctx); code != "444444" || !ok {
		t.Fatal("failed claim consumed the registration")
	}
}

func TestClearPendingRegistrations(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	register(t, repo, "555555", time.Now().Add(time.Hour))
	if err := repo.ClearPendingRegistrations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := repo.PendingRegistrationCode(ctx); ok {
		t.Fatal("registration survived ClearPendingRegistrations")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/db/repository/server/`
Expected: build failure listing `undefined: Repo` and `undefined: New`.

- [ ] **Step 3: Implement**

Create `internal/infra/db/repository/server/repo.go`:

```go
// Package server implements port.ServerRepository: claimed servers, their
// capabilities and features, pending registrations and certificates.
package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

type (
	serverRow          = sqlitegen.Server
	capabilityRow      = sqlitegen.ListCapabilitiesByUserRow
	featureRow         = sqlitegen.ServerFeature
	registrationRow    = sqlitegen.ServerRegistration
	ownsParams         = sqlitegen.UserOwnsServerParams
	insertServerParams = sqlitegen.InsertServerParams
	touchParams        = sqlitegen.TouchServerLastSeenParams
	capabilityParams   = sqlitegen.UpsertCapabilityParams
	featureParams      = sqlitegen.UpsertFeatureParams
	getCapParams       = sqlitegen.GetCapabilityParams
	registrationParams = sqlitegen.InsertRegistrationParams
	certificateParams  = sqlitegen.InsertCertificateParams
)

type querier interface {
	ListServersByUser(ctx context.Context, db backend.DBTX, userID string) ([]serverRow, error)
	ListCapabilitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]capabilityRow, error)
	ListFeaturesByUser(ctx context.Context, db backend.DBTX, userID string) ([]featureRow, error)
	ListServerUserIDs(ctx context.Context, db backend.DBTX, serverID string) ([]string, error)
	UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error)
	HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error)
	GetFirstServerID(ctx context.Context, db backend.DBTX) (string, error)
	InsertServer(ctx context.Context, db backend.DBTX, p insertServerParams) error
	TouchServerLastSeen(ctx context.Context, db backend.DBTX, p touchParams) error
	UpsertCapability(ctx context.Context, db backend.DBTX, p capabilityParams) error
	UpsertFeature(ctx context.Context, db backend.DBTX, p featureParams) error
	GetCapability(ctx context.Context, db backend.DBTX, p getCapParams) (string, error)
	InsertRegistration(ctx context.Context, db backend.DBTX, p registrationParams) error
	DeleteRegistration(ctx context.Context, db backend.DBTX, serverID string) error
	DeleteAllRegistrations(ctx context.Context, db backend.DBTX) error
	GetRegistrationByCode(ctx context.Context, db backend.DBTX, code string) (registrationRow, error)
	GetLatestRegistrationCode(ctx context.Context, db backend.DBTX) (string, error)
	InsertCertificate(ctx context.Context, db backend.DBTX, p certificateParams) error
}

// Repo is the servers repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.ServerRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("server repository: unknown database driver %q", driver)
}

// GetServers returns the servers owned by any organization the user belongs
// to, oldest first, with their capabilities (by name) and features.
func (r *Repo) GetServers(ctx context.Context, userID string) ([]model.Server, error) {
	db := r.tx.Querier(ctx)
	rows, err := r.q.ListServersByUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	caps, err := r.q.ListCapabilitiesByUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	feats, err := r.q.ListFeaturesByUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	capsBy := map[string][]model.Capability{}
	for _, c := range caps {
		capsBy[c.ServerID] = append(capsBy[c.ServerID], model.Capability{Name: c.Name, Value: c.Value})
	}
	featsBy := map[string]map[string]bool{}
	for _, f := range feats {
		if featsBy[f.ServerID] == nil {
			featsBy[f.ServerID] = map[string]bool{}
		}
		featsBy[f.ServerID][f.Name] = f.IsEnabled
	}
	out := make([]model.Server, 0, len(rows))
	for _, row := range rows {
		s := hydrate(row)
		// Both stay nil when the server has none (omitted from JSON).
		s.Capabilities = capsBy[row.ServerID]
		s.Features = featsBy[row.ServerID]
		out = append(out, s)
	}
	return out, nil
}

func hydrate(row serverRow) model.Server {
	s := model.Server{
		ID:             row.ServerID,
		OrganizationID: row.OrganizationID,
		Name:           row.Name,
		CreatedAt:      row.CreatedAt.Time(),
	}
	if row.LastSeen != nil {
		t := row.LastSeen.Time()
		s.LastSeenAt = &t
	}
	return s
}

// RegisterServer replaces the server's pending registration (the PEM is
// stored base64-encoded).
func (r *Repo) RegisterServer(ctx context.Context, d dto.ServerRegistrationDTO) error {
	db := r.tx.Querier(ctx)
	if err := r.q.DeleteRegistration(ctx, db, d.ServerID); err != nil {
		return err
	}
	return r.q.InsertRegistration(ctx, db, registrationParams{
		ServerID:             d.ServerID,
		CertificateID:        d.CertificateID,
		Hostname:             d.Hostname,
		Code:                 d.Code,
		ExpiresAt:            types.DateTime(d.ExpiresAt),
		Certificate:          base64.StdEncoding.EncodeToString(d.Certificate),
		CertificateExpiresAt: types.DateTime(d.CertificateExpiresAt),
		CreatedAt:            types.NewDateTime(),
	})
}

// GetServerUserIDs returns the user ids of every member of the organization
// that owns the server. Unknown server: empty, no error.
func (r *Repo) GetServerUserIDs(ctx context.Context, serverID string) ([]string, error) {
	return r.q.ListServerUserIDs(ctx, r.tx.Querier(ctx), serverID)
}

// UserOwnsServer reports whether the server belongs to one of the user's
// organizations. Unknown server or non-member: false, no error.
func (r *Repo) UserOwnsServer(ctx context.Context, userID, serverID string) (bool, error) {
	return r.q.UserOwnsServer(ctx, r.tx.Querier(ctx), ownsParams{ServerID: serverID, UserID: userID})
}

// HasAnyServer reports whether any server has been claimed.
func (r *Repo) HasAnyServer(ctx context.Context) (bool, error) {
	return r.q.HasAnyServer(ctx, r.tx.Querier(ctx))
}

// FirstServerID returns the id of the (single, in standalone) claimed
// server, if any.
func (r *Repo) FirstServerID(ctx context.Context) (string, bool, error) {
	id, err := r.q.GetFirstServerID(ctx, r.tx.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return id, true, nil
}

// TouchLastSeen records that a server reported in just now. No-op for an
// unclaimed server.
func (r *Repo) TouchLastSeen(ctx context.Context, serverID string) error {
	now := types.NewDateTime()
	return r.q.TouchServerLastSeen(ctx, r.tx.Querier(ctx), touchParams{LastSeen: &now, ServerID: serverID})
}

// SaveCapabilities upserts a server's capabilities and features in one
// transaction.
func (r *Repo) SaveCapabilities(ctx context.Context, serverID string, capabilities map[string]string, features map[string]bool) error {
	now := types.NewDateTime()
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		for name, value := range capabilities {
			if err := r.q.UpsertCapability(ctx, db, capabilityParams{ServerID: serverID, Name: name, Value: value, UpdatedAt: now}); err != nil {
				return err
			}
		}
		for name, enabled := range features {
			if err := r.q.UpsertFeature(ctx, db, featureParams{ServerID: serverID, Name: name, IsEnabled: enabled}); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetCapability returns one capability value; ok is false when the server
// has none recorded under name.
func (r *Repo) GetCapability(ctx context.Context, serverID, name string) (string, bool, error) {
	v, err := r.q.GetCapability(ctx, r.tx.Querier(ctx), getCapParams{ServerID: serverID, Name: name})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

// ClearPendingRegistrations removes all unclaimed registrations.
func (r *Repo) ClearPendingRegistrations(ctx context.Context) error {
	return r.q.DeleteAllRegistrations(ctx, r.tx.Querier(ctx))
}

// ClaimServer consumes a pending registration by code and materializes the
// server and its active certificate, owned by the organization, in one
// transaction. The returned server has no capabilities yet, and its
// CreatedAt is the unrounded local time.Now() it was stored with.
func (r *Repo) ClaimServer(ctx context.Context, d dto.ClaimServerDTO) (model.Server, error) {
	reg, err := r.q.GetRegistrationByCode(ctx, r.tx.Querier(ctx), d.Code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Server{}, model.ErrInvalidRegistrationCode
		}
		return model.Server{}, err
	}
	if reg.ExpiresAt.Time().Before(time.Now()) {
		return model.Server{}, model.ErrRegistrationCodeExpired
	}
	created := types.NewDateTime()
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if err := r.q.InsertServer(ctx, db, insertServerParams{
			ServerID:       reg.ServerID,
			OrganizationID: d.OrganizationID,
			Name:           reg.Hostname,
			CreatedAt:      created,
		}); err != nil {
			return err
		}
		if err := r.q.InsertCertificate(ctx, db, certificateParams{
			CertificateID: reg.CertificateID,
			ServerID:      reg.ServerID,
			Certificate:   reg.Certificate,
			IsActive:      true,
			ExpiresAt:     reg.CertificateExpiresAt,
			CreatedAt:     types.NewDateTime(),
		}); err != nil {
			return err
		}
		return r.q.DeleteRegistration(ctx, db, reg.ServerID)
	})
	if err != nil {
		return model.Server{}, err
	}
	return model.Server{
		ID:             reg.ServerID,
		OrganizationID: d.OrganizationID,
		Name:           reg.Hostname,
		CreatedAt:      created.Time(),
	}, nil
}

// PendingRegistrationCode returns the code of the newest unclaimed
// registration; ok is false when there is none.
func (r *Repo) PendingRegistrationCode(ctx context.Context) (string, bool, error) {
	code, err := r.q.GetLatestRegistrationCode(ctx, r.tx.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return code, true, nil
}
```

Create `internal/infra/db/repository/server/sqlite.go`:

```go
package server

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListServersByUser(ctx context.Context, db backend.DBTX, userID string) ([]serverRow, error) {
	return sqlitegen.New(db).ListServersByUser(ctx, userID)
}

func (sqliteQuerier) ListCapabilitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]capabilityRow, error) {
	return sqlitegen.New(db).ListCapabilitiesByUser(ctx, userID)
}

func (sqliteQuerier) ListFeaturesByUser(ctx context.Context, db backend.DBTX, userID string) ([]featureRow, error) {
	return sqlitegen.New(db).ListFeaturesByUser(ctx, userID)
}

func (sqliteQuerier) ListServerUserIDs(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return sqlitegen.New(db).ListServerUserIDs(ctx, serverID)
}

func (sqliteQuerier) UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error) {
	return sqlitegen.New(db).UserOwnsServer(ctx, p)
}

func (sqliteQuerier) HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error) {
	return sqlitegen.New(db).HasAnyServer(ctx)
}

func (sqliteQuerier) GetFirstServerID(ctx context.Context, db backend.DBTX) (string, error) {
	return sqlitegen.New(db).GetFirstServerID(ctx)
}

func (sqliteQuerier) InsertServer(ctx context.Context, db backend.DBTX, p insertServerParams) error {
	return sqlitegen.New(db).InsertServer(ctx, p)
}

func (sqliteQuerier) TouchServerLastSeen(ctx context.Context, db backend.DBTX, p touchParams) error {
	return sqlitegen.New(db).TouchServerLastSeen(ctx, p)
}

func (sqliteQuerier) UpsertCapability(ctx context.Context, db backend.DBTX, p capabilityParams) error {
	return sqlitegen.New(db).UpsertCapability(ctx, p)
}

func (sqliteQuerier) UpsertFeature(ctx context.Context, db backend.DBTX, p featureParams) error {
	return sqlitegen.New(db).UpsertFeature(ctx, p)
}

func (sqliteQuerier) GetCapability(ctx context.Context, db backend.DBTX, p getCapParams) (string, error) {
	return sqlitegen.New(db).GetCapability(ctx, p)
}

func (sqliteQuerier) InsertRegistration(ctx context.Context, db backend.DBTX, p registrationParams) error {
	return sqlitegen.New(db).InsertRegistration(ctx, p)
}

func (sqliteQuerier) DeleteRegistration(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteRegistration(ctx, serverID)
}

func (sqliteQuerier) DeleteAllRegistrations(ctx context.Context, db backend.DBTX) error {
	return sqlitegen.New(db).DeleteAllRegistrations(ctx)
}

func (sqliteQuerier) GetRegistrationByCode(ctx context.Context, db backend.DBTX, code string) (registrationRow, error) {
	return sqlitegen.New(db).GetRegistrationByCode(ctx, code)
}

func (sqliteQuerier) GetLatestRegistrationCode(ctx context.Context, db backend.DBTX) (string, error) {
	return sqlitegen.New(db).GetLatestRegistrationCode(ctx)
}

func (sqliteQuerier) InsertCertificate(ctx context.Context, db backend.DBTX, p certificateParams) error {
	return sqlitegen.New(db).InsertCertificate(ctx, p)
}
```

Create `internal/infra/db/repository/server/pgsql.go`:

```go
package server

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListServersByUser(ctx context.Context, db backend.DBTX, userID string) ([]serverRow, error) {
	rows, err := pgsqlgen.New(db).ListServersByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]serverRow, len(rows))
	for i, row := range rows {
		out[i] = serverRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListCapabilitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]capabilityRow, error) {
	rows, err := pgsqlgen.New(db).ListCapabilitiesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]capabilityRow, len(rows))
	for i, row := range rows {
		out[i] = capabilityRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListFeaturesByUser(ctx context.Context, db backend.DBTX, userID string) ([]featureRow, error) {
	rows, err := pgsqlgen.New(db).ListFeaturesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]featureRow, len(rows))
	for i, row := range rows {
		out[i] = featureRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListServerUserIDs(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return pgsqlgen.New(db).ListServerUserIDs(ctx, serverID)
}

func (pgsqlQuerier) UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error) {
	return pgsqlgen.New(db).UserOwnsServer(ctx, pgsqlgen.UserOwnsServerParams(p))
}

func (pgsqlQuerier) HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error) {
	return pgsqlgen.New(db).HasAnyServer(ctx)
}

func (pgsqlQuerier) GetFirstServerID(ctx context.Context, db backend.DBTX) (string, error) {
	return pgsqlgen.New(db).GetFirstServerID(ctx)
}

func (pgsqlQuerier) InsertServer(ctx context.Context, db backend.DBTX, p insertServerParams) error {
	return pgsqlgen.New(db).InsertServer(ctx, pgsqlgen.InsertServerParams(p))
}

func (pgsqlQuerier) TouchServerLastSeen(ctx context.Context, db backend.DBTX, p touchParams) error {
	return pgsqlgen.New(db).TouchServerLastSeen(ctx, pgsqlgen.TouchServerLastSeenParams(p))
}

func (pgsqlQuerier) UpsertCapability(ctx context.Context, db backend.DBTX, p capabilityParams) error {
	return pgsqlgen.New(db).UpsertCapability(ctx, pgsqlgen.UpsertCapabilityParams(p))
}

func (pgsqlQuerier) UpsertFeature(ctx context.Context, db backend.DBTX, p featureParams) error {
	return pgsqlgen.New(db).UpsertFeature(ctx, pgsqlgen.UpsertFeatureParams(p))
}

func (pgsqlQuerier) GetCapability(ctx context.Context, db backend.DBTX, p getCapParams) (string, error) {
	return pgsqlgen.New(db).GetCapability(ctx, pgsqlgen.GetCapabilityParams(p))
}

func (pgsqlQuerier) InsertRegistration(ctx context.Context, db backend.DBTX, p registrationParams) error {
	return pgsqlgen.New(db).InsertRegistration(ctx, pgsqlgen.InsertRegistrationParams(p))
}

func (pgsqlQuerier) DeleteRegistration(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteRegistration(ctx, serverID)
}

func (pgsqlQuerier) DeleteAllRegistrations(ctx context.Context, db backend.DBTX) error {
	return pgsqlgen.New(db).DeleteAllRegistrations(ctx)
}

func (pgsqlQuerier) GetRegistrationByCode(ctx context.Context, db backend.DBTX, code string) (registrationRow, error) {
	row, err := pgsqlgen.New(db).GetRegistrationByCode(ctx, code)
	return registrationRow(row), err
}

func (pgsqlQuerier) GetLatestRegistrationCode(ctx context.Context, db backend.DBTX) (string, error) {
	return pgsqlgen.New(db).GetLatestRegistrationCode(ctx)
}

func (pgsqlQuerier) InsertCertificate(ctx context.Context, db backend.DBTX, p certificateParams) error {
	return pgsqlgen.New(db).InsertCertificate(ctx, pgsqlgen.InsertCertificateParams(p))
}
```

- [ ] **Step 4: Run on SQLite**

Run: `go test -count=1 ./internal/infra/db/repository/server/ -v 2>&1 | grep -E '^(---|ok)'`
Expected: `--- PASS` for `TestNewRejectsUnknownDriver`, `TestGetServerUserIDsReturnsOrgMembers`, `TestGetServerUserIDsUnknownServerIsEmpty`, `TestGetServersIncludesCapabilitiesAndFeatures`, `TestGetServersWithoutCapabilitiesOmitsThem`, `TestOwnershipAndPresence`, `TestTouchLastSeenAndGetCapability`, `TestRegisterAndClaimServer`, `TestClaimServerRejectsExpiredCode`, `TestClaimServerIsAtomic`, `TestClearPendingRegistrations`; `ok`.

- [ ] **Step 5: Run on Postgres**

Run: `make test-pg TEST_PG_ARGS='-run "TestGetServer|TestOwnership|TestTouchLastSeen|TestRegisterAndClaim|TestClaimServer|TestClearPending|TestNewRejectsUnknownDriver" -v' 2>&1 | grep -E 'repository/server\s|^--- FAIL'`
Expected: `ok  winterflow/internal/infra/db/repository/server`, no `--- FAIL`.

- [ ] **Step 6: Everything still green**

Run: `gofmt -l internal && go build ./... && go vet ./... && go test ./...`
Expected: no gofmt output; all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db/repository/server
git commit -m "refactor(db): servers repository on sqlc

GetServers loads servers, capabilities and features with three fixed
queries scoped by the user's organizations; claim, registration and
ownership semantics are unchanged.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: `users` repository (`repository/user`)

**Files:**
- Create: `internal/infra/db/repository/user/repo.go`, `sqlite.go`, `pgsql.go`
- Test: `internal/infra/db/repository/user/repo_test.go` (ports `internal/infra/db/repository/user_test.go`, which stays until Task 10)

**Interfaces:**
- Consumes: `backend.TxManager`/`DBTX`/driver constants; the users/tokens/organizations queries of `sqlitegen`/`pgsqlgen` (`GetUser`, `CountUsers`, `InsertUser`, `DeleteUser`, `GetConnectedAccountUserID`, `InsertConnectedAccount`, `GetFirstConnectedAccountProvider`, `InsertCredentials`, `GetCredentialsByEmail`, `GetCredentialsByUser`, `EmailTaken`, `UpdatePassword`, `InsertToken`, `GetTokenByHash`, `TouchTokenLastUsed`, `ListTokensByUser`, `DeleteToken`, `InsertOrganization`, `GetOrganization`, `UpdateOrganization`, `InsertOrganizationUser`, `GetPrimaryOrganizationID`, `LockOrganizationMember`, `IsOrganizationMember`, `ListOrganizationMembers`) and their types; `pat.Generate`, `pat.Hash`, `util.GenerateID`, `util.RefString`, `util.DerefString`, `bcrypt`; `dbtest.Open`, `Ctx`, `ID`, `QueryString`, `Log`.
- Produces: `package user`; `func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error)`; `*Repo` implements `port.UserRepository` (and therefore `port.UserService`).

Bun semantics this must keep (from `repository/user.go`):
- Lookups: `GetUser`, `GetByConnectedAccount` (account → user id → user), `PrimaryOrganizationID` (oldest membership, `ORDER BY created_at ASC LIMIT 1`), `GetOrganization` and `GetCredentials` map `sql.ErrNoRows` to `model.ErrorUserNotFound`; `VerifyLocalCredentials` maps a missing email **and** a bcrypt mismatch to `model.ErrInvalidCredentials`.
- Emails go through `normalizeEmail` (lower-case, trimmed) on every write and lookup; passwords are bcrypt `DefaultCost`.
- `CreateUser`: one transaction — organization named `strings.ToLower(name) + "'s org"`, user (avatar NULL when empty), membership role `owner`, connected account; returns the user with the unrounded creation time. `FindOrCreateUser` creates only on `ErrorUserNotFound` and logs a failed create.
- `BootstrapLocalAdmin`: empty/blank name → the email's local part (or the whole email); one transaction that **re-counts users inside it** and returns `model.ErrNotBootstrap` when any exist; org `name + "'s org"`, role `owner`, connected account `local`/email, credentials (must-change false).
- `CreateMemberUser`: `EmailTaken` check inside the transaction → `model.ErrEmailTaken`; user, membership role `admin` (`model.RoleAdmin`), connected account, credentials with must-change **true**; no organization.
- `RegisterLocalUser`: like bootstrap but checks `EmailTaken` instead of the user count, and the org is `name + "'s org"` with the name as given.
- `SetPassword`, `UpdateOrganization`: zero affected rows → `model.ErrorUserNotFound`. `DeleteToken`: scoped to `(token_id, user_id)`, zero rows → `model.ErrTokenNotFound`.
- Tokens: only `pat.Hash(plaintext)` is stored; `FindByToken` rejects unknown and expired tokens with `model.ErrInvalidToken`, stamps `last_used_at` only when NULL or older than one minute, and only logs a failed stamp; `ListTokens` is `ORDER BY created_at DESC, token_id DESC`; `CreateToken` returns the record it inserted (expiry as given).
- `ListMembers`: members ordered by `created_at ASC, user_id ASC`; email + provider `local` when the member has credentials, otherwise the provider of one connected account (now deterministic, Decision 6), otherwise empty.
- `RemoveMember`: one transaction — the caller's membership row is read first (`FOR UPDATE` on Postgres, a plain `SELECT` on SQLite), missing → `model.ErrorUserNotFound`; target not a member of `orgID` → `model.ErrorUserNotFound`; else delete the user row (memberships, credentials, accounts, PATs cascade).

SQL this repository runs (created in Task 3 — do not edit here):

`internal/infra/db/sqlc/query/sqlite/users.sql`
```sql
-- Users, their connected accounts and local credentials.

-- name: GetUser :one
SELECT * FROM users WHERE user_id = ?;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (user_id, name, avatar, created_at, last_seen)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteUser :exec
DELETE FROM users WHERE user_id = ?;

-- name: GetConnectedAccountUserID :one
SELECT user_id FROM user_connected_accounts WHERE provider = ? AND external_id = ?;

-- name: InsertConnectedAccount :exec
INSERT INTO user_connected_accounts (provider, external_id, user_id)
VALUES (?, ?, ?);

-- name: GetFirstConnectedAccountProvider :one
SELECT provider FROM user_connected_accounts
WHERE user_id = ?
ORDER BY provider ASC, external_id ASC
LIMIT 1;

-- name: InsertCredentials :exec
INSERT INTO user_credentials (user_id, email, password_hash, must_change_password, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetCredentialsByEmail :one
SELECT * FROM user_credentials WHERE email = ?;

-- name: GetCredentialsByUser :one
SELECT * FROM user_credentials WHERE user_id = ?;

-- name: EmailTaken :one
SELECT EXISTS (SELECT 1 FROM user_credentials WHERE email = ?);

-- name: UpdatePassword :execrows
UPDATE user_credentials
SET password_hash = ?, must_change_password = ?, updated_at = ?
WHERE user_id = ?;
```

`internal/infra/db/sqlc/query/sqlite/tokens.sql`
```sql
-- Personal access tokens (hashed at rest).

-- name: InsertToken :exec
INSERT INTO user_tokens (token_id, user_id, name, token_prefix, token_hash, token_type, expires_at, last_used_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTokenByHash :one
SELECT * FROM user_tokens WHERE token_hash = ?;

-- name: TouchTokenLastUsed :exec
UPDATE user_tokens SET last_used_at = ? WHERE token_id = ?;

-- name: ListTokensByUser :many
SELECT * FROM user_tokens
WHERE user_id = ?
ORDER BY created_at DESC, token_id DESC;

-- name: DeleteToken :execrows
DELETE FROM user_tokens WHERE token_id = ? AND user_id = ?;
```

`internal/infra/db/sqlc/query/sqlite/organizations.sql`
```sql
-- Organizations and memberships.

-- name: InsertOrganization :exec
INSERT INTO organizations (organization_id, name, created_at)
VALUES (?, ?, ?);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE organization_id = ?;

-- name: UpdateOrganization :execrows
UPDATE organizations SET name = ?, icon = ?, color = ?
WHERE organization_id = ?;

-- name: InsertOrganizationUser :exec
INSERT INTO organization_users (organization_id, user_id, role, created_at)
VALUES (?, ?, ?, ?);

-- name: GetPrimaryOrganizationID :one
SELECT organization_id FROM organization_users
WHERE user_id = ?
ORDER BY created_at ASC
LIMIT 1;

-- LockOrganizationMember re-reads the caller's membership inside
-- RemoveMember's transaction. SQLite has no row locks (its writers are
-- serialized), so this is a plain SELECT; Postgres adds FOR UPDATE.
-- name: LockOrganizationMember :one
SELECT user_id FROM organization_users
WHERE organization_id = ? AND user_id = ?;

-- name: IsOrganizationMember :one
SELECT EXISTS (
    SELECT 1 FROM organization_users WHERE organization_id = ? AND user_id = ?
);

-- name: ListOrganizationMembers :many
SELECT u.user_id, u.name, u.avatar, u.created_at, u.last_seen
FROM organization_users AS ou
JOIN users AS u ON u.user_id = ou.user_id
WHERE ou.organization_id = ?
ORDER BY ou.created_at ASC, ou.user_id ASC;
```

`internal/infra/db/sqlc/query/pgsql/users.sql`
```sql
-- Users, their connected accounts and local credentials.

-- name: GetUser :one
SELECT * FROM users WHERE user_id = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (user_id, name, avatar, created_at, last_seen)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteUser :exec
DELETE FROM users WHERE user_id = $1;

-- name: GetConnectedAccountUserID :one
SELECT user_id FROM user_connected_accounts WHERE provider = $1 AND external_id = $2;

-- name: InsertConnectedAccount :exec
INSERT INTO user_connected_accounts (provider, external_id, user_id)
VALUES ($1, $2, $3);

-- name: GetFirstConnectedAccountProvider :one
SELECT provider FROM user_connected_accounts
WHERE user_id = $1
ORDER BY provider ASC, external_id ASC
LIMIT 1;

-- name: InsertCredentials :exec
INSERT INTO user_credentials (user_id, email, password_hash, must_change_password, updated_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetCredentialsByEmail :one
SELECT * FROM user_credentials WHERE email = $1;

-- name: GetCredentialsByUser :one
SELECT * FROM user_credentials WHERE user_id = $1;

-- name: EmailTaken :one
SELECT EXISTS (SELECT 1 FROM user_credentials WHERE email = $1);

-- name: UpdatePassword :execrows
UPDATE user_credentials
SET password_hash = $1, must_change_password = $2, updated_at = $3
WHERE user_id = $4;
```

`internal/infra/db/sqlc/query/pgsql/tokens.sql`
```sql
-- Personal access tokens (hashed at rest).

-- name: InsertToken :exec
INSERT INTO user_tokens (token_id, user_id, name, token_prefix, token_hash, token_type, expires_at, last_used_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetTokenByHash :one
SELECT * FROM user_tokens WHERE token_hash = $1;

-- name: TouchTokenLastUsed :exec
UPDATE user_tokens SET last_used_at = $1 WHERE token_id = $2;

-- name: ListTokensByUser :many
SELECT * FROM user_tokens
WHERE user_id = $1
ORDER BY created_at DESC, token_id DESC;

-- name: DeleteToken :execrows
DELETE FROM user_tokens WHERE token_id = $1 AND user_id = $2;
```

`internal/infra/db/sqlc/query/pgsql/organizations.sql`
```sql
-- Organizations and memberships.

-- name: InsertOrganization :exec
INSERT INTO organizations (organization_id, name, created_at)
VALUES ($1, $2, $3);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE organization_id = $1;

-- name: UpdateOrganization :execrows
UPDATE organizations SET name = $1, icon = $2, color = $3
WHERE organization_id = $4;

-- name: InsertOrganizationUser :exec
INSERT INTO organization_users (organization_id, user_id, role, created_at)
VALUES ($1, $2, $3, $4);

-- name: GetPrimaryOrganizationID :one
SELECT organization_id FROM organization_users
WHERE user_id = $1
ORDER BY created_at ASC
LIMIT 1;

-- LockOrganizationMember re-reads and row-locks the caller's membership
-- inside RemoveMember's transaction, so two members removing each other
-- concurrently cannot empty the org.
-- name: LockOrganizationMember :one
SELECT user_id FROM organization_users
WHERE organization_id = $1 AND user_id = $2
FOR UPDATE;

-- name: IsOrganizationMember :one
SELECT EXISTS (
    SELECT 1 FROM organization_users WHERE organization_id = $1 AND user_id = $2
);

-- name: ListOrganizationMembers :many
SELECT u.user_id, u.name, u.avatar, u.created_at, u.last_seen
FROM organization_users AS ou
JOIN users AS u ON u.user_id = ou.user_id
WHERE ou.organization_id = $1
ORDER BY ou.created_at ASC, ou.user_id ASC;
```

- [ ] **Step 1: Write the tests**

Create `internal/infra/db/repository/user/repo_test.go` (the Bun-era cases verbatim except: UUID ids for unknown lookups, the stored role and token hash read with `dbtest.QueryString`, `dbtest.Ctx`; plus UTC location, unknown-user updates, provider listing, concurrent mutual removal and atomicity):

```go
package user

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/pkg/pat"
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

// storedRole reads organization_users.role straight from the table: the app
// no longer reads it, but it is still written (see model.OrganizationRole).
func storedRole(t *testing.T, d *db.DB, userID string) string {
	t.Helper()
	return dbtest.QueryString(t, d, `SELECT role FROM organization_users WHERE user_id = ?`, userID)
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestCreateUserCreatesOrgMembershipAndAccount(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)

	u, err := repo.CreateUser(ctx, dto.UserDTO{
		Name:      "Alice",
		AvatarURL: "https://example.com/a.png",
		Provider:  "google",
		AccountID: "goog-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.Name != "Alice" {
		t.Fatalf("user = %+v", u)
	}

	// Membership: the auto-created org is discoverable.
	orgID, err := repo.PrimaryOrganizationID(ctx, u.ID)
	if err != nil || orgID == "" {
		t.Fatalf("PrimaryOrganizationID = %q, %v", orgID, err)
	}

	// Connected account resolves back to the same user.
	got, err := repo.GetByConnectedAccount(ctx, "google", "goog-123")
	if err != nil || got.ID != u.ID {
		t.Fatalf("GetByConnectedAccount = %+v, %v", got, err)
	}

	// And a plain lookup works.
	byID, err := repo.GetUser(ctx, u.ID)
	if err != nil || byID.Name != "Alice" {
		t.Fatalf("GetUser = %+v, %v", byID, err)
	}
}

func TestUserLookupsReturnNotFound(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)

	if _, err := repo.GetByConnectedAccount(ctx, "google", "nope"); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("GetByConnectedAccount err = %v", err)
	}
	if _, err := repo.GetUser(ctx, dbtest.ID(404)); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("GetUser err = %v", err)
	}
	if _, err := repo.PrimaryOrganizationID(ctx, dbtest.ID(404)); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("PrimaryOrganizationID err = %v", err)
	}
}

func TestCreateTokenAndFindByToken(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	u, err := repo.CreateUser(ctx, dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g1"})
	if err != nil {
		t.Fatal(err)
	}

	rec, plaintext, err := repo.CreateToken(ctx, u.ID, "ci deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "ci deploy" || rec.Prefix == "" || rec.ID == "" {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.HasPrefix(plaintext, "wfp_") || !strings.HasPrefix(plaintext, rec.Prefix) {
		t.Fatalf("plaintext %q / prefix %q mismatch", plaintext, rec.Prefix)
	}

	got, err := repo.FindByToken(ctx, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID {
		t.Errorf("resolved user %q, want %q", got.ID, u.ID)
	}

	// The DB must not contain the plaintext anywhere.
	stored := dbtest.QueryString(t, d, `SELECT token_hash FROM user_tokens WHERE token_id = ?`, rec.ID)
	if stored == plaintext || stored != pat.Hash(plaintext) {
		t.Error("token stored in plaintext or not as its hash")
	}
}

func TestFindByTokenRejectsUnknownAndExpired(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	u, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "A", Provider: "google", AccountID: "g2"})

	if _, err := repo.FindByToken(ctx, "wfp_nope"); !errors.Is(err, model.ErrInvalidToken) {
		t.Errorf("unknown token: err = %v, want ErrInvalidToken", err)
	}

	past := time.Now().Add(-time.Hour)
	_, expired, err := repo.CreateToken(ctx, u.ID, "old", &past)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindByToken(ctx, expired); !errors.Is(err, model.ErrInvalidToken) {
		t.Errorf("expired token: err = %v, want ErrInvalidToken", err)
	}
}

func TestFindByTokenStampsLastUsed(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	u, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "A", Provider: "google", AccountID: "g3"})
	rec, plaintext, _ := repo.CreateToken(ctx, u.ID, "t", nil)

	if _, err := repo.FindByToken(ctx, plaintext); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListTokens(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("list = %+v", list)
	}
	if list[0].LastUsedAt == nil {
		t.Error("last_used_at not stamped on use")
	}
}

func TestDeleteTokenScopedToUser(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	owner, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "O", Provider: "google", AccountID: "g4"})
	other, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "X", Provider: "google", AccountID: "g5"})
	rec, plaintext, _ := repo.CreateToken(ctx, owner.ID, "mine", nil)

	if err := repo.DeleteToken(ctx, other.ID, rec.ID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Errorf("cross-user delete: err = %v, want ErrTokenNotFound", err)
	}
	if err := repo.DeleteToken(ctx, owner.ID, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindByToken(ctx, plaintext); !errors.Is(err, model.ErrInvalidToken) {
		t.Error("token still resolves after delete")
	}
	if err := repo.DeleteToken(ctx, owner.ID, rec.ID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Errorf("double delete: err = %v, want ErrTokenNotFound", err)
	}
}

func TestFindOrCreateUserIsIdempotent(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	in := dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g-1"}

	first, err := repo.FindOrCreateUser(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.FindOrCreateUser(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("second call created a new user: %q vs %q", first.ID, second.ID)
	}
}

func TestBootstrapLocalAdminCreatesOwnerOnce(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)

	if n, err := repo.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v", n, err)
	}

	admin, err := repo.BootstrapLocalAdmin(ctx, "Ada Admin", "  Admin@Example.COM ", "SuperSecret1")
	if err != nil {
		t.Fatal(err)
	}
	if admin.Name != "Ada Admin" {
		t.Errorf("name = %q, want submitted 'Ada Admin'", admin.Name)
	}
	if role := storedRole(t, d, admin.ID); role != "owner" {
		t.Errorf("stored role = %q, want owner", role)
	}

	// Email was normalized on write; verify with normalized form.
	got, err := repo.VerifyLocalCredentials(ctx, "admin@example.com", "SuperSecret1")
	if err != nil || got.ID != admin.ID {
		t.Fatalf("verify after bootstrap: %+v, %v", got, err)
	}

	// Second bootstrap must refuse: users exist now.
	if _, err := repo.BootstrapLocalAdmin(ctx, "", "evil@example.com", "x"); !errors.Is(err, model.ErrNotBootstrap) {
		t.Errorf("second bootstrap err = %v, want ErrNotBootstrap", err)
	}
}

func TestVerifyLocalCredentials(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	admin, err := repo.BootstrapLocalAdmin(ctx, "", "a@b.io", "correct-horse-1")
	if err != nil {
		t.Fatal(err)
	}

	if u, err := repo.VerifyLocalCredentials(ctx, "A@B.io", "correct-horse-1"); err != nil || u.ID != admin.ID {
		t.Errorf("valid creds: %+v, %v", u, err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "a@b.io", "wrong"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Errorf("wrong password err = %v", err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "nobody@b.io", "x"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Errorf("unknown email err = %v", err)
	}
}

func TestCreateMemberUserJoinsOrgWithoutNewOrg(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	admin, _ := repo.BootstrapLocalAdmin(ctx, "", "boss@x.io", "bootpass-123")
	orgID, err := repo.PrimaryOrganizationID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	member, err := repo.CreateMemberUser(ctx, orgID, "Bob", "Bob@X.io", "temp-pass-16char")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.PrimaryOrganizationID(ctx, member.ID); err != nil || got != orgID {
		t.Errorf("member org = %q, %v; want admin's org %q", got, err, orgID)
	}
	// Admin, not the column default 'member': an older binary run against
	// this database must not demote accounts created without roles.
	if role := storedRole(t, d, member.ID); role != "admin" {
		t.Errorf("stored role = %q, want admin", role)
	}
	if u, err := repo.VerifyLocalCredentials(ctx, "bob@x.io", "temp-pass-16char"); err != nil || u.ID != member.ID {
		t.Errorf("temp password login: %v", err)
	}
	if creds, err := repo.GetCredentials(ctx, member.ID); err != nil || !creds.MustChangePassword || creds.Email != "bob@x.io" {
		t.Errorf("creds = %+v, %v; want must_change=true", creds, err)
	}

	// Duplicate email refused.
	if _, err := repo.CreateMemberUser(ctx, orgID, "Bob2", "bob@x.io", "zz"); !errors.Is(err, model.ErrEmailTaken) {
		t.Errorf("dup email err = %v, want ErrEmailTaken", err)
	}

	// No second org was created for the member.
	members, err := repo.ListMembers(ctx, orgID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %d, %v", len(members), err)
	}
}

func TestSetPasswordClearsMustChange(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	admin, _ := repo.BootstrapLocalAdmin(ctx, "", "boss@y.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, admin.ID)
	m, _ := repo.CreateMemberUser(ctx, orgID, "M", "m@y.io", "old-temp-pass1")

	if err := repo.SetPassword(ctx, m.ID, "brand-new-pass1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@y.io", "old-temp-pass1"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Error("old password still valid after SetPassword")
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@y.io", "brand-new-pass1"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
	if creds, _ := repo.GetCredentials(ctx, m.ID); creds.MustChangePassword {
		t.Error("must_change_password not cleared")
	}
}

func TestRemoveMemberAnyMemberOnlyInOrg(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	creator, _ := repo.BootstrapLocalAdmin(ctx, "", "boss@z.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, creator.ID)
	m, _ := repo.CreateMemberUser(ctx, orgID, "M", "m@z.io", "temp-password1")
	outsider, err := repo.RegisterLocalUser(ctx, "Eve", "eve@z.io", "evepass-1234")
	if err != nil {
		t.Fatal(err)
	}

	// A user of another org is not a member here: refused, and untouched.
	if err := repo.RemoveMember(ctx, orgID, creator.ID, outsider.ID); !errors.Is(err, model.ErrorUserNotFound) {
		t.Errorf("remove outsider err = %v, want ErrorUserNotFound", err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "eve@z.io", "evepass-1234"); err != nil {
		t.Errorf("outsider lost their account: %v", err)
	}

	// No owner rule: the org's creator can be removed like anyone else, and
	// the org lives on with the remaining member.
	if err := repo.RemoveMember(ctx, orgID, m.ID, creator.ID); err != nil {
		t.Fatalf("remove creator: %v", err)
	}
	members, err := repo.ListMembers(ctx, orgID)
	if err != nil || len(members) != 1 || members[0].ID != m.ID {
		t.Fatalf("members after removing the creator = %+v, %v", members, err)
	}
	if got, err := repo.PrimaryOrganizationID(ctx, m.ID); err != nil || got != orgID {
		t.Errorf("remaining member's org = %q, %v; want %q", got, err, orgID)
	}
}

// Two members removing each other concurrently must not empty the org: once
// the caller itself has been removed, its pending removal is refused.
func TestRemoveMemberRefusesRemovedCaller(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	a, _ := repo.BootstrapLocalAdmin(ctx, "", "a@r.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, a.ID)
	b, _ := repo.CreateMemberUser(ctx, orgID, "B", "b@r.io", "temp-password1")

	// B removes A first; A's in-flight request to remove B then arrives.
	if err := repo.RemoveMember(ctx, orgID, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveMember(ctx, orgID, a.ID, b.ID); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("removal by a removed caller err = %v, want ErrorUserNotFound", err)
	}
	members, err := repo.ListMembers(ctx, orgID)
	if err != nil || len(members) != 1 || members[0].ID != b.ID {
		t.Fatalf("members = %+v, %v; want only B", members, err)
	}
	// A caller from another org is not a member here either.
	other, _ := repo.RegisterLocalUser(ctx, "Eve", "eve@r.io", "evepass-1234")
	if err := repo.RemoveMember(ctx, orgID, other.ID, b.ID); !errors.Is(err, model.ErrorUserNotFound) {
		t.Errorf("foreign caller err = %v, want ErrorUserNotFound", err)
	}
}

func TestRemoveMemberDeletesUserAndTokens(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	admin, _ := repo.BootstrapLocalAdmin(ctx, "", "boss@w.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, admin.ID)
	m, _ := repo.CreateMemberUser(ctx, orgID, "M", "m@w.io", "temp-password1")
	_, patPlain, err := repo.CreateToken(ctx, m.ID, "m's token", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.RemoveMember(ctx, orgID, admin.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@w.io", "temp-password1"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Error("removed member can still log in")
	}
	if _, err := repo.FindByToken(ctx, patPlain); !errors.Is(err, model.ErrInvalidToken) {
		t.Error("removed member's PAT still resolves")
	}
	if members, _ := repo.ListMembers(ctx, orgID); len(members) != 1 {
		t.Errorf("members after removal = %d, want 1", len(members))
	}
}

func TestRegisterLocalUserCreatesOwnOrg(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)

	a, err := repo.RegisterLocalUser(ctx, "Alice", "Alice@X.io", "alicepass-123")
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.RegisterLocalUser(ctx, "Bob", "bob@x.io", "bobpass-1234")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Alice" {
		t.Errorf("name = %q", a.Name)
	}
	orgA, _ := repo.PrimaryOrganizationID(ctx, a.ID)
	orgB, _ := repo.PrimaryOrganizationID(ctx, b.ID)
	if orgA == "" || orgA == orgB {
		t.Errorf("orgs not distinct: %q vs %q", orgA, orgB)
	}
	if role := storedRole(t, d, a.ID); role != "owner" {
		t.Errorf("stored role = %q, want owner", role)
	}
	if u, err := repo.VerifyLocalCredentials(ctx, "alice@x.io", "alicepass-123"); err != nil || u.ID != a.ID {
		t.Errorf("login after register: %v", err)
	}
	if _, err := repo.RegisterLocalUser(ctx, "Eve", "ALICE@x.io", "x-password-1"); !errors.Is(err, model.ErrEmailTaken) {
		t.Errorf("dup email err = %v, want ErrEmailTaken", err)
	}
}

func TestGetUpdateOrganization(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	u, _ := repo.RegisterLocalUser(ctx, "Alice", "a@o.io", "alicepass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, u.ID)

	org, err := repo.GetOrganization(ctx, orgID)
	if err != nil || org.ID != orgID || org.Name == "" {
		t.Fatalf("GetOrganization = %+v, %v", org, err)
	}
	if err := repo.UpdateOrganization(ctx, orgID, "Homelab", "server", "#3b82f6"); err != nil {
		t.Fatal(err)
	}
	org, _ = repo.GetOrganization(ctx, orgID)
	if org.Name != "Homelab" || org.Icon != "server" || org.Color != "#3b82f6" {
		t.Errorf("after update: %+v", org)
	}
	if _, err := repo.GetOrganization(ctx, dbtest.ID(404)); err == nil {
		t.Error("unknown org must error")
	}
}

func TestUserCreatedAtLocationIsUTC(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	u, err := repo.RegisterLocalUser(ctx, "Zed", "z@utc.io", "zedpass-1234")
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt.Location() != time.UTC || got.LastSeenAt.Location() != time.UTC {
		t.Fatalf("locations = %v / %v, want UTC", got.CreatedAt.Location(), got.LastSeenAt.Location())
	}
	if d := time.Since(got.CreatedAt); d < 0 || d > time.Minute {
		t.Fatalf("created_at %v is not now: the session or format shifted it", got.CreatedAt)
	}
}

func TestSetPasswordUnknownUser(t *testing.T) {
	repo, _ := newRepo(t)
	if err := repo.SetPassword(dbtest.Ctx(t), dbtest.ID(404), "whatever-123", false); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("err = %v, want ErrorUserNotFound", err)
	}
	if err := repo.UpdateOrganization(dbtest.Ctx(t), dbtest.ID(404), "x", "y", "#000000"); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("UpdateOrganization err = %v", err)
	}
	if _, err := repo.GetCredentials(dbtest.Ctx(t), dbtest.ID(404)); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("GetCredentials err = %v", err)
	}
}

// A member without local credentials is listed with the provider of their
// connected account and no email.
func TestListMembersReportsProvider(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	g, err := repo.CreateUser(ctx, dto.UserDTO{Name: "Gina", Provider: "google", AccountID: "g-77", AvatarURL: "https://example.com/g.png"})
	if err != nil {
		t.Fatal(err)
	}
	orgID, _ := repo.PrimaryOrganizationID(ctx, g.ID)
	if name := dbtest.QueryString(t, d, `SELECT name FROM organizations WHERE organization_id = ?`, orgID); name != "gina's org" {
		t.Fatalf("org name = %q, want the lower-cased user name", name)
	}
	m, err := repo.CreateMemberUser(ctx, orgID, "Lou", "lou@x.io", "temp-password1")
	if err != nil {
		t.Fatal(err)
	}
	members, err := repo.ListMembers(ctx, orgID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %+v, %v", members, err)
	}
	byID := map[string]model.Member{}
	for _, mm := range members {
		byID[mm.ID] = mm
	}
	if gm := byID[g.ID]; gm.Provider != "google" || gm.Email != "" || gm.AvatarURL != "https://example.com/g.png" {
		t.Errorf("google member = %+v", gm)
	}
	if lm := byID[m.ID]; lm.Provider != "local" || lm.Email != "lou@x.io" {
		t.Errorf("local member = %+v", lm)
	}
}

// Postgres: concurrent mutual removals serialize on the caller's row lock,
// so exactly one succeeds and the org keeps a member. (SQLite serializes
// every writer, which the same assertions also hold for.)
func TestRemoveMemberConcurrentMutualRemoval(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	a, _ := repo.BootstrapLocalAdmin(ctx, "", "a@c.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, a.ID)
	b, _ := repo.CreateMemberUser(ctx, orgID, "B", "b@c.io", "temp-password1")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, pair := range [][2]string{{a.ID, b.ID}, {b.ID, a.ID}} {
		wg.Add(1)
		go func(i int, caller, target string) {
			defer wg.Done()
			errs[i] = repo.RemoveMember(ctx, orgID, caller, target)
		}(i, pair[0], pair[1])
	}
	wg.Wait()
	// The loser fails with ErrorUserNotFound, or on Postgres possibly with
	// a deadlock error (each side's cascade waits on the other's row lock);
	// either way it changed nothing.
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if members, _ := repo.ListMembers(ctx, orgID); len(members) != 1 || ok != 1 {
		t.Fatalf("successes = %d (%v), members left = %d; want exactly one of each", ok, errs, len(members))
	}
}

// The account rows go in one transaction: a membership insert that fails
// (unknown org) after the user row went in leaves no user behind.
func TestCreateMemberUserIsAtomic(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if _, err := repo.CreateMemberUser(ctx, dbtest.ID(404), "Ghost", "ghost@x.io", "temp-password1"); err == nil {
		t.Fatal("member of an unknown org created")
	}
	if n, err := repo.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("users after the failed create = %d, %v; want 0", n, err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "ghost@x.io", "temp-password1"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Fatalf("credentials survived: %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/db/repository/user/`
Expected: build failure listing `undefined: Repo` and `undefined: New`.

- [ ] **Step 3: Implement**

Create `internal/infra/db/repository/user/repo.go`:

```go
// Package user implements port.UserRepository: users, their organizations
// and memberships, connected accounts, local credentials and personal
// access tokens.
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
	"winterflow/pkg/pat"
	"winterflow/pkg/util"
)

type (
	userRow           = sqlitegen.User
	credentialsRow    = sqlitegen.UserCredential
	tokenRow          = sqlitegen.UserToken
	organizationRow   = sqlitegen.Organization
	insertUserParams  = sqlitegen.InsertUserParams
	accountParams     = sqlitegen.InsertConnectedAccountParams
	accountKeyParams  = sqlitegen.GetConnectedAccountUserIDParams
	credentialsParams = sqlitegen.InsertCredentialsParams
	passwordParams    = sqlitegen.UpdatePasswordParams
	insertTokenParams = sqlitegen.InsertTokenParams
	touchTokenParams  = sqlitegen.TouchTokenLastUsedParams
	deleteTokenParams = sqlitegen.DeleteTokenParams
	insertOrgParams   = sqlitegen.InsertOrganizationParams
	updateOrgParams   = sqlitegen.UpdateOrganizationParams
	membershipParams  = sqlitegen.InsertOrganizationUserParams
	memberKeyParams   = sqlitegen.LockOrganizationMemberParams
	isMemberParams    = sqlitegen.IsOrganizationMemberParams
)

type querier interface {
	// users.sql
	GetUser(ctx context.Context, db backend.DBTX, userID string) (userRow, error)
	CountUsers(ctx context.Context, db backend.DBTX) (int64, error)
	InsertUser(ctx context.Context, db backend.DBTX, p insertUserParams) error
	DeleteUser(ctx context.Context, db backend.DBTX, userID string) error
	GetConnectedAccountUserID(ctx context.Context, db backend.DBTX, p accountKeyParams) (string, error)
	InsertConnectedAccount(ctx context.Context, db backend.DBTX, p accountParams) error
	GetFirstConnectedAccountProvider(ctx context.Context, db backend.DBTX, userID string) (string, error)
	InsertCredentials(ctx context.Context, db backend.DBTX, p credentialsParams) error
	GetCredentialsByEmail(ctx context.Context, db backend.DBTX, email string) (credentialsRow, error)
	GetCredentialsByUser(ctx context.Context, db backend.DBTX, userID string) (credentialsRow, error)
	EmailTaken(ctx context.Context, db backend.DBTX, email string) (bool, error)
	UpdatePassword(ctx context.Context, db backend.DBTX, p passwordParams) (int64, error)
	// tokens.sql
	InsertToken(ctx context.Context, db backend.DBTX, p insertTokenParams) error
	GetTokenByHash(ctx context.Context, db backend.DBTX, tokenHash string) (tokenRow, error)
	TouchTokenLastUsed(ctx context.Context, db backend.DBTX, p touchTokenParams) error
	ListTokensByUser(ctx context.Context, db backend.DBTX, userID string) ([]tokenRow, error)
	DeleteToken(ctx context.Context, db backend.DBTX, p deleteTokenParams) (int64, error)
	// organizations.sql
	InsertOrganization(ctx context.Context, db backend.DBTX, p insertOrgParams) error
	GetOrganization(ctx context.Context, db backend.DBTX, organizationID string) (organizationRow, error)
	UpdateOrganization(ctx context.Context, db backend.DBTX, p updateOrgParams) (int64, error)
	InsertOrganizationUser(ctx context.Context, db backend.DBTX, p membershipParams) error
	GetPrimaryOrganizationID(ctx context.Context, db backend.DBTX, userID string) (string, error)
	LockOrganizationMember(ctx context.Context, db backend.DBTX, p memberKeyParams) (string, error)
	IsOrganizationMember(ctx context.Context, db backend.DBTX, p isMemberParams) (bool, error)
	ListOrganizationMembers(ctx context.Context, db backend.DBTX, organizationID string) ([]userRow, error)
}

// Repo is the users repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.UserRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("user repository: unknown database driver %q", driver)
}

func hydrate(row userRow) model.User {
	return model.User{
		ID:         row.UserID,
		Name:       row.Name,
		AvatarURL:  util.DerefString(row.Avatar),
		CreatedAt:  row.CreatedAt.Time(),
		LastSeenAt: row.LastSeen.Time(),
	}
}

// GetByConnectedAccount resolves a provider account to its user;
// model.ErrorUserNotFound when unknown.
func (r *Repo) GetByConnectedAccount(ctx context.Context, provider, accountID string) (model.User, error) {
	userID, err := r.q.GetConnectedAccountUserID(ctx, r.tx.Querier(ctx), accountKeyParams{Provider: provider, ExternalID: accountID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}
	return r.GetUser(ctx, userID)
}

// CreateUser creates a user, their own organization (named after the
// lower-cased user name; they own it) and the connected account, in one
// transaction.
func (r *Repo) CreateUser(ctx context.Context, d dto.UserDTO) (model.User, error) {
	now := types.NewDateTime()
	orgID := util.GenerateID()
	user := insertUserParams{
		UserID:    util.GenerateID(),
		Name:      d.Name,
		Avatar:    util.RefString(d.AvatarURL),
		CreatedAt: now,
		LastSeen:  now,
	}
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if err := r.q.InsertOrganization(ctx, db, insertOrgParams{OrganizationID: orgID, Name: strings.ToLower(d.Name) + "'s org", CreatedAt: now}); err != nil {
			return err
		}
		if err := r.q.InsertUser(ctx, db, user); err != nil {
			return err
		}
		if err := r.q.InsertOrganizationUser(ctx, db, membershipParams{OrganizationID: orgID, UserID: user.UserID, Role: model.RoleOwner.Value(), CreatedAt: now}); err != nil {
			return err
		}
		return r.q.InsertConnectedAccount(ctx, db, accountParams{Provider: d.Provider, ExternalID: d.AccountID, UserID: user.UserID})
	})
	if err != nil {
		return model.User{}, err
	}
	return model.User{
		ID:         user.UserID,
		Name:       user.Name,
		AvatarURL:  util.DerefString(user.Avatar),
		CreatedAt:  now.Time(),
		LastSeenAt: now.Time(),
	}, nil
}

// FindOrCreateUser resolves the user for a connected account, creating the
// user (plus their org and membership) on first login.
func (r *Repo) FindOrCreateUser(ctx context.Context, d dto.UserDTO) (model.User, error) {
	user, err := r.GetByConnectedAccount(ctx, d.Provider, d.AccountID)
	if err != nil {
		if !errors.Is(err, model.ErrorUserNotFound) {
			return model.User{}, err
		}
		cu, err := r.CreateUser(ctx, d)
		if err != nil {
			r.log.Error("failed to create user: %v", err)
			return model.User{}, err
		}
		return cu, nil
	}
	return user, nil
}

// GetUser returns a user; model.ErrorUserNotFound when there is none.
func (r *Repo) GetUser(ctx context.Context, userID string) (model.User, error) {
	row, err := r.q.GetUser(ctx, r.tx.Querier(ctx), userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}
	return hydrate(row), nil
}

// PrimaryOrganizationID returns the user's organization. The 1-user-per-org
// model gives each user exactly one (created at signup); if a user ever
// belongs to several, the oldest membership wins so the result is stable.
func (r *Repo) PrimaryOrganizationID(ctx context.Context, userID string) (string, error) {
	id, err := r.q.GetPrimaryOrganizationID(ctx, r.tx.Querier(ctx), userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", model.ErrorUserNotFound
		}
		return "", err
	}
	return id, nil
}

// lastUsedWriteInterval throttles last_used_at updates so a busy API client
// costs at most one write per minute, not one per request.
const lastUsedWriteInterval = time.Minute

// FindByToken resolves a PAT to its user, enforcing expiry, and stamps
// last_used_at at most once a minute (a failed stamp is only logged).
func (r *Repo) FindByToken(ctx context.Context, plaintext string) (model.User, error) {
	t, err := r.q.GetTokenByHash(ctx, r.tx.Querier(ctx), pat.Hash(plaintext))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrInvalidToken
		}
		return model.User{}, err
	}
	if t.ExpiresAt != nil && time.Now().After(t.ExpiresAt.Time()) {
		return model.User{}, model.ErrInvalidToken
	}
	if t.LastUsedAt == nil || time.Since(t.LastUsedAt.Time()) > lastUsedWriteInterval {
		now := types.NewDateTime()
		if err := r.q.TouchTokenLastUsed(ctx, r.tx.Querier(ctx), touchTokenParams{LastUsedAt: &now, TokenID: t.TokenID}); err != nil {
			r.log.Error("FindByToken: stamp last_used_at", "error", err)
		}
	}
	return r.GetUser(ctx, t.UserID)
}

// CreateToken mints a PAT. Only its hash is stored; the plaintext is
// returned once.
func (r *Repo) CreateToken(ctx context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error) {
	plaintext, hash, prefix, err := pat.Generate()
	if err != nil {
		return model.UserToken{}, "", err
	}
	row := insertTokenParams{
		TokenID:     util.GenerateID(),
		UserID:      userID,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   hash,
		TokenType:   "pat",
		CreatedAt:   types.NewDateTime(),
	}
	if expiresAt != nil {
		dt := types.DateTime(*expiresAt)
		row.ExpiresAt = &dt
	}
	if err := r.q.InsertToken(ctx, r.tx.Querier(ctx), row); err != nil {
		return model.UserToken{}, "", err
	}
	return toDomainToken(tokenRow(row)), plaintext, nil
}

// ListTokens returns the user's tokens, newest first.
func (r *Repo) ListTokens(ctx context.Context, userID string) ([]model.UserToken, error) {
	rows, err := r.q.ListTokensByUser(ctx, r.tx.Querier(ctx), userID)
	if err != nil {
		return nil, err
	}
	out := make([]model.UserToken, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainToken(row))
	}
	return out, nil
}

// DeleteToken removes the user's token; model.ErrTokenNotFound when the id
// does not exist or belongs to someone else.
func (r *Repo) DeleteToken(ctx context.Context, userID, tokenID string) error {
	n, err := r.q.DeleteToken(ctx, r.tx.Querier(ctx), deleteTokenParams{TokenID: tokenID, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrTokenNotFound
	}
	return nil
}

func toDomainToken(t tokenRow) model.UserToken {
	out := model.UserToken{
		ID:        t.TokenID,
		UserID:    t.UserID,
		Name:      t.Name,
		Prefix:    t.TokenPrefix,
		CreatedAt: t.CreatedAt.Time(),
	}
	if t.ExpiresAt != nil {
		x := t.ExpiresAt.Time()
		out.ExpiresAt = &x
	}
	if t.LastUsedAt != nil {
		x := t.LastUsedAt.Time()
		out.LastUsedAt = &x
	}
	return out
}

// --- local credentials & org membership -------------------------------------

// normalizeEmail is applied at every boundary so lookups and uniqueness are
// case/whitespace-insensitive.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// CountUsers reports how many users exist.
func (r *Repo) CountUsers(ctx context.Context) (int, error) {
	n, err := r.q.CountUsers(ctx, r.tx.Querier(ctx))
	return int(n), err
}

// localAccount inserts the rows every local (email + password) account has:
// user, credentials, connected account "local"/email, membership.
func (r *Repo) localAccount(ctx context.Context, orgID, role string, user insertUserParams, email, hash string, mustChange bool) error {
	db := r.tx.Querier(ctx)
	if err := r.q.InsertUser(ctx, db, user); err != nil {
		return err
	}
	if err := r.q.InsertOrganizationUser(ctx, db, membershipParams{OrganizationID: orgID, UserID: user.UserID, Role: role, CreatedAt: types.NewDateTime()}); err != nil {
		return err
	}
	if err := r.q.InsertConnectedAccount(ctx, db, accountParams{Provider: "local", ExternalID: email, UserID: user.UserID}); err != nil {
		return err
	}
	return r.q.InsertCredentials(ctx, db, credentialsParams{
		UserID: user.UserID, Email: email, PasswordHash: hash, MustChangePassword: mustChange, UpdatedAt: types.NewDateTime(),
	})
}

func newUser(name string) insertUserParams {
	now := types.NewDateTime()
	return insertUserParams{UserID: util.GenerateID(), Name: name, CreatedAt: now, LastSeen: now}
}

func created(u insertUserParams) model.User {
	return model.User{ID: u.UserID, Name: u.Name, CreatedAt: u.CreatedAt.Time(), LastSeenAt: u.LastSeen.Time()}
}

// BootstrapLocalAdmin creates the very first account of an instance from the
// first login's email+password: user + personal org (owner) + local connected
// account + credentials. Refuses when any user already exists: the count is
// re-checked inside the transaction so a racing double-submit has one winner.
func (r *Repo) BootstrapLocalAdmin(ctx context.Context, name, email, password string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := hashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = email
		if i := strings.IndexByte(email, '@'); i > 0 {
			name = email[:i]
		}
	}
	orgID := util.GenerateID()
	user := newUser(name)
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.CountUsers(ctx, r.tx.Querier(ctx))
		if err != nil {
			return err
		}
		if n > 0 {
			return model.ErrNotBootstrap
		}
		if err := r.q.InsertOrganization(ctx, r.tx.Querier(ctx), insertOrgParams{OrganizationID: orgID, Name: name + "'s org", CreatedAt: types.NewDateTime()}); err != nil {
			return err
		}
		return r.localAccount(ctx, orgID, model.RoleOwner.Value(), user, email, hash, false)
	})
	if err != nil {
		return model.User{}, err
	}
	return created(user), nil
}

// VerifyLocalCredentials checks an email+password pair;
// model.ErrInvalidCredentials on any mismatch.
func (r *Repo) VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error) {
	creds, err := r.q.GetCredentialsByEmail(ctx, r.tx.Querier(ctx), normalizeEmail(email))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrInvalidCredentials
		}
		return model.User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(creds.PasswordHash), []byte(password)) != nil {
		return model.User{}, model.ErrInvalidCredentials
	}
	return r.GetUser(ctx, creds.UserID)
}

// CreateMemberUser creates an account inside an existing organization:
// user + credentials (must-change temp password) + local connected account +
// membership. No personal org. The stored role is model.RoleAdmin.
func (r *Repo) CreateMemberUser(ctx context.Context, orgID, name, email, tempPassword string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := hashPassword(tempPassword)
	if err != nil {
		return model.User{}, err
	}
	user := newUser(name)
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		taken, err := r.q.EmailTaken(ctx, r.tx.Querier(ctx), email)
		if err != nil {
			return err
		}
		if taken {
			return model.ErrEmailTaken
		}
		return r.localAccount(ctx, orgID, model.RoleAdmin.Value(), user, email, hash, true)
	})
	if err != nil {
		return model.User{}, err
	}
	return created(user), nil
}

// SetPassword replaces the hash and must-change flag;
// model.ErrorUserNotFound for a user without local credentials.
func (r *Repo) SetPassword(ctx context.Context, userID, password string, mustChange bool) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	n, err := r.q.UpdatePassword(ctx, r.tx.Querier(ctx), passwordParams{
		PasswordHash: hash, MustChangePassword: mustChange, UpdatedAt: types.NewDateTime(), UserID: userID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrorUserNotFound
	}
	return nil
}

// GetCredentials returns the non-secret credential info;
// model.ErrorUserNotFound for users without local credentials.
func (r *Repo) GetCredentials(ctx context.Context, userID string) (model.Credentials, error) {
	creds, err := r.q.GetCredentialsByUser(ctx, r.tx.Querier(ctx), userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Credentials{}, model.ErrorUserNotFound
		}
		return model.Credentials{}, err
	}
	return model.Credentials{Email: creds.Email, MustChangePassword: creds.MustChangePassword}, nil
}

// ListMembers returns the org's members, oldest membership first, each with
// their email and "local" when they have credentials, else the provider of
// one of their connected accounts.
func (r *Repo) ListMembers(ctx context.Context, orgID string) ([]model.Member, error) {
	rows, err := r.q.ListOrganizationMembers(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Member, 0, len(rows))
	for _, row := range rows {
		m := model.Member{User: hydrate(row)}
		if creds, err := r.GetCredentials(ctx, row.UserID); err == nil {
			m.Email = creds.Email
			m.Provider = "local"
		} else if provider, err := r.q.GetFirstConnectedAccountProvider(ctx, r.tx.Querier(ctx), row.UserID); err == nil {
			m.Provider = provider
		}
		out = append(out, m)
	}
	return out, nil
}

// RemoveMember deletes the member's user row outright (memberships,
// credentials, connected accounts, and PATs go with it via FK cascade).
// Only a member of orgID can be removed: anyone else is
// model.ErrorUserNotFound and stays untouched.
//
// callerID is the member doing the removing. Their own membership row is
// re-read inside the transaction (locked FOR UPDATE on Postgres; SQLite
// serializes writers), so two members removing each other concurrently
// cannot empty the org: the second removal finds its caller gone and is
// refused with model.ErrorUserNotFound.
func (r *Repo) RemoveMember(ctx context.Context, orgID, callerID, userID string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if _, err := r.q.LockOrganizationMember(ctx, db, memberKeyParams{OrganizationID: orgID, UserID: callerID}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return model.ErrorUserNotFound
			}
			return err
		}
		member, err := r.q.IsOrganizationMember(ctx, db, isMemberParams{OrganizationID: orgID, UserID: userID})
		if err != nil {
			return err
		}
		if !member {
			return model.ErrorUserNotFound
		}
		return r.q.DeleteUser(ctx, db, userID)
	})
}

// RegisterLocalUser is open self-signup (distributed topology): user + their
// OWN organization (owner) + credentials + local connected account.
func (r *Repo) RegisterLocalUser(ctx context.Context, name, email, password string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := hashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	orgID := util.GenerateID()
	user := newUser(name)
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		taken, err := r.q.EmailTaken(ctx, r.tx.Querier(ctx), email)
		if err != nil {
			return err
		}
		if taken {
			return model.ErrEmailTaken
		}
		if err := r.q.InsertOrganization(ctx, r.tx.Querier(ctx), insertOrgParams{OrganizationID: orgID, Name: name + "'s org", CreatedAt: types.NewDateTime()}); err != nil {
			return err
		}
		return r.localAccount(ctx, orgID, model.RoleOwner.Value(), user, email, hash, false)
	})
	if err != nil {
		return model.User{}, err
	}
	return created(user), nil
}

// GetOrganization returns the org's identity; model.ErrorUserNotFound when
// unknown.
func (r *Repo) GetOrganization(ctx context.Context, orgID string) (model.Organization, error) {
	org, err := r.q.GetOrganization(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Organization{}, model.ErrorUserNotFound
		}
		return model.Organization{}, err
	}
	return model.Organization{ID: org.OrganizationID, Name: org.Name, Icon: org.Icon, Color: org.Color}, nil
}

// UpdateOrganization sets the org's identity; model.ErrorUserNotFound when
// unknown.
func (r *Repo) UpdateOrganization(ctx context.Context, orgID, name, icon, color string) error {
	n, err := r.q.UpdateOrganization(ctx, r.tx.Querier(ctx), updateOrgParams{Name: name, Icon: icon, Color: color, OrganizationID: orgID})
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrorUserNotFound
	}
	return nil
}
```

Create `internal/infra/db/repository/user/sqlite.go`:

```go
package user

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) GetUser(ctx context.Context, db backend.DBTX, userID string) (userRow, error) {
	return sqlitegen.New(db).GetUser(ctx, userID)
}

func (sqliteQuerier) CountUsers(ctx context.Context, db backend.DBTX) (int64, error) {
	return sqlitegen.New(db).CountUsers(ctx)
}

func (sqliteQuerier) InsertUser(ctx context.Context, db backend.DBTX, p insertUserParams) error {
	return sqlitegen.New(db).InsertUser(ctx, p)
}

func (sqliteQuerier) DeleteUser(ctx context.Context, db backend.DBTX, userID string) error {
	return sqlitegen.New(db).DeleteUser(ctx, userID)
}

func (sqliteQuerier) GetConnectedAccountUserID(ctx context.Context, db backend.DBTX, p accountKeyParams) (string, error) {
	return sqlitegen.New(db).GetConnectedAccountUserID(ctx, p)
}

func (sqliteQuerier) InsertConnectedAccount(ctx context.Context, db backend.DBTX, p accountParams) error {
	return sqlitegen.New(db).InsertConnectedAccount(ctx, p)
}

func (sqliteQuerier) GetFirstConnectedAccountProvider(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return sqlitegen.New(db).GetFirstConnectedAccountProvider(ctx, userID)
}

func (sqliteQuerier) InsertCredentials(ctx context.Context, db backend.DBTX, p credentialsParams) error {
	return sqlitegen.New(db).InsertCredentials(ctx, p)
}

func (sqliteQuerier) GetCredentialsByEmail(ctx context.Context, db backend.DBTX, email string) (credentialsRow, error) {
	return sqlitegen.New(db).GetCredentialsByEmail(ctx, email)
}

func (sqliteQuerier) GetCredentialsByUser(ctx context.Context, db backend.DBTX, userID string) (credentialsRow, error) {
	return sqlitegen.New(db).GetCredentialsByUser(ctx, userID)
}

func (sqliteQuerier) EmailTaken(ctx context.Context, db backend.DBTX, email string) (bool, error) {
	return sqlitegen.New(db).EmailTaken(ctx, email)
}

func (sqliteQuerier) UpdatePassword(ctx context.Context, db backend.DBTX, p passwordParams) (int64, error) {
	return sqlitegen.New(db).UpdatePassword(ctx, p)
}

func (sqliteQuerier) InsertToken(ctx context.Context, db backend.DBTX, p insertTokenParams) error {
	return sqlitegen.New(db).InsertToken(ctx, p)
}

func (sqliteQuerier) GetTokenByHash(ctx context.Context, db backend.DBTX, tokenHash string) (tokenRow, error) {
	return sqlitegen.New(db).GetTokenByHash(ctx, tokenHash)
}

func (sqliteQuerier) TouchTokenLastUsed(ctx context.Context, db backend.DBTX, p touchTokenParams) error {
	return sqlitegen.New(db).TouchTokenLastUsed(ctx, p)
}

func (sqliteQuerier) ListTokensByUser(ctx context.Context, db backend.DBTX, userID string) ([]tokenRow, error) {
	return sqlitegen.New(db).ListTokensByUser(ctx, userID)
}

func (sqliteQuerier) DeleteToken(ctx context.Context, db backend.DBTX, p deleteTokenParams) (int64, error) {
	return sqlitegen.New(db).DeleteToken(ctx, p)
}

func (sqliteQuerier) InsertOrganization(ctx context.Context, db backend.DBTX, p insertOrgParams) error {
	return sqlitegen.New(db).InsertOrganization(ctx, p)
}

func (sqliteQuerier) GetOrganization(ctx context.Context, db backend.DBTX, organizationID string) (organizationRow, error) {
	return sqlitegen.New(db).GetOrganization(ctx, organizationID)
}

func (sqliteQuerier) UpdateOrganization(ctx context.Context, db backend.DBTX, p updateOrgParams) (int64, error) {
	return sqlitegen.New(db).UpdateOrganization(ctx, p)
}

func (sqliteQuerier) InsertOrganizationUser(ctx context.Context, db backend.DBTX, p membershipParams) error {
	return sqlitegen.New(db).InsertOrganizationUser(ctx, p)
}

func (sqliteQuerier) GetPrimaryOrganizationID(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return sqlitegen.New(db).GetPrimaryOrganizationID(ctx, userID)
}

func (sqliteQuerier) LockOrganizationMember(ctx context.Context, db backend.DBTX, p memberKeyParams) (string, error) {
	return sqlitegen.New(db).LockOrganizationMember(ctx, p)
}

func (sqliteQuerier) IsOrganizationMember(ctx context.Context, db backend.DBTX, p isMemberParams) (bool, error) {
	return sqlitegen.New(db).IsOrganizationMember(ctx, p)
}

func (sqliteQuerier) ListOrganizationMembers(ctx context.Context, db backend.DBTX, organizationID string) ([]userRow, error) {
	return sqlitegen.New(db).ListOrganizationMembers(ctx, organizationID)
}
```

Create `internal/infra/db/repository/user/pgsql.go`:

```go
package user

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) GetUser(ctx context.Context, db backend.DBTX, userID string) (userRow, error) {
	row, err := pgsqlgen.New(db).GetUser(ctx, userID)
	return userRow(row), err
}

func (pgsqlQuerier) CountUsers(ctx context.Context, db backend.DBTX) (int64, error) {
	return pgsqlgen.New(db).CountUsers(ctx)
}

func (pgsqlQuerier) InsertUser(ctx context.Context, db backend.DBTX, p insertUserParams) error {
	return pgsqlgen.New(db).InsertUser(ctx, pgsqlgen.InsertUserParams(p))
}

func (pgsqlQuerier) DeleteUser(ctx context.Context, db backend.DBTX, userID string) error {
	return pgsqlgen.New(db).DeleteUser(ctx, userID)
}

func (pgsqlQuerier) GetConnectedAccountUserID(ctx context.Context, db backend.DBTX, p accountKeyParams) (string, error) {
	return pgsqlgen.New(db).GetConnectedAccountUserID(ctx, pgsqlgen.GetConnectedAccountUserIDParams(p))
}

func (pgsqlQuerier) InsertConnectedAccount(ctx context.Context, db backend.DBTX, p accountParams) error {
	return pgsqlgen.New(db).InsertConnectedAccount(ctx, pgsqlgen.InsertConnectedAccountParams(p))
}

func (pgsqlQuerier) GetFirstConnectedAccountProvider(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return pgsqlgen.New(db).GetFirstConnectedAccountProvider(ctx, userID)
}

func (pgsqlQuerier) InsertCredentials(ctx context.Context, db backend.DBTX, p credentialsParams) error {
	return pgsqlgen.New(db).InsertCredentials(ctx, pgsqlgen.InsertCredentialsParams(p))
}

func (pgsqlQuerier) GetCredentialsByEmail(ctx context.Context, db backend.DBTX, email string) (credentialsRow, error) {
	row, err := pgsqlgen.New(db).GetCredentialsByEmail(ctx, email)
	return credentialsRow(row), err
}

func (pgsqlQuerier) GetCredentialsByUser(ctx context.Context, db backend.DBTX, userID string) (credentialsRow, error) {
	row, err := pgsqlgen.New(db).GetCredentialsByUser(ctx, userID)
	return credentialsRow(row), err
}

func (pgsqlQuerier) EmailTaken(ctx context.Context, db backend.DBTX, email string) (bool, error) {
	return pgsqlgen.New(db).EmailTaken(ctx, email)
}

func (pgsqlQuerier) UpdatePassword(ctx context.Context, db backend.DBTX, p passwordParams) (int64, error) {
	return pgsqlgen.New(db).UpdatePassword(ctx, pgsqlgen.UpdatePasswordParams(p))
}

func (pgsqlQuerier) InsertToken(ctx context.Context, db backend.DBTX, p insertTokenParams) error {
	return pgsqlgen.New(db).InsertToken(ctx, pgsqlgen.InsertTokenParams(p))
}

func (pgsqlQuerier) GetTokenByHash(ctx context.Context, db backend.DBTX, tokenHash string) (tokenRow, error) {
	row, err := pgsqlgen.New(db).GetTokenByHash(ctx, tokenHash)
	return tokenRow(row), err
}

func (pgsqlQuerier) TouchTokenLastUsed(ctx context.Context, db backend.DBTX, p touchTokenParams) error {
	return pgsqlgen.New(db).TouchTokenLastUsed(ctx, pgsqlgen.TouchTokenLastUsedParams(p))
}

func (pgsqlQuerier) ListTokensByUser(ctx context.Context, db backend.DBTX, userID string) ([]tokenRow, error) {
	rows, err := pgsqlgen.New(db).ListTokensByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]tokenRow, len(rows))
	for i, row := range rows {
		out[i] = tokenRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) DeleteToken(ctx context.Context, db backend.DBTX, p deleteTokenParams) (int64, error) {
	return pgsqlgen.New(db).DeleteToken(ctx, pgsqlgen.DeleteTokenParams(p))
}

func (pgsqlQuerier) InsertOrganization(ctx context.Context, db backend.DBTX, p insertOrgParams) error {
	return pgsqlgen.New(db).InsertOrganization(ctx, pgsqlgen.InsertOrganizationParams(p))
}

func (pgsqlQuerier) GetOrganization(ctx context.Context, db backend.DBTX, organizationID string) (organizationRow, error) {
	row, err := pgsqlgen.New(db).GetOrganization(ctx, organizationID)
	return organizationRow(row), err
}

func (pgsqlQuerier) UpdateOrganization(ctx context.Context, db backend.DBTX, p updateOrgParams) (int64, error) {
	return pgsqlgen.New(db).UpdateOrganization(ctx, pgsqlgen.UpdateOrganizationParams(p))
}

func (pgsqlQuerier) InsertOrganizationUser(ctx context.Context, db backend.DBTX, p membershipParams) error {
	return pgsqlgen.New(db).InsertOrganizationUser(ctx, pgsqlgen.InsertOrganizationUserParams(p))
}

func (pgsqlQuerier) GetPrimaryOrganizationID(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return pgsqlgen.New(db).GetPrimaryOrganizationID(ctx, userID)
}

func (pgsqlQuerier) LockOrganizationMember(ctx context.Context, db backend.DBTX, p memberKeyParams) (string, error) {
	return pgsqlgen.New(db).LockOrganizationMember(ctx, pgsqlgen.LockOrganizationMemberParams(p))
}

func (pgsqlQuerier) IsOrganizationMember(ctx context.Context, db backend.DBTX, p isMemberParams) (bool, error) {
	return pgsqlgen.New(db).IsOrganizationMember(ctx, pgsqlgen.IsOrganizationMemberParams(p))
}

func (pgsqlQuerier) ListOrganizationMembers(ctx context.Context, db backend.DBTX, organizationID string) ([]userRow, error) {
	rows, err := pgsqlgen.New(db).ListOrganizationMembers(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	out := make([]userRow, len(rows))
	for i, row := range rows {
		out[i] = userRow(row)
	}
	return out, nil
}
```

- [ ] **Step 4: Run on SQLite**

Run: `go test -count=1 ./internal/infra/db/repository/user/ -v 2>&1 | grep -E '^(---|ok)'`
Expected: `--- PASS` for all 22 tests (`TestNewRejectsUnknownDriver` … `TestCreateMemberUserIsAtomic`); `ok` (a few seconds: bcrypt).

- [ ] **Step 5: Run on Postgres**

Run: `make test-pg TEST_PG_ARGS='-run "TestCreateUser|TestUserLookups|TestCreateToken|TestFindByToken|TestDeleteToken|TestFindOrCreate|TestBootstrapLocalAdmin|TestVerifyLocal|TestCreateMemberUser|TestSetPassword|TestRemoveMember|TestRegisterLocalUser|TestGetUpdateOrganization|TestUserCreatedAt|TestListMembers|TestNewRejectsUnknownDriver" -v' 2>&1 | grep -E 'repository/user\s|^--- FAIL'`
Expected: `ok  winterflow/internal/infra/db/repository/user`, no `--- FAIL`. `TestRemoveMemberConcurrentMutualRemoval` exercises the `FOR UPDATE` path here (exactly one removal wins; the loser gets `ErrorUserNotFound` or, if the two cascades interleave, a Postgres deadlock error — either way it changed nothing).

- [ ] **Step 6: Everything still green**

Run: `gofmt -l internal && go build ./... && go vet ./... && go test ./...`
Expected: no gofmt output; all `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db/repository/user
git commit -m "refactor(db): users repository on sqlc

RemoveMember's caller lock is FOR UPDATE in the Postgres query and a plain
SELECT in the SQLite one: no dialect branch left in Go. Account creation,
tokens, credentials and membership semantics are unchanged.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Wire the sqlc repositories, delete Bun

**Files:**
- Modify: `internal/infra/bootstrap/core.go`, `internal/infra/bootstrap/api.go`, `internal/infra/bootstrap/standalone.go`, Test: `internal/infra/bootstrap/core_test.go`
- Modify (tests): `internal/app/web/contract/harness_test.go`, `internal/app/web/contract/db_test.go`, `internal/app/web/contract/migration_test.go`, `internal/app/web/handler/server/handler_test.go`
- Delete: `internal/infra/db/bun.go`, `internal/infra/db/bootstrap.go`, `internal/infra/db/bun_test.go`, `internal/infra/db/migration_atomic_test.go`, `internal/infra/db/freeze_dump_test.go`, `internal/infra/db/models/models.go`, `internal/infra/db/migrations/migrations.go` and the eight `internal/infra/db/migrations/2*.go`, `internal/infra/db/repository/{user,server,app,app_domain,server_domain}.go` and their `_test.go` files (the flat `repository` package)
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `db.Open`, `*db.DB{SQL, Driver, Tx}`, `userrepo.New`, `serverrepo.New` (+ `FirstServerID`), `apprepo.New`, `appdomainrepo.New`, `serverdomainrepo.New`, `migrate.Migrate`, `migrate.MigrateTo`, `sqlite.Open`, `pgsql.Open`, `dbtest.Rebind`, `dbtest.Exec`, `backend.SQLite`/`Postgres`.
- Produces: `func WireCore(ctx context.Context, b bus.Bus, conn *db.DB, cfg *config.ServerConfig, log *logger.Logger) (*Deps, *serverrepo.Repo)`; `func publishEmbeddedCapabilities(ctx context.Context, b bus.Bus, serverRepo *serverrepo.Repo, cfg *config.ServerConfig, log *logger.Logger, ingressEnabled bool)`; unexported `newRepos(conn *db.DB, log *logger.Logger) (repos, error)`.

From this commit on, booting the binary adopts an existing Bun-era database (copies `bun_migrations` into `schema_migrations`); the Bun tables stay until Task 11.

- [ ] **Step 1: Write the failing wiring test**

In `internal/infra/bootstrap/core_test.go`, `TestWireCoreWiresResponsePipeline`, replace

```go
	conn := db.NewBunConnection(log, cfg.GetDbURL())
	t.Cleanup(func() { _ = conn.Shutdown() })
```

with

```go
	conn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
```

and append:

```go
func TestNewReposRejectsUnknownDriver(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	if _, err := newRepos(&db.DB{Driver: "mysql"}, log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}
```

Run: `go test ./internal/infra/bootstrap/`
Expected: build failure — `cannot use conn (variable of type *db.DB) as *db.BunConnection value in argument to WireCore` and `undefined: newRepos`.

- [ ] **Step 2: Rewire the bootstrap**

Replace `internal/infra/bootstrap/core.go` with:

```go
package bootstrap

import (
	"context"

	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/db"
	apprepo "winterflow/internal/infra/db/repository/app"
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
	userrepo "winterflow/internal/infra/db/repository/user"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/dispatch"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// WireCore builds the wiring both topologies share: DB-backed repositories and
// services, the notification manager, the command dispatcher, the status
// cache, and the response/events subscribers. The topologies differ only in
// the bus they pass (Redis vs in-process) and the extras they add afterwards
// (standalone: certs, in-process bridge, self-registration; api: nothing).
// The concrete server repository is returned alongside Deps for standalone's
// registration flow, which needs methods beyond the port.
func WireCore(ctx context.Context, b bus.Bus, conn *db.DB, cfg *config.ServerConfig, log *logger.Logger) (*Deps, *serverrepo.Repo) {
	r, err := newRepos(conn, log)
	if err != nil {
		// Only an unknown driver name fails, which db.Open never returns.
		log.Fatalf("wire repositories: %v", err)
	}

	nm := notificationsvc.NewNotificationManager(log)
	dispatcher := dispatch.NewManager(b, nm, cfg, log)
	statusCache := status.NewCache(statusTTL)

	startResponseSubscriber(ctx, b, dispatcher, cfg, log)
	startEventsSubscriber(ctx, b, statusCache, r.server, nm, cfg, log, newDomainSync(ctx, dispatcher, r.serverDomain, log))

	return &Deps{
		Log:                    log,
		Cfg:                    cfg,
		UserService:            r.user,
		ServerRepository:       r.server,
		AppRepository:          r.app,
		AppDomainRepository:    r.appDomain,
		ServerDomainRepository: r.serverDomain,
		CommandDispatcher:      dispatcher,
		NotificationManager:    nm,
		StatusCache:            statusCache,
	}, r.server
}

// repos are the five DB repositories, built for the connection's driver.
type repos struct {
	user         *userrepo.Repo
	server       *serverrepo.Repo
	app          *apprepo.Repo
	appDomain    *appdomainrepo.Repo
	serverDomain *serverdomainrepo.Repo
}

func newRepos(conn *db.DB, log *logger.Logger) (repos, error) {
	var (
		r   repos
		err error
	)
	if r.user, err = userrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.server, err = serverrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.app, err = apprepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.appDomain, err = appdomainrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	r.serverDomain, err = serverdomainrepo.New(conn.Tx, conn.Driver, log)
	return r, err
}
```

In `internal/infra/bootstrap/api.go` replace `dbconn := db.NewBunConnection(log, cfg.GetDbURL())` with:

```go
	dbconn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		log.Fatal("Failed to set up database connection", "error", err)
	}
```

In `internal/infra/bootstrap/standalone.go`:
- import `serverrepo "winterflow/internal/infra/db/repository/server"` instead of `"winterflow/internal/infra/db/repository"`;
- replace `dbconn := db.NewBunConnection(log, cfg.GetDbURL())` with the same four lines as in `api.go` (the following `certmanager, err := cert.NewManager(cfg, log)` still compiles: `certmanager` is new);
- change `publishEmbeddedCapabilities`'s parameter `serverRepo *repository.DbServerRepository` to `serverRepo *serverrepo.Repo`.

Run: `go test -count=1 ./internal/infra/bootstrap/`
Expected: `ok`.

- [ ] **Step 3: Port the remaining tests that built Bun connections**

`internal/app/web/handler/server/handler_test.go`: replace the imports `"winterflow/internal/infra/db"` / `"winterflow/internal/infra/db/repository"` with

```go
	"winterflow/internal/infra/db"
	serverrepo "winterflow/internal/infra/db/repository/server"
	userrepo "winterflow/internal/infra/db/repository/user"
```

and in `newEnv` replace the connection and repository lines with

```go
	// SQLite on purpose: the ids below ("srv-1") are not UUIDs.
	conn, err := db.Open(log, "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	userRepo, err := userrepo.New(conn.Tx, conn.Driver, log)
	if err != nil {
		t.Fatal(err)
	}
	serverRepo, err := serverrepo.New(conn.Tx, conn.Driver, log)
	if err != nil {
		t.Fatal(err)
	}
```

`internal/app/web/contract/db_test.go`: replace the import `"github.com/uptrace/bun/driver/pgdriver"` with `_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver for the admin connection`; in `freshDB` replace `admin := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(base)))` with

```go
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
```

and update the two comments: `// the server accepts only postgres:// (db.Open)` and `// A non-UTC session zone: the server must pin UTC itself (pgsql.utcDSN).`

`internal/app/web/contract/harness_test.go`: import `"winterflow/internal/infra/db/dbtest"` instead of `"winterflow/internal/infra/db/models"`; field `conn *db.DB`; in `newEnv` replace the connection lines with

```go
	conn, err := db.Open(testLog, freshDB(t))
	if err != nil {
		t.Fatal(err)
	}
	// Parallel tests share one Postgres; keep each test's pool small.
	if conn.Driver == "postgres" {
		conn.SQL.SetMaxOpenConns(4)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
```

(SQLite keeps production's single connection — Decision 10) and the body of `setStoredRole` with

```go
	dbtest.Exec(t, e.conn, `UPDATE organization_users SET role = ? WHERE user_id = ?`, role, userID)
```

Replace `internal/app/web/contract/migration_test.go` with (the "before" schema now comes from `migrate.MigrateTo`; the UUID test no longer needs to rewind a migrated schema):

```go
package contract_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/migrate"
	"winterflow/internal/infra/db/pgsql"
	"winterflow/internal/infra/db/sqlite"
)

// openRaw opens a fresh, unmigrated database of the run's dialect through
// the production opener.
func openRaw(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := freshDB(t)
	var (
		raw    *sql.DB
		err    error
		driver = backend.SQLite
	)
	if dialect(t) == "postgres" {
		driver = backend.Postgres
		raw, err = pgsql.Open(context.Background(), dsn)
	} else {
		raw, err = sqlite.Open(context.Background(), dsn)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw, driver
}

// TestAppDomainsPerServerKeyMigration upgrades an install whose app_domains
// key is the hostname alone: every row survives, and afterwards a second
// server may index the same hostname while one server still may not.
func TestAppDomainsPerServerKeyMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	raw, driver := openRaw(t)
	if err := migrate.MigrateTo(ctx, raw, driver, 20261010000001); err != nil {
		t.Fatal(err)
	}

	const srvA, srvB = "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a01", "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a02"
	const appA, appB = "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a11", "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a12"
	insert := func(domain, app, srv string) error {
		_, err := raw.ExecContext(ctx, dbtest.Rebind(driver, `INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at)
			VALUES (?, ?, ?, 'route', '2026-10-10 12:00:00')`), domain, app, srv)
		return err
	}
	for _, d := range []string{"keep.test", "also.test"} {
		if err := insert(d, appA, srvA); err != nil {
			t.Fatal(err)
		}
	}
	if err := insert("keep.test", appB, srvB); err == nil {
		t.Fatal("the old key accepted a hostname twice; the test does not start from the old schema")
	}

	if err := migrate.Migrate(ctx, raw, driver); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var n int
	if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM app_domains`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows after migration = %d, %v; want 2", n, err)
	}
	if err := insert("keep.test", appB, srvB); err != nil {
		t.Fatalf("second server cannot index the same hostname: %v", err)
	}
	if err := insert("keep.test", appB, srvA); err == nil {
		t.Fatal("one server indexed a hostname twice")
	}
}

// TestAppDomainsUUIDMigrationSurvivesJunkRows replays the app_domains UUID
// migration over an install whose ids are CHAR(36) and include rows that
// are not UUIDs: the migration must drop them instead of aborting startup.
func TestAppDomainsUUIDMigrationSurvivesJunkRows(t *testing.T) {
	if dialect(t) != "postgres" {
		t.Skip("the UUID retype is Postgres-only")
	}
	t.Parallel()
	ctx := context.Background()
	raw, driver := openRaw(t)
	if err := migrate.MigrateTo(ctx, raw, driver, 20260711000001); err != nil {
		t.Fatal(err)
	}
	const good, other = "0192A3B4-C5D6-7E8F-9A0B-1C2D3E4F5A6B", "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a6c"
	if _, err := raw.ExecContext(ctx, `INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at) VALUES
		('keep.test', '`+good+`', '`+other+`', 'route', now()),
		('bad-app.test', 'not-a-uuid', '`+other+`', 'route', now()),
		('bad-server.test', '`+good+`', 'also-junk', 'route', now())`); err != nil {
		t.Fatal(err)
	}

	if err := migrate.Migrate(ctx, raw, driver); err != nil {
		t.Fatalf("migration aborted on junk rows: %v", err)
	}
	rows, err := raw.QueryContext(ctx, `SELECT domain FROM app_domains ORDER BY domain`)
	if err != nil {
		t.Fatal(err)
	}
	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		domains = append(domains, d)
	}
	rows.Close()
	if strings.Join(domains, ",") != "keep.test" {
		t.Fatalf("rows after migration = %v, want only keep.test", domains)
	}
	var typ string
	if err := raw.QueryRowContext(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'app_domains' AND column_name = 'app_id'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if typ != "uuid" {
		t.Fatalf("app_id type = %s, want uuid", typ)
	}
}
```

- [ ] **Step 4: Delete Bun**

```bash
git rm internal/infra/db/bun.go internal/infra/db/bootstrap.go internal/infra/db/bun_test.go \
  internal/infra/db/migration_atomic_test.go internal/infra/db/freeze_dump_test.go \
  internal/infra/db/models/models.go internal/infra/db/migrations/migrations.go \
  internal/infra/db/migrations/2*.go \
  internal/infra/db/repository/user.go internal/infra/db/repository/user_test.go \
  internal/infra/db/repository/server.go internal/infra/db/repository/server_test.go \
  internal/infra/db/repository/app.go internal/infra/db/repository/app_test.go \
  internal/infra/db/repository/app_domain.go internal/infra/db/repository/app_domain_test.go \
  internal/infra/db/repository/server_domain.go internal/infra/db/repository/server_domain_test.go
go mod tidy
git diff go.mod
grep -rn 'uptrace\|BunConnection\|infra/db/models\|sqliteshim\|pgdriver' --include='*.go' . ; echo "grep exit=$?"
```

Expected: `go.mod` loses the six `github.com/uptrace/bun*` requirements and the indirect `fatih/color`, `jinzhu/inflection`, `mattn/go-sqlite3`, `puzpuzpuz/xsync/v3`, `tmthrgd/go-hex`, `vmihailenco/msgpack/v5`, `vmihailenco/tagparser/v2`, `mellium.im/sasl`; nothing is added. The grep prints nothing (`grep exit=1`). (`TestAppDomainsKeyRebuildIsAtomic`'s guarantee lives on as Task 2's `TestAppDomainsRebuildIsAtomic`.)

- [ ] **Step 5: Run everything on SQLite, as CI does**

Run: `gofmt -l $(git ls-files '*.go') && go vet ./... && WF_CONTRACT_GUARD=require make test`
Expected: no gofmt output; every package `ok` (the contract package with its route guard; `internal/infra/db/repository/user` is slow under `-race`, ~50 s).

- [ ] **Step 6: Run on Postgres**

Run: `WF_CONTRACT_GUARD=require make test-pg`
Expected: `ok` for `internal/app/web/contract` (including `TestParity` against the unchanged `testdata/parity.golden.json`, `TestAppDomainsPerServerKeyMigration`, `TestAppDomainsUUIDMigrationSurvivesJunkRows`) and every `internal/infra/db/...` package.

- [ ] **Step 7: Coverage gates with the Postgres profile merged**

```bash
make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out'
tail -n +2 contract-postgres.out >> coverage.out
make web-test
go run ./tools/diffcov --base main --min 80 --go coverage.out --lcov web/coverage/lcov.info
go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info
```

Expected (plan-time numbers on the finished branch): diff gate `go … (86.5%)  PASS`; ratchet `go` ≥ 75.2 `PASS`. (`coverage.out` is the profile Step 5's `make test` wrote.) If CI later prints `::notice::raise coverage-baseline.json go to …`, bump `coverage-baseline.json` in the same PR.

- [ ] **Step 8: Commit**

```bash
git add -A internal go.mod go.sum
git commit -m "refactor(db): wire the sqlc repositories and remove Bun

db.Open replaces NewBunConnection in both topologies; WireCore builds the
five sqlc repositories for the connection's driver. Bun, its models, Go
migrations and flat repositories are gone; the contract upgrade tests
build their before-schema with migrate.MigrateTo. A Bun-era database is
adopted on first boot (its bun_* tables stay until the next migration).

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Drop the Bun tables, sqlc CI job, docs

**Files:**
- Create: `internal/infra/db/migrations/sqlite/20261011000001_drop_bun_tables.sql`, `internal/infra/db/migrations/pgsql/20261011000001_drop_bun_tables.sql`
- Test: `internal/infra/db/migrate/adopt_test.go`
- Modify: `.github/workflows/ci.yml` (new `sqlc` job), `.env.example`, `CLAUDE.md`, `AGENTS.md`, `MIGRATION.md`, `.goreleaser.yaml`

**Interfaces:**
- Consumes: Task 2's runner and adoption tests, Task 3's `make sqlc-check`.
- Produces: migration version `20261011000001` on both engines; CI job `sqlc`.

After this commit a Bun build can no longer run on a database this build has opened (it would find no `bun_migrations` and re-run every migration): **downgrade is unsupported**; state it in the PR description and release notes (Step 8).

- [ ] **Step 1: Assert the Bun tables are gone after adoption**

Replace `internal/infra/db/migrate/adopt_test.go` with:

```go
package migrate

import (
	"context"
	"reflect"
	"testing"

	"winterflow/internal/infra/db/backend"
)

const (
	frozenOrg   = "0192a3b4-0000-7000-8000-000000000001"
	frozenToken = "0192a3b4-0000-7000-8000-000000000006"
)

// TestAdoptBunDatabase loads a frozen Bun-era database (every migration
// applied, or only those up to 20260711000001), migrates it, and checks
// that adoption recorded every version, re-ran nothing Bun had applied
// (seeded rows survive, the PAT table was not recreated), and left the
// same schema a fresh install has.
func TestAdoptBunDatabase(t *testing.T) {
	for _, d := range engines(t) {
		for _, dump := range []string{"bun_full.sql", "bun_20260711000001.sql"} {
			t.Run(d+"/"+dump, func(t *testing.T) {
				ctx := context.Background()
				fresh := openEmpty(t, d)
				if err := Migrate(ctx, fresh, d); err != nil {
					t.Fatal(err)
				}

				db := openEmpty(t, d)
				loadDump(t, db, d, dump)
				if err := Migrate(ctx, db, d); err != nil {
					t.Fatal(err)
				}
				if got, want := versions(t, db), versions(t, fresh); !reflect.DeepEqual(got, want) {
					t.Fatalf("versions = %v, want %v", got, want)
				}
				if n := count(t, db, d, `SELECT count(*) FROM organizations WHERE organization_id = ? AND name = 'frozen org' AND icon = 'server'`, frozenOrg); n != 1 {
					t.Fatal("seeded organization lost or altered")
				}
				if n := count(t, db, d, `SELECT count(*) FROM user_tokens WHERE token_id = ? AND name = 'ci; deploy'`, frozenToken); n != 1 {
					t.Fatal("seeded token lost: a Bun migration re-ran")
				}
				if n := count(t, db, d, `SELECT count(*) FROM app_domains`); n != 2 {
					t.Fatalf("app_domains rows = %d, want 2", n)
				}
				if n := count(t, db, d, bunTablesSQL(d)); n != 0 {
					t.Fatalf("%d bun_* tables survived drop_bun_tables", n)
				}
				// Idempotent: a second boot adopts nothing and applies nothing.
				if err := Migrate(ctx, db, d); err != nil {
					t.Fatalf("second Migrate: %v", err)
				}
				sameLines(t, "adopted schema vs fresh install", schemaOf(t, db, d), schemaOf(t, fresh, d))
			})
		}
	}
}

func bunTablesSQL(d string) string {
	if d == backend.Postgres {
		return `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name LIKE 'bun\_%'`
	}
	return `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'bun\_%' ESCAPE '\'`
}

// A build that adopted bun_migrations but crashed before drop_bun_tables
// leaves both tables: the next boot must not adopt again (schema_migrations
// wins) and finishes the pending migrations.
func TestAdoptionIsNotRepeated(t *testing.T) {
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := context.Background()
			db := openEmpty(t, d)
			loadDump(t, db, d, "bun_20260711000001.sql")
			if err := MigrateTo(ctx, db, d, 20260711000001); err != nil {
				t.Fatal(err)
			}
			if got := versions(t, db); len(got) != 5 {
				t.Fatalf("adopted versions = %v, want the 5 Bun applied", got)
			}
			// Bun's bookkeeping would now claim a version this build has
			// not run; a repeated adoption would copy it and skip it.
			mustExec(t, db, d, `INSERT INTO bun_migrations (name, group_id) VALUES ('20261010000003', 2)`)
			if err := Migrate(ctx, db, d); err != nil {
				t.Fatal(err)
			}
			if n := count(t, db, d, tableCountSQL(d), "server_domains"); n != 1 {
				t.Fatal("server_domains missing: adoption ran twice and skipped its migration")
			}
		})
	}
}
```

Run: `go test -count=1 ./internal/infra/db/migrate/ -run TestAdoptBunDatabase`
Expected: `FAIL` — `2 bun_* tables survived drop_bun_tables` for both dumps.

- [ ] **Step 2: Add the migration on both engines**

`internal/infra/db/migrations/sqlite/20261011000001_drop_bun_tables.sql` and `internal/infra/db/migrations/pgsql/20261011000001_drop_bun_tables.sql` (identical):

```sql
-- Bun's bookkeeping is superseded by schema_migrations (adopted from it on
-- first boot of this build). A Bun build can no longer run on this database.

DROP TABLE IF EXISTS bun_migrations;
DROP TABLE IF EXISTS bun_migration_locks;
```

Run: `go test -count=1 ./internal/infra/db/migrate/ && make test-pg TEST_PG_ARGS='-run "TestAdopt|TestMigrate|TestConcurrent|TestEngines" -v' 2>&1 | grep -E '^(---|ok|FAIL)'`
Expected: `ok` on SQLite; on Postgres every listed test passes (`TestAdoptBunDatabase/postgres/...` included) and `TestEnginesShareVersions` passes (nine versions on each engine).

Run: `make sqlc-check; echo exit=$?`
Expected: `exit=0` (dropping tables sqlc never knew changes no generated code).

- [ ] **Step 3: CI job for stale generated code**

In `.github/workflows/ci.yml`, add after the `installer` job:

```yaml
  # Generated sqlc code (internal/infra/db/sqlc/gen) must match the queries
  # and migrations it is built from.
  sqlc:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: make sqlc-check
        run: make sqlc-check
```

- [ ] **Step 4: `.env.example`**

Replace

```
# Set to "true" to log every SQL statement (bundebug). Very noisy; dev only.
#DEBUG_SQL=false
```

with

```
# Set to "true" to log every SQL statement with its duration through the app
# logger (statement text only, never the arguments). Read by standalone and
# api. Very noisy; dev only.
#DEBUG_SQL=false
```

- [ ] **Step 5: Docs**

`CLAUDE.md`, Commands: replace `` `make sqlc` regenerates SQL (note: no `sqlc.yaml` checked in yet).`` at the end of the `make grpc` bullet with nothing, and add a bullet after it:

```markdown
- `make sqlc` — regenerate `internal/infra/db/sqlc/gen` (packages `sqlitegen`, `pgsqlgen`) from the per-engine queries and migrations; sqlc is pinned and run through `go run` (needs a C compiler). `make sqlc-check` fails when the committed code is stale (CI's `sqlc` job).
```

`CLAUDE.md`, Architecture, **Layering** paragraph: replace the sentence ``Persistence uses Bun ORM (`internal/infra/db`, SQLite or Postgres via `DATABASE_URL`); migrations live in `internal/infra/db/migrations` and run on startup.`` with:

```markdown
Persistence is sqlc over `database/sql` (`internal/infra/db`, econumo's pattern), SQLite (`modernc.org/sqlite`) or Postgres (`pgx/v5`, simple protocol, session pinned to UTC) chosen by the `DATABASE_URL` scheme in `db.Open`, which also runs the forward-only migrations (`internal/infra/db/migrations/{sqlite,pgsql}/<version>_<name>.sql`; each runs in one transaction with its `schema_migrations` row, Postgres runs are serialized by an advisory lock, and a Bun-era `bun_migrations` table is adopted once). SQL is written per engine, never in Go: each repository (`internal/infra/db/repository/<aggregate>/`) has `repo.go` (port methods + hydrate against `sqlitegen` row types behind a private `querier`), `sqlite.go` (passthrough) and `pgsql.go` (struct conversion from the identically shaped `pgsqlgen` types). Transactions: `Tx.WithTx(ctx, fn)` (nested calls use savepoints); inside `fn` every query must use `Tx.Querier(ctx)` with `fn`'s ctx — SQLite has a single connection, so the outer ctx deadlocks. **Adding a query:** the same `-- name:` in `internal/infra/db/sqlc/query/sqlite/<file>.sql` and `query/pgsql/<file>.sql`, `make sqlc`, then the method on the repo's `querier` and both shims (`shapes_test.go` fails to compile if the two engines' types differ). **Adding a migration:** the same `<version>_<name>.sql` in `migrations/sqlite` and `migrations/pgsql` (comment-only when one engine has nothing to do; `-- migrate:foreign_keys=off` as the first line for a SQLite table rebuild), then `make sqlc`. Keep `.sql` files ASCII; never edit `sqlc/gen`. Repository tests use `dbtest.Open(t)` and run on Postgres under `make test-pg`.
```

`AGENTS.md`, Architecture: after the "Layering is hexagonal …" paragraph add:

```markdown
**Persistence** is sqlc (`internal/infra/db`): per-engine SQL in `internal/infra/db/sqlc/query/{sqlite,pgsql}` and `internal/infra/db/migrations/{sqlite,pgsql}` (same file names and versions on both engines), generated packages `sqlitegen`/`pgsqlgen` (`make sqlc`, never edited by hand), and one package per repository under `internal/infra/db/repository/` (`repo.go` + `sqlite.go` + `pgsql.go`). Dialect differences live only in the `.sql` files. Migrations are forward-only and run on startup from `db.Open`.
```

`AGENTS.md`, Build/Test commands: replace the `make test-pg` bullet with

```markdown
- `make test-pg` – the API contract suite (`internal/app/web/contract`) and the `internal/infra/db` packages (migration runner, repositories) against a throwaway `postgres:16` container (Docker); plain `go test ./...` runs them on SQLite. Extra flags: `make test-pg TEST_PG_ARGS='-run TestParity -v'`.
- `make sqlc` – regenerates `internal/infra/db/sqlc/gen` from the queries and migrations; `make sqlc-check` (CI) fails when it is stale.
```

`AGENTS.md`, Testing Guidelines: in the CI sentence replace `` the contract suite on SQLite (inside `go test`) and on Postgres (`contract` job, whose coverage is merged into the gates)`` with `` the contract suite on SQLite (inside `go test`) and, with the `internal/infra/db` packages, on Postgres (`contract` job, whose coverage is merged into the gates), `make sqlc-check` ``; in the exemptions list add `` `internal/infra/db/sqlc/gen/` (also left out of the ratchet total) `` after `` `*.pb.go` ``.

`MIGRATION.md`: replace

```
- **Persistence:** `internal/infra/db/{models,migrations,repository}` (Bun;
  SQLite or Postgres via `DATABASE_URL`).
```

with

```
- **Persistence:** `internal/infra/db/{migrations,migrate,sqlc,repository}`
  (sqlc; SQLite or Postgres via `DATABASE_URL`).
```

`.goreleaser.yaml`: replace

```
# CGO is off so cross-compilation works everywhere; the sqliteshim driver then
# falls back to the pure-Go modernc.org/sqlite automatically.
```

with

```
# CGO is off so cross-compilation works everywhere; SQLite is the pure-Go
# modernc.org/sqlite driver.
```

- [ ] **Step 6: Full verification**

```bash
gofmt -l $(git ls-files '*.go')
go vet ./...
WF_CONTRACT_GUARD=require make test
WF_CONTRACT_GUARD=require make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out'
make sqlc-check
tail -n +2 contract-postgres.out >> coverage.out
make web-test
go run ./tools/diffcov --base main --min 80 --go coverage.out --lcov web/coverage/lcov.info
go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info
bash scripts/install_test.sh
```

Expected: no gofmt output; vet clean; every package `ok` on SQLite and Postgres (`TestParity` unchanged); `make sqlc-check` exit 0; diff gate `PASS` (plan time: 86.5%); ratchet `PASS` (plan time: go 75.7% SQLite-only, higher merged; baseline 75.2); installer suite unchanged. `make web-test` needs `pnpm --dir web install` once.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db/migrations internal/infra/db/migrate/adopt_test.go \
  .github/workflows/ci.yml .env.example CLAUDE.md AGENTS.md MIGRATION.md .goreleaser.yaml
git commit -m "feat(db)!: drop Bun's bookkeeping tables; sqlc CI job and docs

Migration 20261011000001 drops bun_migrations and bun_migration_locks once
schema_migrations has adopted them. CI checks the generated sqlc code;
docs describe the sqlc pattern and how to add a query or a migration;
DEBUG_SQL now logs statements and durations through the app logger.

BREAKING CHANGE: a database opened by this build cannot be used by an
earlier (Bun-based) build any more; downgrading is unsupported.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 8: PR description notes (PR #11)**

Add to the PR description (the user commits/edits it; do not push):

```markdown
**Upgrade:** on first boot an existing database's `bun_migrations` is adopted into `schema_migrations` (no migration re-runs), pending migrations apply, then the Bun tables are dropped. **Downgrade to a Bun-based build is unsupported** once this build has run (it would re-run every migration).

**Postgres behind PgBouncer:** queries use pgx's simple protocol (no server-side prepared statements), but startup migrations hold a session-level `pg_advisory_lock`; point `DATABASE_URL` at Postgres directly or use session pooling for the API.

Suites run: `WF_CONTRACT_GUARD=require make test`, `make test-pg` (contract suite + `./internal/infra/db/...`, Postgres 16), `make sqlc-check`, diff coverage with the Postgres profile merged, `bash scripts/install_test.sh`.
```

---

## Self-review

**Spec coverage** (spec section → task):

| Spec section | Where |
|---|---|
| Chosen approach (two generated packages, private `querier`, SQLite passthrough, Postgres struct conversion, engine chosen in the constructor) | Tasks 3, 5–9 |
| Layout (`db.go`, `backend/`, `sqlite/`, `pgsql/`, `migrate/`, `migrations/`, `sqlc/`, `types/`, `dbtest/`, `repository/<aggregate>/`; deletions) | File Structure; Tasks 2–10 |
| Drivers and connections (modernc, pgx stdlib, pragmas, pools, `timezone=UTC`, 10 s ping, scheme selection, `DEBUG_SQL` wrapper, `BUNDEBUG` dropped) | Task 4; `.env.example` Task 11 |
| sqlc configuration (two targets, options, overrides, pinned `go run`, `make sqlc`, `make sqlc-check`, `ascii_test.go`) | Task 3 |
| Runner (embed, identical versions test, `schema_migrations`, one transaction per migration with its version row, splitter, `foreign_keys=off` + `foreign_key_check`, forward-only, boot from `db.Open`, advisory lock, `MigrateTo`) | Task 2; `db.Open` Task 4 |
| Ported migrations (eight, same versions; `drop_bun_tables`) | Task 2; Task 11 |
| Adopting existing databases (copy applied versions in one transaction, `migrated_at` filter or all rows, unknown name fails, partial installs resume, downgrade unsupported) | Task 2; Task 11 (drop, PR note) |
| TxManager (`WithTx`, savepoints `sp_N`, panic rollback, `Querier`) | Task 4 |
| Repository package shape, `sql.ErrNoRows` → domain errors | Tasks 5–9 |
| Dialect differences in SQL (`FOR UPDATE` only in pgsql; `?` vs `$N`; no dynamic SQL) | Task 3; Task 9; Decisions 3–4 |
| Wiring (`db.Open`, five repos with `New(d.Tx, d.Driver, log)`, health check/shutdown on `*sql.DB`) | Task 10 (`newRepos`); Task 4 (`HealthCheck`, `Shutdown`) |
| Testing: contract suite unchanged on both engines; `migration_test.go` via `MigrateTo`; ported repository tests; `dbtest.Open`; test-pg/contract job run `./internal/infra/db/...`; adoption tests with frozen dumps; runner unit tests; TxManager tests on both engines; ASCII test | Tasks 10, 5–9, 4, 2, 1–2 + 11, 2, 4, 3 |
| Coverage exclusion of `internal/infra/db/sqlc/gen/**` | Task 3 (`tools/diffcov`) |
| CI (`sqlc` job; `contract` job runs DB packages) | Task 11; Task 2 |
| Docs and config (CLAUDE.md, AGENTS.md recipe, `.env.example`, Makefile, `go.mod`) | Task 11; Task 3; Tasks 2, 10 |
| Delivery order 1–7 | Tasks 1, 2, 3, 4, 5–9, 10, 11 |
| Risks (schema drift, timestamp drift, sqlc quirks) | Task 2 schema comparison; Review Focus 2; Task 3 overrides + `shapes_test.go` + ASCII guard |

**Placeholder scan:** no "TBD"/"TODO"/"similar to Task N"; every file is given in full or as an exact old → new replacement; every run step names the command and the expected result.

**Type and signature consistency** (checked across tasks): `backend.SQLite`/`backend.Postgres` are the only driver strings (`db.DB.Driver`, `migrate.*`, `migrations.FS`, every `New`); `migrate.MigrateTo(ctx, *sql.DB, driver string, version int64)` is called with `int64` literals in Tasks 2 and 10; `db.Open(log, url) (*db.DB, error)` everywhere (bootstrap, contract harness, handler test, `dbtest.Open`); `*db.DB` fields `SQL`, `Driver`, `Tx`; `TxManager.WithTx(ctx, func(ctx context.Context) error) error` and `Querier(ctx) backend.DBTX`; repository constructors `New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error)` in all five packages; `WireCore` returns `*serverrepo.Repo`, which `publishEmbeddedCapabilities` takes; `dbtest.Exec(t, *db.DB, query, args...)` is what the contract harness calls; generated type names in Task 3's Interfaces match the aliases in each `repo.go`.
