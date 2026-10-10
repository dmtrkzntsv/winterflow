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

	"github.com/uptrace/bun/driver/pgdriver"
)

// dbEnv names the Postgres server the suite runs against. Unset means
// SQLite: one temp file per test.
const dbEnv = "WF_TEST_DATABASE_URL"

// dialect reports which database this run exercises ("sqlite" or "postgres").
func dialect() string {
	if strings.HasPrefix(os.Getenv(dbEnv), "postgres://") {
		return "postgres"
	}
	return "sqlite"
}

// freshDB returns a connection string for an empty database owned by t.
// SQLite: a temp file. Postgres: a new schema t_<hex> on WF_TEST_DATABASE_URL,
// selected with search_path, dropped on cleanup.
func freshDB(t *testing.T) string {
	t.Helper()
	base := os.Getenv(dbEnv)
	if dialect() == "sqlite" {
		return "sqlite://" + filepath.Join(t.TempDir(), "wf.sqlite")
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(b[:])
	admin := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(base)))
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema on %s: %v", dbEnv, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", dbEnv, err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	// A non-UTC session zone: the server must pin UTC itself (db.utcDSN).
	q.Set("timezone", "America/New_York")
	u.RawQuery = q.Encode()
	return fmt.Sprint(u)
}
