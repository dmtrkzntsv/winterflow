-- +migrate Up
-- Enable foreign key constraints
PRAGMA foreign_keys = ON;


-- Create an Organization table
CREATE TABLE organizations
(
    organization_id     CHAR(36) PRIMARY KEY,
    name                VARCHAR(255) NOT NULL,
    subscription_status VARCHAR(6)   NOT NULL CHECK (subscription_status IN ('paid', 'unpaid')) DEFAULT 'unpaid',
    created_at          DATETIME     NOT NULL
);

-- Create an Organization Users table
CREATE TABLE organization_users
(
    organization_id CHAR(36)    NOT NULL,
    user_id         CHAR(36)    NOT NULL,
    role            VARCHAR(32) NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'billing')) DEFAULT 'member',
    created_at      DATETIME    NOT NULL,
    PRIMARY KEY (organization_id, user_id),
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

-- Create Users table
CREATE TABLE users
(
    user_id    CHAR(36) PRIMARY KEY,
    name       VARCHAR(255) NOT NULL,
    avatar     TEXT,
    created_at DATETIME     NOT NULL,
    last_seen  DATETIME     NOT NULL
);

-- Create UserTokens table
CREATE TABLE user_tokens
(
    token_id   CHAR(36) PRIMARY KEY,
    token      CHAR(32)    NOT NULL UNIQUE,
    token_type VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
    user_id    CHAR(36),
    expires_at DATETIME,
    created_at DATETIME    NOT NULL,
    FOREIGN KEY (user_id)
        REFERENCES users (user_id)
        ON DELETE CASCADE
);

-- Create UserConnectedAccounts table
CREATE TABLE user_connected_accounts
(
    provider    VARCHAR(16) NOT NULL,
    external_id TEXT        NOT NULL,
    user_id     CHAR(36)    NOT NULL,
    PRIMARY KEY (provider, external_id),
    FOREIGN KEY (user_id)
        REFERENCES users (user_id)
        ON DELETE CASCADE
);

-- Create Agents table
CREATE TABLE agents
(
    agent_id            CHAR(36) PRIMARY KEY,
    organization_id     CHAR(36)    NOT NULL,
    name                VARCHAR(64) NOT NULL,
    subscription_status VARCHAR(8)  NOT NULL CHECK (subscription_status IN ('paid', 'unpaid')) DEFAULT 'unpaid',
    created_at          DATETIME    NOT NULL,
    last_seen           DATETIME,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

-- Create AgentCapabilities table
CREATE TABLE agent_capabilities
(
    agent_id   CHAR(36)    NOT NULL,
    name       VARCHAR(64) NOT NULL,
    value      TEXT        NOT NULL DEFAULT '',
    updated_at DATETIME    NOT NULL,
    PRIMARY KEY (agent_id, name),
    FOREIGN KEY (agent_id) REFERENCES agents (agent_id) ON DELETE CASCADE
);

-- Create AgentFeatures table
CREATE TABLE agent_features
(
    agent_id   CHAR(36)    NOT NULL,
    name       VARCHAR(64) NOT NULL,
    is_enabled BOOLEAN     NOT NULL,
    PRIMARY KEY (agent_id, name),
    FOREIGN KEY (agent_id) REFERENCES agents (agent_id) ON DELETE CASCADE
);

-- Create AgentRegistrations table
CREATE TABLE agent_registrations
(
    agent_id               CHAR(36) PRIMARY KEY,
    certificate_id         CHAR(36) NOT NULL,
    hostname               TEXT     NOT NULL,
    code                   CHAR(6)  NOT NULL UNIQUE,
    expires_at             DATETIME NOT NULL,
    certificate            TEXT     NOT NULL,
    certificate_expires_at DATETIME NOT NULL,
    created_at             DATETIME NOT NULL
);

-- Create AgentCertificates table
CREATE TABLE agent_certificates
(
    certificate_id CHAR(36) PRIMARY KEY,
    agent_id       CHAR(36) NOT NULL,
    certificate    TEXT     NOT NULL,
    is_active      BOOLEAN  NOT NULL DEFAULT true,
    expires_at     DATETIME NOT NULL,
    created_at     DATETIME NOT NULL,
    FOREIGN KEY (agent_id) REFERENCES agents (agent_id) ON DELETE CASCADE
);

-- Create Release Versions table
CREATE TABLE release_versions
(
    version_number CHAR(9) PRIMARY KEY,
    name           VARCHAR(32) NOT NULL UNIQUE,
    is_beta        BOOLEAN     NOT NULL DEFAULT false,
    message        TEXT,
    url            TEXT        NOT NULL
);

-- Create Apps table
CREATE TABLE apps
(
    app_id      CHAR(36) PRIMARY KEY,
    agent_id    CHAR(36)     NOT NULL,
    template_id CHAR(36)              DEFAULT NULL,
    name        VARCHAR(255) NOT NULL,
    version     VARCHAR(128) NOT NULL DEFAULT '',
    icon        VARCHAR(64)  NOT NULL,
    color       CHAR(7)      NOT NULL,
    created_at  DATETIME     NOT NULL,
    FOREIGN KEY (agent_id) REFERENCES agents (agent_id) ON DELETE CASCADE
);

-- Create indexes for better query performance
CREATE INDEX idx_user_tokens_token ON user_tokens (token);
CREATE INDEX idx_user_connected_accounts_user_id ON user_connected_accounts (user_id);
CREATE INDEX idx_agent_registrations_code ON agent_registrations (code);
CREATE INDEX idx_agent_registrations_expires_at ON agent_registrations (expires_at);
CREATE INDEX idx_agent_certificates_agent_id ON agent_certificates (agent_id);
CREATE INDEX idx_agent_certificates_is_active_expires_at ON agent_certificates (is_active, expires_at);
CREATE INDEX idx_apps_agent_id ON apps (agent_id);
CREATE INDEX idx_apps_template_id ON apps (template_id);


-- +migrate Down
DROP TABLE IF EXISTS release_versions;
DROP TABLE IF EXISTS agent_access;
DROP TABLE IF EXISTS agent_registrations;
DROP TABLE IF EXISTS agent_certificates;
DROP TABLE IF EXISTS agent_capabilities;
DROP TABLE IF EXISTS agent_features;
DROP TABLE IF EXISTS agents;
DROP TABLE IF EXISTS user_connected_accounts;
DROP TABLE IF EXISTS user_tokens;
DROP TABLE IF EXISTS apps;
DROP TABLE IF EXISTS organization_users;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;

