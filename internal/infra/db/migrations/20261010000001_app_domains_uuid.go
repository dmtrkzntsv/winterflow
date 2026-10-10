package migrations

import (
	"context"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	// No down step: narrowing back to CHAR(36) would only restore the bug.
	Migrations.MustRegister(appDomainsUUIDColumns, nil)
}

// appDomainsUUIDColumns retypes app_domains.app_id/server_id to UUID on
// Postgres. They were CHAR(36) while apps.app_id and servers.server_id are
// UUID, so FindClaims' join (app_domain -> app, server) failed with
// "operator does not exist: uuid = character" and every save-app with an
// ingress domain answered 400. SQLite has no UUID type: nothing to do.
func appDomainsUUIDColumns(ctx context.Context, db *bun.DB) error {
	if db.Dialect().Name() != dialect.PG {
		return nil
	}
	_, err := db.ExecContext(ctx, `ALTER TABLE app_domains
		ALTER COLUMN app_id TYPE UUID USING app_id::uuid,
		ALTER COLUMN server_id TYPE UUID USING server_id::uuid`)
	return err
}
