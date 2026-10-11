-- Frozen from the Bun-era code by internal/infra/db/freeze_dump_test.go
-- (every statement Bun executed except SELECTs). Do not edit.
CREATE TABLE IF NOT EXISTS bun_migrations ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "name" VARCHAR, "group_id" INTEGER, "migrated_at" TIMESTAMP NOT NULL DEFAULT current_timestamp);
CREATE TABLE IF NOT EXISTS bun_migration_locks ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "table_name" VARCHAR, UNIQUE ("table_name"));
INSERT INTO bun_migrations ("name", "group_id") VALUES ('20241113120001', 1) RETURNING "id", "migrated_at";
CREATE TABLE IF NOT EXISTS organizations (
            organization_id VARCHAR(36) PRIMARY KEY,
            name VARCHAR(255) NOT NULL,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
        );
CREATE TABLE IF NOT EXISTS users (
            user_id VARCHAR(36) PRIMARY KEY,
            name VARCHAR(255) NOT NULL,
            avatar TEXT,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
        );
CREATE TABLE IF NOT EXISTS organization_users (
            organization_id VARCHAR(36) NOT NULL,
            user_id VARCHAR(36) NOT NULL,
            role VARCHAR(32) NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member', 'billing')),
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            PRIMARY KEY (organization_id, user_id),
            FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        );
CREATE TABLE IF NOT EXISTS user_tokens (
            token_id VARCHAR(36) PRIMARY KEY,
            token VARCHAR(32) NOT NULL UNIQUE,
            token_type VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
            user_id VARCHAR(36),
            expires_at TIMESTAMP,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        );
CREATE TABLE IF NOT EXISTS user_connected_accounts (
            provider VARCHAR(16) NOT NULL,
            external_id TEXT NOT NULL,
            user_id VARCHAR(36) NOT NULL,
            PRIMARY KEY (provider, external_id),
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        );
CREATE TABLE IF NOT EXISTS servers (
            server_id VARCHAR(36) PRIMARY KEY,
            organization_id VARCHAR(36) NOT NULL,
            name VARCHAR(64) NOT NULL,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            last_seen TIMESTAMP,
            FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
        );
CREATE TABLE IF NOT EXISTS server_capabilities (
            server_id VARCHAR(36) NOT NULL,
            name VARCHAR(64) NOT NULL,
            value TEXT NOT NULL DEFAULT '',
            updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            PRIMARY KEY (server_id, name),
            FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
        );
CREATE TABLE IF NOT EXISTS server_features (
            server_id VARCHAR(36) NOT NULL,
            name VARCHAR(64) NOT NULL,
            is_enabled BOOLEAN NOT NULL,
            PRIMARY KEY (server_id, name),
            FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
        );
CREATE TABLE IF NOT EXISTS server_registrations (
            server_id VARCHAR(36) PRIMARY KEY,
            certificate_id VARCHAR(36) NOT NULL,
            hostname TEXT NOT NULL,
            code VARCHAR(6) NOT NULL UNIQUE,
            expires_at TIMESTAMP NOT NULL,
            certificate TEXT NOT NULL,
            certificate_expires_at TIMESTAMP NOT NULL,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
        );
CREATE TABLE IF NOT EXISTS server_certificates (
            certificate_id VARCHAR(36) PRIMARY KEY,
            server_id VARCHAR(36) NOT NULL,
            certificate TEXT NOT NULL,
            is_active BOOLEAN NOT NULL DEFAULT TRUE,
            expires_at TIMESTAMP NOT NULL,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
        );
CREATE TABLE IF NOT EXISTS apps (
            app_id VARCHAR(36) PRIMARY KEY,
            server_id VARCHAR(36) NOT NULL,
            template_id VARCHAR(36),
            name VARCHAR(255) NOT NULL,
            version VARCHAR(128) NOT NULL DEFAULT '',
            icon VARCHAR(64) NOT NULL,
            color VARCHAR(7) NOT NULL,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
        );
