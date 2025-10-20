package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	genq "winterflow/internal/infra/db/generated"
	"winterflow/pkg/logger"

	_ "modernc.org/sqlite"
)

type Connection struct {
	mutex sync.RWMutex
	db    *sql.DB
	log   *logger.Logger
}

func NewDbConnection(log *logger.Logger, connStr string) *Connection {
	db, err := setup(connStr, log)
	if err != nil {
		log.Fatal("Failed to set up database connection", "error", err)
	}
	return &Connection{
		db:  db,
		log: log,
	}
}

func setup(connStr string, log *logger.Logger) (*sql.DB, error) {
	if !strings.HasPrefix(connStr, "sqlite3://") {
		return nil, fmt.Errorf("invalid connection string: must start with sqlite3://")
	}

	dbPath := strings.TrimPrefix(connStr, "sqlite3://")
	if dbPath == "" {
		return nil, fmt.Errorf("invalid connection string: path is empty")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
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
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to execute pragma %s: %w", pragma, err)
		}
	}

	// Set connection pool settings optimized for SQLite
	db.SetMaxOpenConns(1)    // SQLite can only handle one writer at a time
	db.SetMaxIdleConns(1)    // Keep one connection alive
	db.SetConnMaxLifetime(0) // Connections never expire (SQLite is file-based)

	// Run migrations
	migrator := NewMigrator(log, db)
	if err := migrator.LoadMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to load migrations: %w", err)
	}

	if err := migrator.Migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Info("Database connection established successfully")
	return db, nil
}

func (d *Connection) GetConnection() *sql.DB {
	d.mutex.RLock()
	defer d.mutex.RUnlock()
	return d.db
}

func (d *Connection) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := d.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database health check failed: %w", err)
	}

	return nil
}

func (d *Connection) Shutdown() error {
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

func (d *Connection) Repo(ctx context.Context) (*genq.Queries, error) {
	if err := d.db.PingContext(ctx); err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	repo, err := genq.Prepare(ctx, d.db)
	if err != nil {
		d.log.Error("Failed to prepare database statements", "error", err)
		return nil, fmt.Errorf("failed to prepare statements: %w", err)
	}

	return repo, nil
}

func (d *Connection) Transaction(ctx context.Context, fn func(*genq.Queries) error) error {
	if err := d.db.PingContext(ctx); err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	repo := genq.New(d.db).WithTx(tx)

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(repo); err != nil {
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
