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
	if err := d.SQL.QueryRowContext(dbtest.Ctx(t), `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
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

// The error reaches the logs (journald), so the URL's password must not.
func TestOpenRejectsUnknownScheme(t *testing.T) {
	for in, want := range map[string]string{
		"mysql://u:s3cret@h/db":        "unsupported database type in connection string: mysql://u:xxxxx@h/db",
		"mysql://u:s3cret@h/db\x7f":    "unsupported database type in connection string: mysql://<unparsable>",
		"mysql://h/db?password=s3cret": "unsupported database type in connection string: mysql://h/db",
		"mysql:u:s3cret@h":             "unsupported database type in connection string: <unparsable>",
		"host=h password=s3cret":       "unsupported database type in connection string: <unparsable>",
		"s3cret":                       "unsupported database type in connection string: <unparsable>",
	} {
		_, err := db.Open(dbtest.Log, in)
		if err == nil || err.Error() != want {
			t.Errorf("%q: err = %v, want %q", in, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%q: err = %v leaks the password", in, err)
		}
	}
}

func TestOpenRejectsEmptySQLitePath(t *testing.T) {
	if _, err := db.Open(dbtest.Log, "sqlite://"); err == nil {
		t.Fatal("empty path accepted")
	}
}
