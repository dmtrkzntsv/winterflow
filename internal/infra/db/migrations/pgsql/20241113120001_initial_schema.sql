-- Initial schema (ported from the Bun migration 20241113120001_initial_schema_fixed).

CREATE TABLE IF NOT EXISTS organizations (
    organization_id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
    user_id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    avatar TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS organization_users (
    organization_id UUID NOT NULL,
    user_id UUID NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member', 'billing')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, user_id),
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_tokens (
    token_id UUID PRIMARY KEY,
    token VARCHAR(32) NOT NULL UNIQUE,
    token_type VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
    user_id UUID,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_connected_accounts (
    provider VARCHAR(16) NOT NULL,
    external_id TEXT NOT NULL,
    user_id UUID NOT NULL,
    PRIMARY KEY (provider, external_id),
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS servers (
    server_id UUID PRIMARY KEY,
    organization_id UUID NOT NULL,
    name VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen TIMESTAMPTZ,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_capabilities (
    server_id UUID NOT NULL,
    name VARCHAR(64) NOT NULL,
    value TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_features (
    server_id UUID NOT NULL,
    name VARCHAR(64) NOT NULL,
    is_enabled BOOLEAN NOT NULL,
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS server_registrations (
    server_id UUID PRIMARY KEY,
    certificate_id UUID NOT NULL,
    hostname TEXT NOT NULL,
    code VARCHAR(6) NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    certificate TEXT NOT NULL,
    certificate_expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS server_certificates (
    certificate_id UUID PRIMARY KEY,
    server_id UUID NOT NULL,
    certificate TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS apps (
    app_id UUID PRIMARY KEY,
    server_id UUID NOT NULL,
    template_id UUID,
    name VARCHAR(255) NOT NULL,
    version VARCHAR(128) NOT NULL DEFAULT '',
    icon VARCHAR(64) NOT NULL,
    color VARCHAR(7) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
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
