package migrate

import (
	"fmt"
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

// TestVersionsAreASequence pins the naming scheme: 0001_<name>.sql,
// 0002_<name>.sql, ... with no gaps, on both engines.
func TestVersionsAreASequence(t *testing.T) {
	for _, d := range []string{backend.SQLite, backend.Postgres} {
		migs := load(t, d)
		for i, m := range migs {
			if want := fmt.Sprintf("%04d_", i+1); m.Version != int64(i+1) || !strings.HasPrefix(m.Name, want) {
				t.Errorf("%s: migration %d is %s (version %d), want %s<name>", d, i, m.Name, m.Version, want)
			}
		}
	}
}

// TestBunVersionsMapToTheirPorts checks bunVersions against the Bun-era
// migration names: each one must land on the SQL file that ports it.
func TestBunVersionsMapToTheirPorts(t *testing.T) {
	ports := map[int64]string{
		20241113120001: "0001_initial_schema",
		20260703000001: "0002_pat_tokens",
		20260703000002: "0003_user_credentials",
		20260703000003: "0004_org_identity",
		20260711000001: "0005_app_domains",
		20261010000001: "0006_app_domains_uuid",
		20261010000002: "0007_app_domains_per_server",
		20261010000003: "0008_server_domains",
	}
	if len(bunVersions) != len(ports) {
		t.Fatalf("bunVersions has %d entries, want %d", len(bunVersions), len(ports))
	}
	for _, d := range []string{backend.SQLite, backend.Postgres} {
		names := map[int64]string{}
		for _, m := range load(t, d) {
			names[m.Version] = m.Name
		}
		for bun, want := range ports {
			if got := names[bunVersions[bun]]; got != want {
				t.Errorf("%s: Bun %d maps to %q, want %q", d, bun, got, want)
			}
		}
	}
}

func load(t *testing.T, driver string) []Migration {
	t.Helper()
	fsys, err := migrations.FS(driver)
	if err != nil {
		t.Fatal(err)
	}
	migs, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return migs
}

func TestFSRejectsUnknownDriver(t *testing.T) {
	if _, err := migrations.FS("mysql"); err == nil {
		t.Fatal("unknown driver accepted")
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

func TestRunRejectsUnknownDriver(t *testing.T) {
	if err := Run(tctx(t), nil, "mysql", fstest.MapFS{}, -1); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestMigrateFreshIsIdempotent(t *testing.T) {
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := tctx(t)
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
			ctx := tctx(t)
			db := openEmpty(t, d)
			if err := MigrateTo(ctx, db, d, 5); err != nil {
				t.Fatal(err)
			}
			got := versions(t, db)
			if got[len(got)-1] != 5 {
				t.Fatalf("versions = %v", got)
			}
			if err := MigrateTo(ctx, db, d, 999); err == nil {
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
			ctx := tctx(t)
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
	ctx := tctx(t)
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

// An orphan that predates the run (Bun-era installs have them: Bun lost
// foreign_keys=ON whenever its pool replaced the connection) in a table the
// rebuild does not touch must not fail the rebuild, nor be removed by it.
func TestForeignKeysOffRebuildToleratesPreexistingOrphans(t *testing.T) {
	ctx := tctx(t)
	db := openEmpty(t, backend.SQLite)
	loadDump(t, db, backend.SQLite, "bun_20260711000001.sql")
	mustExec(t, db, backend.SQLite, `PRAGMA foreign_keys = OFF`)
	mustExec(t, db, backend.SQLite, `INSERT INTO server_capabilities (server_id, name, value, updated_at) VALUES ('0192a3b4-0000-7000-8000-0000000000ff', 'orphan', 'x', '2026-08-23 10:11:12')`)
	mustExec(t, db, backend.SQLite, `PRAGMA foreign_keys = ON`)

	if err := Migrate(ctx, db, backend.SQLite); err != nil {
		t.Fatalf("a pre-existing orphan failed the migration: %v", err)
	}
	if n := count(t, db, backend.SQLite, `SELECT count(*) FROM server_capabilities WHERE name = 'orphan'`); n != 1 {
		t.Fatalf("orphan rows = %d, want 1", n)
	}
	if n := count(t, db, backend.SQLite, `SELECT count(*) FROM schema_migrations WHERE version = 7`); n != 1 {
		t.Fatal("the app_domains rebuild was not recorded")
	}
}

// A rebuild that adds a violation of its own still fails, even when the
// database already had an unrelated one.
func TestForeignKeysOffRebuildFailsOnNewViolationBesideOldOne(t *testing.T) {
	ctx := tctx(t)
	db := openEmpty(t, backend.SQLite)
	base := fstest.MapFS{
		"1_parent.sql": {Data: []byte("CREATE TABLE parent (id INT PRIMARY KEY);\nCREATE TABLE child (pid INT REFERENCES parent (id));\nCREATE TABLE other (pid INT REFERENCES parent (id));\nINSERT INTO parent VALUES (1);\nINSERT INTO child VALUES (1);")},
	}
	if err := Run(ctx, db, backend.SQLite, base, -1); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, backend.SQLite, `PRAGMA foreign_keys = OFF`)
	mustExec(t, db, backend.SQLite, `INSERT INTO other VALUES (42)`)
	mustExec(t, db, backend.SQLite, `PRAGMA foreign_keys = ON`)

	broken := fstest.MapFS{
		"1_parent.sql":  base["1_parent.sql"],
		"2_rebuild.sql": {Data: []byte(fkOffMarker + "\nCREATE TABLE parent_new (id INT PRIMARY KEY);\nDROP TABLE parent;\nALTER TABLE parent_new RENAME TO parent;")},
	}
	err := Run(ctx, db, backend.SQLite, broken, -1)
	if err == nil || !strings.Contains(err.Error(), "foreign_key_check") || !strings.Contains(err.Error(), "child") {
		t.Fatalf("err = %v, want a foreign_key_check failure naming child", err)
	}
	if strings.Contains(err.Error(), "other") {
		t.Fatalf("err = %v names the pre-existing orphan", err)
	}
	if n := count(t, db, backend.SQLite, `SELECT count(*) FROM parent`); n != 1 {
		t.Fatalf("parent rows after rollback = %d, want 1", n)
	}
	if got := versions(t, db); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("versions = %v", got)
	}
}

func TestForeignKeysOffIsSQLiteOnly(t *testing.T) {
	fsys := fstest.MapFS{"1_x.sql": {Data: []byte(fkOffMarker + "\nSELECT 1;")}}
	if err := Run(tctx(t), nil, backend.Postgres, fsys, -1); err == nil {
		t.Fatal("a Postgres migration with the foreign_keys marker was accepted")
	}
}

// The real app_domains rebuild (0007_app_domains_per_server) under a child table that
// references app_domains(domain): the new key no longer makes domain unique,
// so the check fails and nothing is half-applied.
func TestAppDomainsRebuildIsAtomic(t *testing.T) {
	ctx := tctx(t)
	db := openEmpty(t, backend.SQLite)
	if err := MigrateTo(ctx, db, backend.SQLite, 6); err != nil {
		t.Fatal(err)
	}
	for _, dom := range []string{"keep.test", "also.test"} {
		mustExec(t, db, backend.SQLite, `INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at) VALUES (?, 'a', 's', 'route', '2026-10-10 12:00:00')`, dom)
	}
	mustExec(t, db, backend.SQLite, `CREATE TABLE app_domains_child (d VARCHAR(253) REFERENCES app_domains (domain))`)
	mustExec(t, db, backend.SQLite, `INSERT INTO app_domains_child (d) VALUES ('keep.test')`)

	if err := Migrate(ctx, db, backend.SQLite); err == nil || !strings.Contains(err.Error(), "foreign_key_check") {
		t.Fatalf("err = %v, want a foreign_key_check failure", err)
	}
	if n := count(t, db, backend.SQLite, `SELECT count(*) FROM app_domains`); n != 2 {
		t.Fatalf("app_domains rows = %d, want 2", n)
	}
	if n := count(t, db, backend.SQLite, `SELECT count(*) FROM sqlite_master WHERE name = 'app_domains_new'`); n != 0 {
		t.Fatal("half-built table survived")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at) VALUES ('keep.test', 'b', 's2', 'route', '2026-10-10 12:00:00')`); err == nil {
		t.Fatal("the key changed despite the rollback")
	}
}

func TestAdoptRejectsUnknownBunVersion(t *testing.T) {
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			db := openEmpty(t, d)
			loadDump(t, db, d, "bun_full.sql")
			mustExec(t, db, d, `INSERT INTO bun_migrations (name, group_id) VALUES ('29990101000001', 2)`)
			err := Migrate(tctx(t), db, d)
			if err == nil || !strings.Contains(err.Error(), "29990101000001") {
				t.Fatalf("err = %v, want the unknown Bun version named", err)
			}
			if n := count(t, db, d, tableCountSQL(d), "schema_migrations"); n != 0 {
				t.Fatal("a failed adoption left schema_migrations behind")
			}
		})
	}
}
