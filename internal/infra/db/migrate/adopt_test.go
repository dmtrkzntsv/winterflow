package migrate

import (
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
				ctx := tctx(t)
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
			ctx := tctx(t)
			db := openEmpty(t, d)
			loadDump(t, db, d, "bun_20260711000001.sql")
			if err := MigrateTo(ctx, db, d, 5); err != nil {
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
