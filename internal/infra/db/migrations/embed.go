package migrations

import (
	"embed"
	"fmt"
	"io/fs"

	"winterflow/internal/infra/db/backend"
)

// Per-engine SQL migrations, <version>_<name>.sql. The same directories are
// sqlc's schema input (internal/infra/db/sqlc/sqlc.yaml).
//
//go:embed sqlite/*.sql pgsql/*.sql
var files embed.FS

// FS returns the migration directory for a driver (backend.SQLite or
// backend.Postgres), rooted so its entries are the .sql files.
func FS(driver string) (fs.FS, error) {
	switch driver {
	case backend.SQLite:
		return fs.Sub(files, "sqlite")
	case backend.Postgres:
		return fs.Sub(files, "pgsql")
	}
	return nil, fmt.Errorf("migrations: unknown driver %q", driver)
}
