// Package backend holds what every engine shares: the driver names, the
// DBTX execution surface sqlc-generated code runs on, and the
// savepoint-aware transaction manager (tx.go).
package backend

import (
	"context"
	"database/sql"
)

// Driver names. db.Open picks one from the DATABASE_URL scheme; every
// repository constructor switches on it once.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
)

// DBTX is the execution surface of sqlc-generated code. *sql.DB, *sql.Tx and
// *sql.Conn all satisfy it.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
