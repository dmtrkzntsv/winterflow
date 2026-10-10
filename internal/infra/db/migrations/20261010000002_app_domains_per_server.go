package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(appDomainsPerServerKey, appDomainsPerServerKeyDown)
}

// appDomainsPerServerKey makes a hostname unique per server instead of
// globally: the primary key moves from (domain) to (server_id, domain), so
// apps on different servers may claim the same hostname (DNS round robin)
// while two apps on one server still cannot. Every existing row satisfies
// the new key, so all rows are kept.
func appDomainsPerServerKey(ctx context.Context, db *bun.DB) error {
	if db.Dialect().Name() == dialect.PG {
		_, err := db.ExecContext(ctx,
			`ALTER TABLE app_domains DROP CONSTRAINT app_domains_pkey, ADD PRIMARY KEY (server_id, domain)`)
		return err
	}
	// SQLite cannot alter a primary key: rebuild the table.
	stmts := []string{
		`CREATE TABLE app_domains_new (
			domain VARCHAR(253) NOT NULL,
			app_id CHAR(36) NOT NULL,
			server_id CHAR(36) NOT NULL,
			kind VARCHAR(16) NOT NULL,
			ssl BOOLEAN NOT NULL DEFAULT FALSE,
			upstream_port INTEGER NOT NULL DEFAULT 0,
			target VARCHAR(2048) NOT NULL DEFAULT '',
			code INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (server_id, domain)
		)`,
		`INSERT INTO app_domains_new (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
			SELECT domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at FROM app_domains`,
		`DROP TABLE app_domains`,
		`ALTER TABLE app_domains_new RENAME TO app_domains`,
		"CREATE INDEX idx_app_domains_app_id ON app_domains (app_id)",
		"CREATE INDEX idx_app_domains_server_id ON app_domains (server_id)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("app_domains per-server key: %w", err)
		}
	}
	return nil
}

// appDomainsPerServerKeyDown is a no-op: the global key cannot be restored
// once two servers share a hostname, and the index is rebuildable anyway.
func appDomainsPerServerKeyDown(context.Context, *bun.DB) error { return nil }
