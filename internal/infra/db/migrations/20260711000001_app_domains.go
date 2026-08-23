package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return createAppDomains(ctx, db)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS app_domains")
		return err
	})
}

// createAppDomains is the rebuildable ingress index: one row per hostname an
// app claims (routes + domain-level redirect sources; path rules ride an
// existing row). PK on domain = global cross-app/cross-server uniqueness.
func createAppDomains(ctx context.Context, db *bun.DB) error {
	stmts := []string{
		`CREATE TABLE app_domains (
			domain VARCHAR(253) NOT NULL PRIMARY KEY,
			app_id CHAR(36) NOT NULL,
			server_id CHAR(36) NOT NULL,
			kind VARCHAR(16) NOT NULL,
			ssl BOOLEAN NOT NULL DEFAULT FALSE,
			upstream_port INTEGER NOT NULL DEFAULT 0,
			target VARCHAR(2048) NOT NULL DEFAULT '',
			code INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL
		)`,
		"CREATE INDEX idx_app_domains_app_id ON app_domains (app_id)",
		"CREATE INDEX idx_app_domains_server_id ON app_domains (server_id)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("app_domains migration: %w", err)
		}
	}
	return nil
}
