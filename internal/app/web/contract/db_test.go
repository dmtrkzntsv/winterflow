package contract_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver for the admin connection
)

// dbEnv names the Postgres server the suite runs against. Unset means
// SQLite: one temp file per test.
const dbEnv = "WF_TEST_DATABASE_URL"

// dialect reports which database this run exercises ("sqlite" or
// "postgres"); a WF_TEST_DATABASE_URL it cannot use fails the test rather
// than silently falling back to SQLite.
func dialect(t testing.TB) string {
	t.Helper()
	d, err := dialectOf(os.Getenv(dbEnv))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// dialectOf maps a WF_TEST_DATABASE_URL to its dialect: empty is SQLite,
// postgres:// and postgresql:// are Postgres, anything else is an error.
func dialectOf(dsn string) (string, error) {
	if dsn == "" {
		return "sqlite", nil
	}
	scheme, _, _ := strings.Cut(dsn, "://")
	switch scheme {
	case "postgres", "postgresql":
		return "postgres", nil
	}
	return "", fmt.Errorf("%s: unsupported scheme %q (want postgres:// or postgresql://, or unset for SQLite)", dbEnv, scheme)
}

func TestDialectOf(t *testing.T) {
	t.Parallel()
	for dsn, want := range map[string]string{
		"":                      "sqlite",
		"postgres://u:p@h/db":   "postgres",
		"postgresql://u:p@h/db": "postgres",
		"mysql://u:p@h/db":      "",
		"sqlite:///tmp/x":       "",
		"localhost:5432":        "",
	} {
		got, err := dialectOf(dsn)
		if got != want || (err != nil) != (want == "") {
			t.Errorf("dialectOf(%q) = %q, %v; want %q", dsn, got, err, want)
		}
	}
}

// freshDB returns a connection string for an empty database owned by t.
// SQLite: a temp file. Postgres: a new schema t_<hex> on WF_TEST_DATABASE_URL,
// selected with search_path, dropped on cleanup.
func freshDB(t *testing.T) string {
	t.Helper()
	base := os.Getenv(dbEnv)
	if dialect(t) == "sqlite" {
		return "sqlite://" + filepath.Join(t.TempDir(), "wf.sqlite")
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(b[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 30*time.Second)
	}
	ctx, cancel := adminCtx()
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema on %s: %v", dbEnv, err)
	}
	t.Cleanup(func() {
		ctx, cancel := adminCtx()
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", dbEnv, err)
	}
	u.Scheme = "postgres" // the server accepts only postgres:// (db.Open)
	q := u.Query()
	q.Set("search_path", schema)
	// A non-UTC session zone: the server must pin UTC itself (pgsql.utcDSN).
	q.Set("timezone", "America/New_York")
	u.RawQuery = q.Encode()
	return fmt.Sprint(u)
}
