-- Initial schema (ported from the Bun migration 20241113120001_initial_schema_fixed).

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
