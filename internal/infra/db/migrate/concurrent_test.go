package migrate

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"winterflow/internal/infra/db/backend"
)

// Several API replicas booting at once against one Postgres: the advisory
// lock serializes them, so every runner succeeds and each version is
// applied once.
func TestConcurrentMigrateIsSerialized(t *testing.T) {
	base := os.Getenv("WF_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("Postgres only (make test-pg)")
	}
	ctx := tctx(t)
	first := openEmpty(t, backend.Postgres)
	var schema string
	if err := first.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn := schemaDSN(t, base, schema)

	const replicas = 4
	var wg sync.WaitGroup
	errs := make([]error, replicas)
	for i := range replicas {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = Migrate(ctx, db, backend.Postgres)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d: %v", i, err)
		}
	}
	fresh := openEmpty(t, backend.Postgres)
	if err := Migrate(ctx, fresh, backend.Postgres); err != nil {
		t.Fatal(err)
	}
	if got, want := versions(t, first), versions(t, fresh); !reflect.DeepEqual(got, want) {
		t.Fatalf("versions = %v, want %v", got, want)
	}
}

// A Run that fails while holding the advisory lock must release it: a
// second pool (another replica) migrates straight away while the first
// pool, whose session may be reused, is still open.
func TestFailedRunReleasesAdvisoryLock(t *testing.T) {
	base := os.Getenv("WF_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("Postgres only (make test-pg)")
	}
	ctx := tctx(t)
	a := openEmpty(t, backend.Postgres)
	var schema string
	if err := a.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	broken := fstest.MapFS{"1_broken.sql": {Data: []byte("INSERT INTO missing VALUES (1);")}}
	if err := Run(ctx, a, backend.Postgres, broken, -1); err == nil {
		t.Fatal("broken migration succeeded")
	}

	b, err := sql.Open("pgx", schemaDSN(t, base, schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if err := Migrate(ctx, b, backend.Postgres); err != nil {
		t.Fatalf("second pool blocked or failed after a failed run: %v", err)
	}
}

// pg_advisory_unlock returning false (the session no longer held the lock,
// here because a migration released it itself) is reported, not ignored.
func TestUnlockNotHeldIsReported(t *testing.T) {
	if os.Getenv("WF_TEST_DATABASE_URL") == "" {
		t.Skip("Postgres only (make test-pg)")
	}
	ctx := tctx(t)
	db := openEmpty(t, backend.Postgres)
	fsys := fstest.MapFS{"1_unlock.sql": {Data: []byte(fmt.Sprintf("SELECT pg_advisory_unlock(%d);", pgLockKey))}}
	err := Run(ctx, db, backend.Postgres, fsys, -1)
	if err == nil || !strings.Contains(err.Error(), "advisory unlock") {
		t.Fatalf("err = %v, want an advisory unlock failure", err)
	}
	// The pool still works: the suspect session was discarded, not reused.
	if n := count(t, db, backend.Postgres, `SELECT count(*) FROM schema_migrations`); n != 1 {
		t.Fatalf("schema_migrations rows = %d, want 1", n)
	}
}
