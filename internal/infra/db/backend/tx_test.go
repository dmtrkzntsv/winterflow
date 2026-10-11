package backend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/pkg/logger"
)

func newTable(t *testing.T) *db.DB {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.Exec(t, d, `CREATE TABLE tx_probe (n INTEGER PRIMARY KEY)`)
	return d
}

func insert(ctx context.Context, d *db.DB, n int) error {
	_, err := d.Tx.Querier(ctx).ExecContext(ctx, dbtest.Rebind(d.Driver, `INSERT INTO tx_probe (n) VALUES (?)`), n)
	return err
}

func probes(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.SQL.QueryRowContext(dbtest.Ctx(t), `SELECT count(*) FROM tx_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWithTxCommits(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	if err := d.Tx.WithTx(ctx, func(ctx context.Context) error {
		if err := insert(ctx, d, 1); err != nil {
			return err
		}
		return insert(ctx, d, 2)
	}); err != nil {
		t.Fatal(err)
	}
	if n := probes(t, d); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	boom := errors.New("boom")
	err := d.Tx.WithTx(ctx, func(ctx context.Context) error {
		if err := insert(ctx, d, 1); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := probes(t, d); n != 0 {
		t.Fatalf("rows = %d after rollback", n)
	}
}

func TestWithTxRollsBackOnPanic(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed")
			}
		}()
		_ = d.Tx.WithTx(ctx, func(ctx context.Context) error {
			if err := insert(ctx, d, 1); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if n := probes(t, d); n != 0 {
		t.Fatalf("rows = %d after panic", n)
	}
	// The connection went back to the pool: the next unit of work runs.
	if err := insert(ctx, d, 3); err != nil {
		t.Fatal(err)
	}
}

// A failing nested WithTx rolls back to its savepoint only; the outer
// transaction stays usable (on Postgres a failed statement would otherwise
// abort it) and commits its own work.
func TestNestedWithTxRollsBackToSavepoint(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	if err := d.Tx.WithTx(ctx, func(ctx context.Context) error {
		if err := insert(ctx, d, 1); err != nil {
			return err
		}
		inner := d.Tx.WithTx(ctx, func(ctx context.Context) error {
			if err := insert(ctx, d, 2); err != nil {
				return err
			}
			return insert(ctx, d, 1) // duplicate key: fails the statement
		})
		if inner == nil {
			t.Error("duplicate insert succeeded")
		}
		if err := d.Tx.WithTx(ctx, func(ctx context.Context) error { return insert(ctx, d, 4) }); err != nil {
			return err
		}
		return insert(ctx, d, 5)
	}); err != nil {
		t.Fatal(err)
	}
	var got []int
	rows, err := d.SQL.QueryContext(ctx, `SELECT n FROM tx_probe ORDER BY n`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		_ = rows.Scan(&n)
		got = append(got, n)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("rows = %v, want [1 4 5]", got)
	}
}

func TestNestedPanicRollsBackEverything(t *testing.T) {
	d := newTable(t)
	ctx := dbtest.Ctx(t)
	func() {
		defer func() { _ = recover() }()
		_ = d.Tx.WithTx(ctx, func(ctx context.Context) error {
			_ = insert(ctx, d, 1)
			return d.Tx.WithTx(ctx, func(ctx context.Context) error {
				_ = insert(ctx, d, 2)
				panic("boom")
			})
		})
	}()
	if n := probes(t, d); n != 0 {
		t.Fatalf("rows = %d after nested panic", n)
	}
}

func TestQuerierOutsideTxIsThePool(t *testing.T) {
	d := newTable(t)
	if q := d.Tx.Querier(context.Background()); q != backend.DBTX(d.SQL) {
		t.Fatalf("Querier = %T, want the *sql.DB", q)
	}
}

// TestDebugSQLLogsAndDelegates: with debug on, every statement kind reaches
// the database and leaves one log record carrying the statement text and
// duration, but never the arguments (they include password and token hashes).
func TestDebugSQLLogsAndDelegates(t *testing.T) {
	d := newTable(t)
	var buf bytes.Buffer
	log := &logger.Logger{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}
	m := backend.NewTxManager(d.SQL, log, true)
	ctx := dbtest.Ctx(t)
	q := m.Querier(ctx)

	const secret = 7777
	insertSQL := dbtest.Rebind(d.Driver, `INSERT INTO tx_probe (n) VALUES (?)`)
	if _, err := q.ExecContext(ctx, insertSQL, secret); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM tx_probe`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
	rows, err := q.QueryContext(ctx, `SELECT n FROM tx_probe`)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	st, err := q.PrepareContext(ctx, `SELECT n FROM tx_probe WHERE n >= 0`)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	// A failing statement logs its error.
	if _, err := q.ExecContext(ctx, insertSQL, secret); err == nil {
		t.Fatal("duplicate insert succeeded")
	}

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	if len(records) != 5 {
		t.Fatalf("got %d log records, want 5:\n%s", len(records), buf.String())
	}
	for i, want := range []string{"INSERT INTO tx_probe", "SELECT count(*)", "SELECT n FROM tx_probe", "WHERE n >= 0", "INSERT INTO tx_probe"} {
		query, _ := records[i]["query"].(string)
		if !strings.Contains(query, want) {
			t.Errorf("record %d query = %q, want it to contain %q", i, query, want)
		}
		if _, ok := records[i]["duration"].(string); !ok {
			t.Errorf("record %d has no duration: %v", i, records[i])
		}
	}
	if records[0]["error"] != nil || records[4]["error"] == nil {
		t.Errorf("error fields: first = %v, last = %v", records[0]["error"], records[4]["error"])
	}
	if strings.Contains(buf.String(), "7777") {
		t.Errorf("arguments leaked into the log:\n%s", buf.String())
	}

	// Without debug the manager hands out the bare pool: nothing is logged.
	buf.Reset()
	quiet := backend.NewTxManager(d.SQL, log, false)
	if _, err := quiet.Querier(ctx).ExecContext(ctx, `DELETE FROM tx_probe`); err != nil || buf.Len() != 0 {
		t.Fatalf("quiet manager: err = %v, log = %q", err, buf.String())
	}
}

// A rollback that fails after fn already failed (here: the context died and
// database/sql rolled the transaction back itself) is logged; the caller
// still gets fn's error.
func TestWithTxReturnsFnErrorWhenRollbackFails(t *testing.T) {
	d := newTable(t)
	var buf bytes.Buffer
	log := &logger.Logger{Logger: slog.New(slog.NewJSONHandler(&buf, nil))}
	m := backend.NewTxManager(d.SQL, log, false)
	ctx, cancel := context.WithCancel(dbtest.Ctx(t))
	defer cancel()
	boom := errors.New("boom")
	err := m.WithTx(ctx, func(context.Context) error {
		cancel()
		time.Sleep(100 * time.Millisecond) // let database/sql finish its own rollback
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if !strings.Contains(buf.String(), "Failed to rollback transaction") {
		t.Fatalf("rollback failure not logged: %q", buf.String())
	}
}
