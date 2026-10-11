// Package sqlite opens SQLite databases with the pure-Go modernc.org/sqlite
// driver (what the CGO-off release builds always used).
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Scheme prefixes a SQLite DATABASE_URL: sqlite://<path>.
const Scheme = "sqlite://"

var pragmas = []string{
	"PRAGMA foreign_keys = ON",
	"PRAGMA journal_mode = WAL",
	"PRAGMA synchronous = NORMAL",
	"PRAGMA cache_size = 1000",
	"PRAGMA temp_store = memory",
	"PRAGMA mmap_size = 268435456", // 256MB
}

// Open opens the database file named by a sqlite:// URL, creating its
// directory, applies the pragmas and pins the pool to one connection (one
// writer; the pragmas are per connection). It does not migrate.
func Open(ctx context.Context, url string) (*sql.DB, error) {
	path := strings.TrimPrefix(url, Scheme)
	if path == "" {
		return nil, fmt.Errorf("invalid connection string: path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}
	// One connection that never expires: SQLite has one writer, and the
	// pragmas below hold only on the connection they ran on.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to execute pragma %s: %w", p, err)
		}
	}
	return db, nil
}
