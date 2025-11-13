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
	} else {
		timestampType = "TIMESTAMP"
		timestampDefault = "DEFAULT CURRENT_TIMESTAMP"
		uuidType = "VARCHAR(36)"
	}

	// Create all tables with proper types for each database
	tables := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS organizations (
            organization_id %s PRIMARY KEY,
            name VARCHAR(255) NOT NULL,
            subscription_status VARCHAR(6) NOT NULL DEFAULT 'unpaid' CHECK (subscription_status IN ('paid', 'unpaid')),
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

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS agents (
            agent_id %s PRIMARY KEY,
            organization_id %s NOT NULL,
            name VARCHAR(64) NOT NULL,
            subscription_status VARCHAR(8) NOT NULL DEFAULT 'unpaid' CHECK (subscription_status IN ('paid', 'unpaid')),
            created_at %s NOT NULL %s,
            last_seen %s,
            FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
        )`, uuidType, uuidType, timestampType, timestampDefault, timestampType),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS agent_capabilities (
            agent_id %s NOT NULL,
            name VARCHAR(64) NOT NULL,
            value TEXT NOT NULL DEFAULT '',
            updated_at %s NOT NULL %s,
            PRIMARY KEY (agent_id, name),
            FOREIGN KEY (agent_id) REFERENCES agents (agent_id) ON DELETE CASCADE
        )`, uuidType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS agent_features (
            agent_id %s NOT NULL,
            name VARCHAR(64) NOT NULL,
            is_enabled BOOLEAN NOT NULL,
            PRIMARY KEY (agent_id, name),
            FOREIGN KEY (agent_id) REFERENCES agents (agent_id) ON DELETE CASCADE
        )`, uuidType),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS agent_registrations (
            agent_id %s PRIMARY KEY,
            certificate_id %s NOT NULL,
            hostname TEXT NOT NULL,
            code VARCHAR(6) NOT NULL UNIQUE,
            expires_at %s NOT NULL,
            certificate TEXT NOT NULL,
            certificate_expires_at %s NOT NULL,
            created_at %s NOT NULL %s
        )`, uuidType, uuidType, timestampType, timestampType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS agent_certificates (
            certificate_id %s PRIMARY KEY,
            agent_id %s NOT NULL,
            certificate TEXT NOT NULL,
            is_active BOOLEAN NOT NULL DEFAULT TRUE,
            expires_at %s NOT NULL,
            created_at %s NOT NULL %s,
            FOREIGN KEY (agent_id) REFERENCES agents (agent_id) ON DELETE CASCADE
        )`, uuidType, uuidType, timestampType, timestampType, timestampDefault),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS release_versions (
            version_number VARCHAR(9) PRIMARY KEY,
            name VARCHAR(32) NOT NULL UNIQUE,
            is_beta BOOLEAN NOT NULL DEFAULT FALSE,
            message TEXT,
            url TEXT NOT NULL
        )`),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS apps (
            app_id %s PRIMARY KEY,
            agent_id %s NOT NULL,
            template_id %s,
            name VARCHAR(255) NOT NULL,
            version VARCHAR(128) NOT NULL DEFAULT '',
            icon VARCHAR(64) NOT NULL,
            color VARCHAR(7) NOT NULL,
            created_at %s NOT NULL %s,
            FOREIGN KEY (agent_id) REFERENCES agents (agent_id) ON DELETE CASCADE
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
		"CREATE INDEX IF NOT EXISTS idx_agent_registrations_code ON agent_registrations (code)",
		"CREATE INDEX IF NOT EXISTS idx_agent_registrations_expires_at ON agent_registrations (expires_at)",
		"CREATE INDEX IF NOT EXISTS idx_agent_certificates_agent_id ON agent_certificates (agent_id)",
		"CREATE INDEX IF NOT EXISTS idx_apps_agent_id ON apps (agent_id)",
		"CREATE INDEX IF NOT EXISTS idx_apps_template_id ON apps (template_id)",
		"CREATE INDEX IF NOT EXISTS idx_agent_certificates_is_active_expires_at ON agent_certificates (is_active, expires_at)",
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
		"agent_certificates",
		"agent_registrations",
		"agent_features",
		"agent_capabilities",
		"agents",
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
