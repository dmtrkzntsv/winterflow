package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(createServerStorages, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS server_storages")
		return err
	})
}

// createServerStorages is the API's index of each agent's storages (the
// agent's storages/ dir is the source of truth): name, type and location,
// never a secret. Rebuilt from storage.list.
func createServerStorages(ctx context.Context, db *bun.DB) error {
	uuidType := "VARCHAR(36)"
	if db.Dialect().Name() == dialect.PG {
		uuidType = "UUID"
	}
	_, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE server_storages (
		server_id %s NOT NULL,
		name VARCHAR(32) NOT NULL,
		type VARCHAR(16) NOT NULL,
		location VARCHAR(1024) NOT NULL DEFAULT '',
		updated_at TIMESTAMP NOT NULL,
		PRIMARY KEY (server_id, name),
		FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
	)`, uuidType))
	if err != nil {
		return fmt.Errorf("server_storages migration: %w", err)
	}
	return nil
}
