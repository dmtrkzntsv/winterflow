package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return recreateUserTokensForPATs(ctx, db)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS user_tokens")
		return err
	})
}

// recreateUserTokensForPATs replaces the never-used v1 user_tokens table with
// the PAT shape: hashed token, display prefix, name, last-used. Dropping is
// safe — the feature never shipped, the table is empty everywhere.
func recreateUserTokensForPATs(ctx context.Context, db *bun.DB) error {
	var timestampType, timestampDefault, uuidType string
	switch db.Dialect().Name() {
	case dialect.PG:
		timestampType, timestampDefault, uuidType = "TIMESTAMPTZ", "DEFAULT NOW()", "UUID"
	case dialect.SQLite:
		timestampType, timestampDefault, uuidType = "TIMESTAMP", "DEFAULT CURRENT_TIMESTAMP", "VARCHAR(36)"
	default:
		return fmt.Errorf("unsupported database dialect: %s", db.Dialect().Name())
	}

	stmts := []string{
		"DROP TABLE IF EXISTS user_tokens",
		fmt.Sprintf(`CREATE TABLE user_tokens (
            token_id     %s PRIMARY KEY,
            user_id      %s NOT NULL,
            name         VARCHAR(64) NOT NULL,
            token_prefix VARCHAR(16) NOT NULL,
            token_hash   VARCHAR(64) NOT NULL UNIQUE,
            token_type   VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
            expires_at   %s,
            last_used_at %s,
            created_at   %s NOT NULL %s,
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        )`, uuidType, uuidType, timestampType, timestampType, timestampType, timestampDefault),
		"CREATE INDEX IF NOT EXISTS idx_user_tokens_user_id ON user_tokens (user_id)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("pat migration: %w", err)
		}
	}
	return nil
}
