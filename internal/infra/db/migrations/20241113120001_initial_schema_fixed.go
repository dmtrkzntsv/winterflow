package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return createInitialSchemaFixed(ctx, db)
	}, func(ctx context.Context, db *bun.DB) error {
		return dropInitialSchemaFixed(ctx, db)
	})
}

func createInitialSchemaFixed(ctx context.Context, db *bun.DB) error {
	dialectName := db.Dialect().Name()

	var timestampType, timestampDefault, uuidType string

	if dialectName == dialect.PG {
		timestampType = "TIMESTAMPTZ"
		timestampDefault = "DEFAULT NOW()"
		uuidType = "UUID"
	} else if dialectName == dialect.SQLite {
		timestampType = "TIMESTAMP"
		timestampDefault = "DEFAULT CURRENT_TIMESTAMP"
		uuidType = "VARCHAR(36)"
	} else {
		return fmt.Errorf("unsupported database dialect: %s", dialectName)
	}

	// Create all tables with proper types for each database
	tables := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS organizations (
            organization_id %s PRIMARY KEY,
            name VARCHAR(255) NOT NULL,
            created_at %s NOT NULL %s
        )`, uuidType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS users (
            user_id %s PRIMARY KEY,
            name VARCHAR(255) NOT NULL,
            avatar TEXT,
            created_at %s NOT NULL %s,
            last_seen %s NOT NULL %s
        )`, uuidType, timestampType, timestampDefault, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS organization_users (
            organization_id %s NOT NULL,
            user_id %s NOT NULL,
            role VARCHAR(32) NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member', 'billing')),
            created_at %s NOT NULL %s,
            PRIMARY KEY (organization_id, user_id),
            FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        )`, uuidType, uuidType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS user_tokens (
            token_id %s PRIMARY KEY,
            token VARCHAR(32) NOT NULL UNIQUE,
            token_type VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
            user_id %s,
            expires_at %s,
            created_at %s NOT NULL %s,
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        )`, uuidType, uuidType, timestampType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS user_connected_accounts (
            provider VARCHAR(16) NOT NULL,
            external_id TEXT NOT NULL,
            user_id %s NOT NULL,
            PRIMARY KEY (provider, external_id),
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        )`, uuidType),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS servers (
            server_id %s PRIMARY KEY,
            organization_id %s NOT NULL,
            name VARCHAR(64) NOT NULL,
            created_at %s NOT NULL %s,
            last_seen %s,
            FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
        )`, uuidType, uuidType, timestampType, timestampDefault, timestampType),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS server_capabilities (
            server_id %s NOT NULL,
            name VARCHAR(64) NOT NULL,
            value TEXT NOT NULL DEFAULT '',
            updated_at %s NOT NULL %s,
            PRIMARY KEY (server_id, name),
            FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
        )`, uuidType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS server_features (
            server_id %s NOT NULL,
            name VARCHAR(64) NOT NULL,
            is_enabled BOOLEAN NOT NULL,
            PRIMARY KEY (server_id, name),
            FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
        )`, uuidType),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS server_registrations (
            server_id %s PRIMARY KEY,
            certificate_id %s NOT NULL,
            hostname TEXT NOT NULL,
            code VARCHAR(6) NOT NULL UNIQUE,
            expires_at %s NOT NULL,
            certificate TEXT NOT NULL,
            certificate_expires_at %s NOT NULL,
            created_at %s NOT NULL %s
        )`, uuidType, uuidType, timestampType, timestampType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS server_certificates (
            certificate_id %s PRIMARY KEY,
            server_id %s NOT NULL,
            certificate TEXT NOT NULL,
            is_active BOOLEAN NOT NULL DEFAULT TRUE,
            expires_at %s NOT NULL,
            created_at %s NOT NULL %s,
            FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
        )`, uuidType, uuidType, timestampType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS apps (
            app_id %s PRIMARY KEY,
            server_id %s NOT NULL,
            template_id %s,
            name VARCHAR(255) NOT NULL,
            version VARCHAR(128) NOT NULL DEFAULT '',
            icon VARCHAR(64) NOT NULL,
            color VARCHAR(7) NOT NULL,
            created_at %s NOT NULL %s,
            FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
        )`, uuidType, uuidType, uuidType, timestampType, timestampDefault),
	}

	// Execute all table creation statements
	for i, tableSQL := range tables {
		if _, err := db.ExecContext(ctx, tableSQL); err != nil {
			return fmt.Errorf("failed to create table %d: %w", i+1, err)
		}
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_user_tokens_token ON user_tokens (token)",
		"CREATE INDEX IF NOT EXISTS idx_user_connected_accounts_user_id ON user_connected_accounts (user_id)",
		"CREATE INDEX IF NOT EXISTS idx_server_registrations_code ON server_registrations (code)",
		"CREATE INDEX IF NOT EXISTS idx_server_registrations_expires_at ON server_registrations (expires_at)",
		"CREATE INDEX IF NOT EXISTS idx_server_certificates_server_id ON server_certificates (server_id)",
		"CREATE INDEX IF NOT EXISTS idx_apps_server_id ON apps (server_id)",
		"CREATE INDEX IF NOT EXISTS idx_apps_template_id ON apps (template_id)",
		"CREATE INDEX IF NOT EXISTS idx_server_certificates_is_active_expires_at ON server_certificates (is_active, expires_at)",
	}

	for _, indexSQL := range indexes {
		if _, err := db.ExecContext(ctx, indexSQL); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	return nil
}

func dropInitialSchemaFixed(ctx context.Context, db *bun.DB) error {
	tables := []string{
		"apps",
		"release_versions",
		"server_certificates",
		"server_registrations",
		"server_features",
		"server_capabilities",
		"servers",
		"user_connected_accounts",
		"user_tokens",
		"organization_users",
		"users",
		"organizations",
	}

	for _, table := range tables {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table)); err != nil {
			return fmt.Errorf("failed to drop table %s: %w", table, err)
		}
	}

	return nil
}
