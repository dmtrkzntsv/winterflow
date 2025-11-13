package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/driver/sqliteshim"
	"github.com/uptrace/bun/extra/bundebug"
	"github.com/uptrace/bun/migrate"

	"winterflow/internal/infra/db/migrations"
	"winterflow/pkg/logger"
)

type BunConnection struct {
	mutex sync.RWMutex
	db    *bun.DB
	log   *logger.Logger
}

func NewBunConnection(log *logger.Logger, connStr string) *BunConnection {
	db, err := setupBun(connStr, log)
	if err != nil {
		log.Fatal("Failed to set up database connection", "error", err)
	}
	return &BunConnection{
		db:  db,
		log: log,
	}
}

func setupBun(connStr string, log *logger.Logger) (*bun.DB, error) {
	var sqldb *sql.DB
	var err error
	var db *bun.DB

	if strings.HasPrefix(connStr, "sqlite://") {
		sqldb, err = setupSQLite(connStr, log)
		if err != nil {
			return nil, fmt.Errorf("failed to setup SQLite: %w", err)
		}
		db = bun.NewDB(sqldb, sqlitedialect.New())
	} else if strings.HasPrefix(connStr, "postgres://") {
		sqldb, err = setupPostgreSQL(connStr, log)
		if err != nil {
			return nil, fmt.Errorf("failed to setup PostgreSQL: %w", err)
		}
		db = bun.NewDB(sqldb, pgdialect.New())
	} else {
		return nil, fmt.Errorf("unsupported database type in connection string: %s", connStr)
	}

	// Add query hook for debugging (only in development)
	if os.Getenv("DEBUG_SQL") == "true" {
		db.AddQueryHook(bundebug.NewQueryHook(
			bundebug.WithVerbose(true),
			bundebug.FromEnv("BUNDEBUG"),
		))
	}

	// Register models for Bun ORM
	registerModels(db)

	// Run migrations using Bun's migration system
	migrator := migrate.NewMigrator(db, migrations.Migrations)
	if err := migrator.Init(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize migrator: %w", err)
	}

	if _, err := migrator.Migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Info("Database connection established successfully")
	return db, nil
}

func setupSQLite(connStr string, log *logger.Logger) (*sql.DB, error) {
	dbPath := strings.TrimPrefix(connStr, "sqlite://")

	if dbPath == "" {
		return nil, fmt.Errorf("invalid connection string: path is empty")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	sqldb, err := sql.Open(sqliteshim.ShimName, dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = 1000",
		"PRAGMA temp_store = memory",
		"PRAGMA mmap_size = 268435456", // 256MB
	}

	for _, pragma := range pragmas {
		if _, err := sqldb.Exec(pragma); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("failed to execute pragma %s: %w", pragma, err)
		}
	}

	// Set connection pool settings optimized for SQLite
	sqldb.SetMaxOpenConns(1)    // SQLite can only handle one writer at a time
	sqldb.SetMaxIdleConns(1)    // Keep one connection alive
	sqldb.SetConnMaxLifetime(0) // Connections never expire (SQLite is file-based)

	return sqldb, nil
}

func setupPostgreSQL(connStr string, log *logger.Logger) (*sql.DB, error) {
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(connStr)))

	// Set connection pool settings optimized for PostgreSQL
	sqldb.SetMaxOpenConns(25)                 // Maximum number of open connections
	sqldb.SetMaxIdleConns(10)                 // Maximum number of idle connections
	sqldb.SetConnMaxLifetime(5 * time.Minute) // Maximum amount of time a connection may be reused

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL database: %w", err)
	}

	return sqldb, nil
}

func (d *BunConnection) GetDB() *bun.DB {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	return d.db
}

func (d *BunConnection) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := d.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database health check failed: %w", err)
	}

	return nil
}

func (d *BunConnection) Shutdown() error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if d.db == nil {
		return nil
	}

	if err := d.db.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	d.db = nil
	return nil
}

func (d *BunConnection) Transaction(ctx context.Context, fn func(bun.IDB) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			d.log.Error("Failed to rollback transaction", "error", rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		d.log.Error("Failed to commit transaction", "error", err)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
