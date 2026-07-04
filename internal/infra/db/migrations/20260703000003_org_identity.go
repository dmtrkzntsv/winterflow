package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return addOrgIdentity(ctx, db)
	}, func(ctx context.Context, db *bun.DB) error {
		// SQLite cannot drop columns portably; the added columns are
		// harmless on rollback.
		return nil
	})
}

// addOrgIdentity gives organizations a visual identity (icon + color, same
// vocabulary as apps) editable on the members page.
func addOrgIdentity(ctx context.Context, db *bun.DB) error {
	stmts := []string{
		"ALTER TABLE organizations ADD COLUMN icon VARCHAR(64) NOT NULL DEFAULT ''",
		"ALTER TABLE organizations ADD COLUMN color VARCHAR(7) NOT NULL DEFAULT ''",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("org identity migration: %w", err)
		}
	}
	return nil
}
