package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(appDomainsUUIDColumns, appDomainsUUIDColumnsDown)
}

// uuidPattern matches a canonical UUID, case-insensitively (~*).
const uuidPattern = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`

// appDomainsUUIDColumns retypes app_domains.app_id/server_id to UUID on
// Postgres. They were CHAR(36) while apps.app_id and servers.server_id are
// UUID, so FindClaims' join (app_domain -> app, server) failed with
// "operator does not exist: uuid = character" and every save-app with an
// ingress domain answered 400. SQLite has no UUID type: nothing to do.
//
// app_domains is the rebuildable ingress index (the next apps.list
// reconcile repopulates it), so rows whose ids are not UUIDs are deleted
// first: a single such row would make the cast fail and crashloop startup.
func appDomainsUUIDColumns(ctx context.Context, db *bun.DB) error {
	if db.Dialect().Name() != dialect.PG {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM app_domains WHERE app_id !~* ? OR server_id !~* ?`, uuidPattern, uuidPattern); err != nil {
		return fmt.Errorf("app_domains: drop non-UUID rows: %w", err)
	}
	_, err := db.ExecContext(ctx, `ALTER TABLE app_domains
		ALTER COLUMN app_id TYPE UUID USING app_id::uuid,
		ALTER COLUMN server_id TYPE UUID USING server_id::uuid`)
	return err
}

// appDomainsUUIDColumnsDown is deliberately a no-op: narrowing the columns
// back to CHAR(36) would only restore the join bug, and UUID values read
// back fine as text on either type.
func appDomainsUUIDColumnsDown(context.Context, *bun.DB) error { return nil }
