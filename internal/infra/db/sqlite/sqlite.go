// Package sqlite opens SQLite databases with the pure-Go modernc.org/sqlite
// driver (what the CGO-off release builds always used).
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Scheme prefixes a SQLite DATABASE_URL: sqlite://<path>.
const Scheme = "sqlite://"

// pragmas are applied by the driver to every connection it opens (the DSN's
// _pragma parameters), not once on whichever connection the pool holds:
// database/sql discards a connection the driver reports unusable (a query
// interrupted by its context deadline, say) and opens a fresh one, which
// would otherwise come up with SQLite's defaults (foreign keys off).
var pragmas = []string{
	"foreign_keys(1)",
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
	"cache_size(1000)",
	"temp_store(memory)",
	"mmap_size(268435456)", // 256MB
}

// dsn builds the modernc.org/sqlite connection string for a database file:
// a file: URI (the path's '?', '#' and '%' escaped, so none of them is taken
// for the query) followed by the pragmas.
func dsn(path string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.Clean(path))
	return "file:" + escaped + "?" + q.Encode()
}

// Open opens the database file named by a sqlite:// URL, creating its
// directory, and pins the pool to one connection (one writer). Every
// connection the pool ever opens gets the pragmas. It does not migrate.
func Open(ctx context.Context, url string) (*sql.DB, error) {
	path := strings.TrimPrefix(url, Scheme)
	if path == "" {
		return nil, fmt.Errorf("invalid connection string: path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}
	// One connection that never expires: SQLite has one writer.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	// Connect now so a bad path or pragma fails at startup, not on first use.
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}
	return db, nil
}
