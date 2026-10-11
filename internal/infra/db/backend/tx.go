package backend

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"winterflow/pkg/logger"
)

// TxManager owns the *sql.DB and runs units of work. A transaction travels
// in the context: WithTx stores it, Querier hands it (or the pool) to sqlc.
type TxManager struct {
	db    *sql.DB
	log   *logger.Logger
	debug bool
}

// NewTxManager wraps db. debugSQL logs every statement and its duration
// (DEBUG_SQL=true).
func NewTxManager(db *sql.DB, log *logger.Logger, debugSQL bool) *TxManager {
	return &TxManager{db: db, log: log, debug: debugSQL}
}

// DB returns the pool, for wiring and tests; repositories use Querier.
func (m *TxManager) DB() *sql.DB { return m.db }

type txState struct {
	tx        *sql.Tx
	savepoint int
}

type ctxKey struct{}

// Querier returns the transaction stored in ctx by WithTx, or the pool.
func (m *TxManager) Querier(ctx context.Context) DBTX {
	var q DBTX = m.db
	if st, ok := ctx.Value(ctxKey{}).(*txState); ok {
		q = st.tx
	}
	if m.debug {
		return loggingDBTX{inner: q, log: m.log}
	}
	return q
}

// WithTx runs fn in a transaction: commit when fn returns nil, roll back
// when it returns an error or panics (the panic is re-raised). Called again
// with a ctx that already carries a transaction, it nests through
// SAVEPOINT sp_N / RELEASE SAVEPOINT / ROLLBACK TO SAVEPOINT (same syntax on
// SQLite and Postgres), so an inner failure leaves the outer transaction
// usable. fn must issue its queries through Querier(ctx) with the ctx it is
// given: on SQLite the pool has one connection, held by the transaction.
func (m *TxManager) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if st, ok := ctx.Value(ctxKey{}).(*txState); ok {
		return m.nested(ctx, st, fn)
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(context.WithValue(ctx, ctxKey{}, &txState{tx: tx})); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			m.log.Error("Failed to rollback transaction", "error", rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		m.log.Error("Failed to commit transaction", "error", err)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func (m *TxManager) nested(ctx context.Context, st *txState, fn func(ctx context.Context) error) error {
	st.savepoint++
	name := fmt.Sprintf("sp_%d", st.savepoint)
	if _, err := st.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return fmt.Errorf("failed to create savepoint %s: %w", name, err)
	}
	defer func() {
		if p := recover(); p != nil {
			_, _ = st.tx.ExecContext(context.WithoutCancel(ctx), "ROLLBACK TO SAVEPOINT "+name)
			panic(p)
		}
	}()
	if err := fn(ctx); err != nil {
		if _, rbErr := st.tx.ExecContext(context.WithoutCancel(ctx), "ROLLBACK TO SAVEPOINT "+name); rbErr != nil {
			m.log.Error("Failed to rollback to savepoint", "savepoint", name, "error", rbErr)
		}
		return err
	}
	if _, err := st.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return fmt.Errorf("failed to release savepoint %s: %w", name, err)
	}
	return nil
}

// loggingDBTX logs each statement and how long it took (DEBUG_SQL=true).
// Arguments are not logged: they include password and token hashes.
type loggingDBTX struct {
	inner DBTX
	log   *logger.Logger
}

func (l loggingDBTX) logQuery(query string, start time.Time, err error) {
	l.log.Info("sql", "query", query, "duration", time.Since(start).String(), "error", err)
}

func (l loggingDBTX) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := l.inner.ExecContext(ctx, query, args...)
	l.logQuery(query, start, err)
	return res, err
}

func (l loggingDBTX) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	start := time.Now()
	st, err := l.inner.PrepareContext(ctx, query)
	l.logQuery(query, start, err)
	return st, err
}

func (l loggingDBTX) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.inner.QueryContext(ctx, query, args...)
	l.logQuery(query, start, err)
	return rows, err
}

func (l loggingDBTX) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := l.inner.QueryRowContext(ctx, query, args...)
	l.logQuery(query, start, row.Err())
	return row
}