CREATE INDEX IF NOT EXISTS idx_user_tokens_token ON user_tokens (token);
CREATE INDEX IF NOT EXISTS idx_user_connected_accounts_user_id ON user_connected_accounts (user_id);
CREATE INDEX IF NOT EXISTS idx_server_registrations_code ON server_registrations (code);
CREATE INDEX IF NOT EXISTS idx_server_registrations_expires_at ON server_registrations (expires_at);
CREATE INDEX IF NOT EXISTS idx_server_certificates_server_id ON server_certificates (server_id);
CREATE INDEX IF NOT EXISTS idx_apps_server_id ON apps (server_id);
CREATE INDEX IF NOT EXISTS idx_apps_template_id ON apps (template_id);
CREATE INDEX IF NOT EXISTS idx_server_certificates_is_active_expires_at ON server_certificates (is_active, expires_at);
INSERT INTO bun_migrations ("name", "group_id") VALUES ('20260703000001', 1) RETURNING "id", "migrated_at";
DROP TABLE IF EXISTS user_tokens;
CREATE TABLE user_tokens (
            token_id     VARCHAR(36) PRIMARY KEY,
            user_id      VARCHAR(36) NOT NULL,
            name         VARCHAR(64) NOT NULL,
            token_prefix VARCHAR(16) NOT NULL,
            token_hash   VARCHAR(64) NOT NULL UNIQUE,
            token_type   VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
            expires_at   TIMESTAMP,
            last_used_at TIMESTAMP,
            created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
        );
CREATE INDEX IF NOT EXISTS idx_user_tokens_user_id ON user_tokens (user_id);
INSERT INTO bun_migrations ("name", "group_id") VALUES ('20260703000002', 1) RETURNING "id", "migrated_at";
CREATE TABLE IF NOT EXISTS user_credentials (
        user_id              VARCHAR(36) PRIMARY KEY,
        email                VARCHAR(255) NOT NULL UNIQUE,
        password_hash        VARCHAR(100) NOT NULL,
        must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
        updated_at           TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
    );
INSERT INTO bun_migrations ("name", "group_id") VALUES ('20260703000003', 1) RETURNING "id", "migrated_at";
ALTER TABLE organizations ADD COLUMN icon VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN color VARCHAR(7) NOT NULL DEFAULT '';
INSERT INTO bun_migrations ("name", "group_id") VALUES ('20260711000001', 1) RETURNING "id", "migrated_at";
CREATE TABLE app_domains (
			domain VARCHAR(253) NOT NULL PRIMARY KEY,
			app_id CHAR(36) NOT NULL,
			server_id CHAR(36) NOT NULL,
			kind VARCHAR(16) NOT NULL,
			ssl BOOLEAN NOT NULL DEFAULT FALSE,
			upstream_port INTEGER NOT NULL DEFAULT 0,
			target VARCHAR(2048) NOT NULL DEFAULT '',
			code INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL
		);
CREATE INDEX idx_app_domains_app_id ON app_domains (app_id);
CREATE INDEX idx_app_domains_server_id ON app_domains (server_id);
INSERT INTO bun_migrations ("name", "group_id") VALUES ('20261010000001', 1) RETURNING "id", "migrated_at";
INSERT INTO bun_migrations ("name", "group_id") VALUES ('20261010000002', 1) RETURNING "id", "migrated_at";
DROP TABLE IF EXISTS app_domains_new;
CREATE TABLE app_domains_new (
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
		);
