package contract_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"

	"winterflow/internal/infra/db/migrations"
)

// TestAppDomainsUUIDMigrationSurvivesJunkRows replays the app_domains UUID
// migration over an install whose ids are CHAR(36) and include a row that is
// not a UUID: the migration must drop it instead of aborting startup.
func TestAppDomainsUUIDMigrationSurvivesJunkRows(t *testing.T) {
	if dialect() != "postgres" {
		t.Skip("the UUID retype is Postgres-only")
	}
	t.Parallel()
	ctx := context.Background()
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(freshDB(t))))
	t.Cleanup(func() { _ = sqldb.Close() })
	db := bun.NewDB(sqldb, pgdialect.New())
	m := migrate.NewMigrator(db, migrations.Migrations)
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	const good, other = "0192A3B4-C5D6-7E8F-9A0B-1C2D3E4F5A6B", "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a6c"
	for _, q := range []string{
		// Rewind to the pre-migration shape and mark it unapplied.
		`ALTER TABLE app_domains ALTER COLUMN app_id TYPE CHAR(36) USING app_id::text, ALTER COLUMN server_id TYPE CHAR(36) USING server_id::text`,
		`DELETE FROM bun_migrations WHERE name = '20261010000001'`,
		`INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at) VALUES
			('keep.test', '` + good + `', '` + other + `', 'route', now()),
			('bad-app.test', 'not-a-uuid', '` + other + `', 'route', now()),
			('bad-server.test', '` + good + `', 'also-junk', 'route', now())`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	if _, err := m.Migrate(ctx); err != nil {
		t.Fatalf("migration aborted on junk rows: %v", err)
	}
	var domains []string
	if err := db.NewRaw(`SELECT domain FROM app_domains ORDER BY domain`).Scan(ctx, &domains); err != nil {
		t.Fatal(err)
	}
	if len(domains) != 1 || domains[0] != "keep.test" {
		t.Fatalf("rows after migration = %v, want only keep.test", domains)
	}
	var typ string
	if err := db.NewRaw(`SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'app_domains' AND column_name = 'app_id'`).Scan(ctx, &typ); err != nil {
		t.Fatal(err)
	}
	if typ != "uuid" {
		t.Fatalf("app_id type = %s, want uuid", typ)
	}
}
