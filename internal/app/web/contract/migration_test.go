package contract_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/migrate"
	"winterflow/internal/infra/db/pgsql"
	"winterflow/internal/infra/db/sqlite"
)

// openRaw opens a fresh, unmigrated database of the run's dialect through
// the production opener.
func openRaw(t *testing.T) (context.Context, *sql.DB, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	dsn := freshDB(t)
	var (
		raw    *sql.DB
		err    error
		driver = backend.SQLite
	)
	if dialect(t) == backend.Postgres {
		driver = backend.Postgres
		raw, err = pgsql.Open(ctx, dsn)
	} else {
		raw, err = sqlite.Open(ctx, dsn)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return ctx, raw, driver
}

// TestAppDomainsPerServerKeyMigration upgrades an install whose app_domains
// key is the hostname alone: every row survives, and afterwards a second
// server may index the same hostname while one server still may not.
func TestAppDomainsPerServerKeyMigration(t *testing.T) {
	t.Parallel()
	ctx, raw, driver := openRaw(t)
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
	if dialect(t) != backend.Postgres {
		t.Skip("the UUID retype is Postgres-only")
	}
	t.Parallel()
	ctx, raw, driver := openRaw(t)
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