INSERT INTO app_domains_new (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
			SELECT domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at FROM app_domains;
DROP TABLE app_domains;
ALTER TABLE app_domains_new RENAME TO app_domains;
CREATE INDEX idx_app_domains_app_id ON app_domains (app_id);
CREATE INDEX idx_app_domains_server_id ON app_domains (server_id);
INSERT INTO bun_migrations ("name", "group_id") VALUES ('20261010000003', 1) RETURNING "id", "migrated_at";
CREATE TABLE server_domains (
			server_id VARCHAR(36) NOT NULL,
			name VARCHAR(253) NOT NULL,
			tls VARCHAR(16) NOT NULL,
			not_after TIMESTAMP,
			issuer VARCHAR(255) NOT NULL DEFAULT '',
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (server_id, name),
			FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
		);
CREATE INDEX idx_server_domains_name ON server_domains (name);
INSERT INTO "organizations" ("organization_id", "name", "icon", "color", "created_at") VALUES ('0192a3b4-0000-7000-8000-000000000001', 'frozen org', 'server', '#3b82f6', '2026-08-23 10:11:12');
INSERT INTO "users" ("user_id", "name", "avatar", "created_at", "last_seen") VALUES ('0192a3b4-0000-7000-8000-000000000002', 'Frozen', 'https://example.test/a.png', '2026-08-23 10:11:12', '2026-08-23 10:11:12');
INSERT INTO "organization_users" ("organization_id", "user_id", "role", "created_at") VALUES ('0192a3b4-0000-7000-8000-000000000001', '0192a3b4-0000-7000-8000-000000000002', 'owner', '2026-08-23 10:11:12');
INSERT INTO "user_connected_accounts" ("provider", "external_id", "user_id") VALUES ('local', 'frozen@example.test', '0192a3b4-0000-7000-8000-000000000002');
INSERT INTO "user_credentials" ("user_id", "email", "password_hash", "must_change_password", "updated_at") VALUES ('0192a3b4-0000-7000-8000-000000000002', 'frozen@example.test', '$2a$10$frozenfrozenfrozenfrozenfrozenfrozenfrozenfrozenfroze', TRUE, '2026-08-23 10:11:12');
INSERT INTO "user_tokens" ("token_id", "user_id", "name", "token_prefix", "token_hash", "token_type", "expires_at", "last_used_at", "created_at") VALUES ('0192a3b4-0000-7000-8000-000000000006', '0192a3b4-0000-7000-8000-000000000002', 'ci; deploy', 'wfp_abcd', 'abababababababababababababababababababababababababababababababab', 'pat', '2027-01-02 03:04:05', NULL, '2026-08-23 10:11:12') RETURNING "last_used_at";
INSERT INTO "servers" ("server_id", "organization_id", "name", "created_at", "last_seen") VALUES ('0192a3b4-0000-7000-8000-000000000003', '0192a3b4-0000-7000-8000-000000000001', 'box', '2026-08-23 10:11:12', '2026-08-23 10:11:12');
INSERT INTO "server_capabilities" ("server_id", "name", "value", "updated_at") VALUES ('0192a3b4-0000-7000-8000-000000000003', 'server_ip', '203.0.113.7', '2026-08-23 10:11:12');
INSERT INTO "server_features" ("server_id", "name", "is_enabled") VALUES ('0192a3b4-0000-7000-8000-000000000003', 'ingress', TRUE);
INSERT INTO "server_certificates" ("certificate_id", "server_id", "certificate", "is_active", "expires_at", "created_at") VALUES ('0192a3b4-0000-7000-8000-000000000004', '0192a3b4-0000-7000-8000-000000000003', 'Y2VydA==', TRUE, '2027-01-02 03:04:05', '2026-08-23 10:11:12');
INSERT INTO "server_registrations" ("server_id", "certificate_id", "hostname", "code", "expires_at", "certificate", "certificate_expires_at", "created_at") VALUES ('0192a3b4-0000-7000-8000-000000000007', '0192a3b4-0000-7000-8000-000000000008', 'pending', '123456', '2027-01-02 03:04:05', 'Y2VydA==', '2027-01-02 03:04:05', '2026-08-23 10:11:12');
INSERT INTO "apps" ("app_id", "server_id", "template_id", "name", "version", "icon", "color", "created_at") VALUES ('0192a3b4-0000-7000-8000-000000000005', '0192a3b4-0000-7000-8000-000000000003', NULL, 'grafana', '3', 'chart', '#123456', '2026-08-23 10:11:12') RETURNING "template_id";
INSERT INTO "app_domains" ("domain", "app_id", "server_id", "kind", "ssl", "upstream_port", "updated_at") VALUES ('grafana.example.test', '0192a3b4-0000-7000-8000-000000000005', '0192a3b4-0000-7000-8000-000000000003', 'route', TRUE, 3000, '2026-08-23 10:11:12') RETURNING "target", "code";
INSERT INTO "app_domains" ("domain", "app_id", "server_id", "kind", "ssl", "target", "code", "updated_at") VALUES ('www.example.test', '0192a3b4-0000-7000-8000-000000000005', '0192a3b4-0000-7000-8000-000000000003', 'redirect', FALSE, 'https://grafana.example.test', 301, '2026-08-23 10:11:12') RETURNING "upstream_port";
INSERT INTO "server_domains" ("server_id", "name", "tls", "not_after", "issuer", "updated_at") VALUES ('0192a3b4-0000-7000-8000-000000000003', '*.example.test', 'certificate', '2027-01-02 03:04:05', 'CF Origin', '2026-08-23 10:11:12');
INSERT INTO "server_domains" ("server_id", "name", "tls", "not_after", "issuer", "updated_at") VALUES ('0192a3b4-0000-7000-8000-000000000003', 'example.test', 'auto', NULL, '', '2026-08-23 10:11:12') RETURNING "not_after";
