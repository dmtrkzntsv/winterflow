package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/migrate"
	"winterflow/internal/infra/db/pgsql"
	"winterflow/internal/infra/db/sqlite"
	"winterflow/pkg/logger"
)

// migrateTimeout bounds the startup migration run, including the wait for
// another replica's Postgres advisory lock.
const migrateTimeout = 5 * time.Minute

// DB is an open, migrated database: the pool, its driver name
// (backend.SQLite or backend.Postgres) and the transaction manager every
// repository is built on.
type DB struct {
	SQL    *sql.DB
	Driver string
	Tx     *backend.TxManager

	mu     sync.Mutex
	closed bool
}

// Open connects to DATABASE_URL (sqlite://<path> or postgres://...) and
// applies pending migrations. DEBUG_SQL=true logs every statement.
func Open(log *logger.Logger, url string) (*DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancel()

	var (
		sqldb  *sql.DB
		driver string
		err    error
	)
	switch {
	case strings.HasPrefix(url, sqlite.Scheme):
		driver = backend.SQLite
		sqldb, err = sqlite.Open(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("failed to setup SQLite: %w", err)
		}
	case strings.HasPrefix(url, pgsql.Scheme):
		driver = backend.Postgres
		sqldb, err = pgsql.Open(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("failed to setup PostgreSQL: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported database type in connection string: %s", url)
	}

	if err := migrate.Migrate(ctx, sqldb, driver); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	log.Info("Database connection established successfully")
	return &DB{
		SQL:    sqldb,
		Driver: driver,
		Tx:     backend.NewTxManager(sqldb, log, os.Getenv("DEBUG_SQL") == "true"),
	}, nil
}

// HealthCheck pings the database within 5 s.
func (d *DB) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.SQL.PingContext(ctx); err != nil {
		return fmt.Errorf("database health check failed: %w", err)
	}
	return nil
}

// Shutdown closes the pool; later calls are no-ops.
func (d *DB) Shutdown() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if err := d.SQL.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}
	return nil
}
