package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(createServerDomains, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS server_domains")
		return err
	})
}

// createServerDomains is the API's index of each agent's domain
// registrations (the agent's domains/ dir is the source of truth): what
// save-app checks ingress hostnames against. Rebuilt from domain.list.
func createServerDomains(ctx context.Context, db *bun.DB) error {
	uuidType := "VARCHAR(36)"
	if db.Dialect().Name() == dialect.PG {
		uuidType = "UUID"
	}
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE server_domains (
			server_id %s NOT NULL,
			name VARCHAR(253) NOT NULL,
			tls VARCHAR(16) NOT NULL,
			not_after TIMESTAMP,
			issuer VARCHAR(255) NOT NULL DEFAULT '',
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (server_id, name),
			FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
		)`, uuidType),
		"CREATE INDEX idx_server_domains_name ON server_domains (name)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("server_domains migration: %w", err)
		}
	}
	return nil
}
