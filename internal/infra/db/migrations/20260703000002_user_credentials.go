package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return createUserCredentials(ctx, db)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS user_credentials")
		return err
	})
}

// createUserCredentials backs the always-on local (email+password) auth.
// One row per local user; Google-only users have none. Only the bcrypt hash
// is stored.
func createUserCredentials(ctx context.Context, db *bun.DB) error {
	var timestampType, timestampDefault, uuidType string
	switch db.Dialect().Name() {
	case dialect.PG:
		timestampType, timestampDefault, uuidType = "TIMESTAMPTZ", "DEFAULT NOW()", "UUID"
	case dialect.SQLite:
		timestampType, timestampDefault, uuidType = "TIMESTAMP", "DEFAULT CURRENT_TIMESTAMP", "VARCHAR(36)"
	default:
		return fmt.Errorf("unsupported database dialect: %s", db.Dialect().Name())
	}

	stmt := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS user_credentials (
        user_id              %s PRIMARY KEY,
        email                VARCHAR(255) NOT NULL UNIQUE,
        password_hash        VARCHAR(100) NOT NULL,
        must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
        updated_at           %s NOT NULL %s,
        FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
    )`, uuidType, timestampType, timestampDefault)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("user_credentials migration: %w", err)
	}
	return nil
}
