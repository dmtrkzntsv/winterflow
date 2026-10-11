package migrate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"winterflow/internal/infra/db/backend"
)

// tctx bounds a test's database work: a deadlock on SQLite's single
// connection or a stuck advisory lock fails the test instead of hanging it.
func tctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// engines lists the drivers this run exercises: SQLite always, Postgres when
// WF_TEST_DATABASE_URL names a server (make test-pg, CI's contract job).
func engines(t *testing.T) []string {
	t.Helper()
	if os.Getenv("WF_TEST_DATABASE_URL") == "" {
		return []string{backend.SQLite}
	}
	return []string{backend.SQLite, backend.Postgres}
}

// openEmpty opens an empty, unmigrated database with production-like
// session settings: SQLite with foreign keys on and one connection;
// Postgres in a fresh schema with the session pinned to UTC. (Local to this
// package: a shared test helper would import migrate, an import cycle.)
func openEmpty(t *testing.T, driverName string) *sql.DB {
	t.Helper()
	ctx := tctx(t)
	if driverName == backend.SQLite {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "m.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	base := os.Getenv("WF_TEST_DATABASE_URL")
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "m_" + hex.EncodeToString(b[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(cctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	db, err := sql.Open("pgx", schemaDSN(t, base, schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// schemaDSN points base at one schema with the session pinned to UTC (the
// frozen dumps hold naive timestamp literals).
func schemaDSN(t *testing.T, base, schema string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	return u.String()
}

// rebind turns ? placeholders into $n for Postgres. (Local to this package,
// like openEmpty.)
func rebind(driverName, q string) string {
	if driverName != backend.Postgres {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

func mustExec(t *testing.T, db *sql.DB, driverName, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(tctx(t), rebind(driverName, q), args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func count(t *testing.T, db *sql.DB, driverName, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(tctx(t), rebind(driverName, q), args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func versions(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.QueryContext(tctx(t), `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// loadDump replays a frozen Bun-era dump (testdata/<engine>/<file>).
func loadDump(t *testing.T, db *sql.DB, driverName, file string) {
	t.Helper()
	ctx := tctx(t)
	dir := "sqlite"
	if driverName == backend.Postgres {
		dir = "pgsql"
	}
	b, err := os.ReadFile(filepath.Join("testdata", dir, file))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range Split(string(b)) {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("replay %s: %v\n%s", file, err, s)
		}
	}
}

// schemaOf is a normalized description of every table, column, constraint
// and index outside the migration bookkeeping, comparable across a fresh
// install and an adopted Bun-era database.
func schemaOf(t *testing.T, db *sql.DB, driverName string) []string {
	t.Helper()
	ctx := tctx(t)
	var queries []string
	if driverName == backend.SQLite {
		// sqlite_master.sql with whitespace collapsed: column types,
		// defaults, keys, CHECKs and foreign keys all live in it.
		queries = []string{`SELECT type || ' ' || name || ' ' || tbl_name || ' ' || COALESCE(sql, '')
			FROM sqlite_master
			WHERE name NOT LIKE 'sqlite_%' AND name NOT LIKE 'bun_%' AND name <> 'schema_migrations'
			ORDER BY type, name`}
	} else {
		queries = []string{
			`SELECT table_name || '.' || column_name || ' ' || ordinal_position || ' ' || data_type || ' ' ||
				COALESCE(character_maximum_length::text, '-') || ' ' || is_nullable || ' ' || COALESCE(column_default, '-')
			FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name NOT LIKE 'bun_%' AND table_name <> 'schema_migrations'
			ORDER BY table_name, ordinal_position`,
			`SELECT conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
			FROM pg_constraint
			WHERE connamespace = current_schema()::regnamespace
			  AND conrelid::regclass::text NOT LIKE 'bun_%' AND conrelid::regclass::text <> 'schema_migrations'
			ORDER BY 1`,
			`SELECT tablename || ' ' || indexname || ' ' || replace(indexdef, current_schema() || '.', '')
			FROM pg_indexes
			WHERE schemaname = current_schema() AND tablename NOT LIKE 'bun_%' AND tablename <> 'schema_migrations'
			ORDER BY 1`,
		}
	}
	var out []string
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, strings.Join(strings.Fields(s), " "))
		}
		rows.Close()
	}
	return out
}

// sameLines fails t with the lines only one side has.
func sameLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	in := func(xs []string) map[string]bool {
		m := make(map[string]bool, len(xs))
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	g, w := in(got), in(want)
	for _, x := range got {
		if !w[x] {
			t.Errorf("%s: unexpected %q", what, x)
		}
	}
	for _, x := range want {
		if !g[x] {
			t.Errorf("%s: missing %q", what, x)
		}
	}
	if len(want) == 0 {
		t.Errorf("%s: empty", what)
	}
}
