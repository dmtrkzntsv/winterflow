package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"

	"winterflow/internal/infra/db/migrations"
	"winterflow/pkg/logger"
)

// TestAppDomainsKeyRebuildIsAtomic: the SQLite rebuild of app_domains runs on
// the production connection (one connection, production pragmas). bun marks a
// migration applied before running it, so a failure mid-rebuild must roll
// back instead of leaving the index table lost or half-built for good.
func TestAppDomainsKeyRebuildIsAtomic(t *testing.T) {
	ctx := context.Background()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	sqldb, err := setupSQLite("sqlite://"+filepath.Join(t.TempDir(), "atomic.sqlite"), log)
	if err != nil {
		t.Fatal(err)
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	t.Cleanup(func() { _ = db.Close() })

	const target = "20261010000002"
	older := migrate.NewMigrations()
	for _, m := range migrations.Migrations.Sorted() {
		if m.Name < target {
			older.Add(m)
		}
	}
	oldMigrator := migrate.NewMigrator(db, older)
	if err := oldMigrator.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := oldMigrator.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"keep.test", "also.test"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at)
			VALUES (?, 'a', 's', 'route', '2026-10-10 12:00:00')`, d); err != nil {
			t.Fatal(err)
		}
	}
	// A child row referencing app_domains makes DROP TABLE fail (foreign keys
	// are ON), after the replacement table was already created and filled.
	for _, q := range []string{
		`CREATE TABLE app_domains_child (d VARCHAR(253) REFERENCES app_domains (domain))`,
		`INSERT INTO app_domains_child (d) VALUES ('keep.test')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := migrate.NewMigrator(db, migrations.Migrations).Migrate(ctx); err == nil {
		t.Fatal("migration succeeded although DROP TABLE must fail")
	}

	var n int
	if err := db.NewRaw(`SELECT count(*) FROM app_domains`).Scan(ctx, &n); err != nil || n != 2 {
		t.Fatalf("app_domains after a failed rebuild: %d rows, %v; want the original 2", n, err)
	}
	var leftover int
	if err := db.NewRaw(`SELECT count(*) FROM sqlite_master WHERE name = 'app_domains_new'`).Scan(ctx, &leftover); err != nil || leftover != 0 {
		t.Fatalf("half-built table survived the rollback: %d, %v", leftover, err)
	}
	// Still the old key: nothing was half-applied.
	if _, err := db.ExecContext(ctx, `INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at)
		VALUES ('keep.test', 'b', 's2', 'route', '2026-10-10 12:00:00')`); err == nil {
		t.Fatal("the key changed despite the rollback")
	}
}
