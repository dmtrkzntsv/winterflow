// Package dbtest opens migrated throwaway databases for tests: a SQLite temp
// file by default, or a fresh schema on the Postgres server named by
// WF_TEST_DATABASE_URL (the contract suite's convention). Test-only: import
// it from _test.go files.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver for the admin connection

	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Env names the Postgres server tests run against; unset means SQLite.
const Env = "WF_TEST_DATABASE_URL"

// Log is a quiet logger for tests.
var Log = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})

// Driver reports the engine this run exercises. A WF_TEST_DATABASE_URL that
// is neither postgres:// nor postgresql:// fails the test.
func Driver(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv(Env)
	if dsn == "" {
		return backend.SQLite
	}
	scheme, _, _ := strings.Cut(dsn, "://")
	if scheme != "postgres" && scheme != "postgresql" {
		t.Fatalf("%s: unsupported scheme %q (want postgres:// or postgresql://, or unset for SQLite)", Env, scheme)
	}
	return backend.Postgres
}

// URL returns a DATABASE_URL for an empty database owned by t: a SQLite temp
// file, or a new schema t_<hex> on WF_TEST_DATABASE_URL selected with
// search_path and dropped on cleanup. The Postgres URL asks for a non-UTC
// session zone on purpose: db.Open must pin UTC itself.
func URL(t testing.TB) string {
	t.Helper()
	if Driver(t) == backend.SQLite {
		return "sqlite://" + filepath.Join(t.TempDir(), "wf.sqlite")
	}
	base := os.Getenv(Env)
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(b[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema on %s: %v", Env, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", Env, err)
	}
	u.Scheme = "postgres" // db.Open accepts only postgres://
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("timezone", "America/New_York")
	u.RawQuery = q.Encode()
	return u.String()
}

// Open returns a migrated database through the production db.Open, closed
// on cleanup.
func Open(t testing.TB) *db.DB {
	t.Helper()
	d, err := db.Open(Log, URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown() })
	return d
}

// Ctx is a context that ends after 30 s, so a repository that deadlocks on
// SQLite's single connection fails the test instead of hanging it.
func Ctx(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Rebind rewrites ? placeholders to $1, $2, ... for Postgres.
func Rebind(driver, query string) string {
	if driver != backend.Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

// Exec runs a ?-placeholder statement on d, failing the test on error.
func Exec(t testing.TB, d *db.DB, query string, args ...any) {
	t.Helper()
	if _, err := d.SQL.ExecContext(Ctx(t), Rebind(d.Driver, query), args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// QueryString runs a ?-placeholder query returning one text value.
func QueryString(t testing.TB, d *db.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := d.SQL.QueryRowContext(Ctx(t), Rebind(d.Driver, query), args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s
}

// ID returns a fixed, valid UUID for small n (Postgres columns are UUID):
// ID(1) = "00000000-0000-7000-8000-000000000001". IDs sort by n.
func ID(n int) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}

// SeedOrg inserts an organization row (name = its id).
func SeedOrg(t testing.TB, d *db.DB, orgID string) {
	t.Helper()
	Exec(t, d, `INSERT INTO organizations (organization_id, name, created_at) VALUES (?, ?, ?)`,
		orgID, orgID, types.NewDateTime())
}

// SeedServer inserts an organization and a claimed server it owns.
func SeedServer(t testing.TB, d *db.DB, orgID, serverID string) {
	t.Helper()
	SeedOrg(t, d, orgID)
	Exec(t, d, `INSERT INTO servers (server_id, organization_id, name, created_at) VALUES (?, ?, 'box', ?)`,
		serverID, orgID, types.NewDateTime())
}

// SeedMember inserts a user who is an owner of orgID.
func SeedMember(t testing.TB, d *db.DB, userID, orgID string) {
	t.Helper()
	now := types.NewDateTime()
	Exec(t, d, `INSERT INTO users (user_id, name, created_at, last_seen) VALUES (?, ?, ?, ?)`, userID, userID, now, now)
	Exec(t, d, `INSERT INTO organization_users (organization_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`, orgID, userID, now)
}

// SeedApp inserts an app row on serverID.
func SeedApp(t testing.TB, d *db.DB, appID, serverID, name string) {
	t.Helper()
	Exec(t, d, `INSERT INTO apps (app_id, server_id, name, icon, color, created_at) VALUES (?, ?, ?, 'i', '#000000', ?)`,
		appID, serverID, name, types.NewDateTime())
}
