// Package migrate is winterflow's forward-only migration runner. Migrations
// are per-engine SQL files (internal/infra/db/migrations/{sqlite,pgsql}),
// named <version>_<name>.sql with a 4-digit sequence version (0001, 0002,
// ...). Each pending one runs in one transaction
// together with the insert of its version into schema_migrations, so a
// failure leaves neither its effects nor its bookkeeping behind.
//
// A database last run by a Bun-era build (bun_migrations present,
// schema_migrations absent) is adopted first: the migrations Bun applied are
// recorded in schema_migrations under their sequence versions (bunVersions)
// without re-running them.
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
	"time"

	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/migrations"
)

// fkOffMarker, as the first line of a SQLite migration, marks a table
// rebuild: it runs with foreign keys off and must not add any PRAGMA
// foreign_key_check violation before it commits (violations already in the
// database before it ran are tolerated).
const fkOffMarker = "-- migrate:foreign_keys=off"

// pgLockKey serializes concurrent runners (several API replicas booting at
// once) on one Postgres database: pg_advisory_lock("wfmigrat").
const pgLockKey int64 = 0x77666d6967726174

// unlockTimeout bounds the release of the advisory lock.
const unlockTimeout = 10 * time.Second

// bunVersions maps the timestamp version of every migration Bun-era builds
// shipped to the sequence version of its SQL port. Frozen: Bun is gone, so
// no entry is ever added.
var bunVersions = map[int64]int64{
	20241113120001: 1, // initial_schema
	20260703000001: 2, // pat_tokens
	20260703000002: 3, // user_credentials
	20260703000003: 4, // org_identity
	20260711000001: 5, // app_domains
	20261010000001: 6, // app_domains_uuid
	20261010000002: 7, // app_domains_per_server
	20261010000003: 8, // server_domains
}

// Migration is one parsed migration file.
type Migration struct {
	Version int64
	Name    string // file name without .sql, e.g. 0001_initial_schema
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
			// Unlock even when ctx is already cancelled, but never wait on a
			// dead connection forever.
			uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
			defer cancel()
			var released bool
			uerr := c.QueryRowContext(uctx, `SELECT pg_advisory_unlock($1)`, pgLockKey).Scan(&released)
			if uerr == nil && !released {
				// false: this session did not hold the lock, so the run
				// was not serialized as assumed; treat it like a failed
				// unlock.
				uerr = errors.New("lock was not held by this session")
			}
			if uerr != nil {
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
// present, schema_migrations absent) it first records every migration Bun
// applied under its sequence version (bunVersions), in the same transaction.
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
		bv, err := bunVersion(name)
		if err != nil {
			return err
		}
		v, ok := bunVersions[bv]
		if !ok || !known[v] {
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
	var baseline map[fkViolation]bool
	if m.FKOff {
		// Orphans that predate the run (a Bun-era build lost foreign_keys=ON
		// whenever its pool replaced the connection) are not this rebuild's
		// doing and must not block it; only new ones fail the check.
		vs, err := foreignKeyViolations(ctx, tx)
		if err != nil {
			return fmt.Errorf("migrate: %s: %w", m.Name, err)
		}
		baseline = make(map[fkViolation]bool, len(vs))
		for _, v := range vs {
			baseline[v] = true
		}
	}
	for _, stmt := range Split(m.SQL) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %s: %w\n--- statement ---\n%s", m.Name, err, stmt)
		}
	}
	if m.FKOff {
		if err := foreignKeyCheck(ctx, tx, baseline); err != nil {
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

// fkViolation is one row of PRAGMA foreign_key_check: the child table, the
// child row's rowid (NULL for a WITHOUT ROWID table), the parent table and
// the index of the violated foreign key.
type fkViolation struct {
	table, rowid, parent, fkid string
}

// foreignKeyViolations lists what PRAGMA foreign_key_check reports. An error
// running the pragma itself (e.g. "foreign key mismatch": a parent key that
// is no longer unique) is returned as is.
func foreignKeyViolations(ctx context.Context, tx *sql.Tx) ([]fkViolation, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return nil, fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()
	var out []fkViolation
	for rows.Next() {
		var table, parent string
		var rowid, fkid any
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return nil, fmt.Errorf("foreign_key_check: %w", err)
		}
		out = append(out, fkViolation{table: table, rowid: fmt.Sprint(rowid), parent: parent, fkid: fmt.Sprint(fkid)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("foreign_key_check: %w", err)
	}
	return out, nil
}

// foreignKeyCheck fails when PRAGMA foreign_key_check reports a row that is
// not in baseline (the violations present before the migration ran), or
// cannot run at all.
func foreignKeyCheck(ctx context.Context, tx *sql.Tx, baseline map[fkViolation]bool) error {
	vs, err := foreignKeyViolations(ctx, tx)
	if err != nil {
		return err
	}
	var broken []string
	for _, v := range vs {
		if baseline[v] {
			continue
		}
		broken = append(broken, fmt.Sprintf("%s(rowid %s) -> %s", v.table, v.rowid, v.parent))
	}
	if len(broken) > 0 {
		return errors.New("foreign_key_check failed: " + strings.Join(broken, ", "))
	}
	return nil
}
