package migrate

import (
	"database/sql"
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
