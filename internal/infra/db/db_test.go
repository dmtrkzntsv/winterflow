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
