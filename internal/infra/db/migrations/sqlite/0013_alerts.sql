-- Alerts (monitoring PR 2): org-wide notification channels and rules,
-- per-target overrides, the live state of each (rule, target) and the
-- event history. A target is (server_id, app_id) with app_id '' for the
-- server itself, as in metric_samples. Channel URLs carry secrets and are
-- stored AES-GCM encrypted (key derived from JWT_SECRET).

ALTER TABLE organizations ADD COLUMN alert_defaults_seeded BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE notification_channels (
    channel_id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    name VARCHAR(64) NOT NULL,
    service VARCHAR(32) NOT NULL,
    fields TEXT NOT NULL DEFAULT '{}',
    url_encrypted TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_error TEXT NOT NULL DEFAULT '',
    last_sent_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE INDEX idx_notification_channels_org ON notification_channels (organization_id);

CREATE TABLE alert_rules (
    rule_id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    target_kind VARCHAR(16) NOT NULL,
    metric VARCHAR(32) NOT NULL,
    threshold REAL NOT NULL DEFAULT 0,
    duration_minutes INTEGER NOT NULL,
    channel_ids TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE INDEX idx_alert_rules_org ON alert_rules (organization_id);

CREATE TABLE alert_overrides (
    rule_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    threshold REAL,
    duration_minutes INTEGER,
    channel_ids TEXT,
    PRIMARY KEY (rule_id, server_id, app_id),
    FOREIGN KEY (rule_id) REFERENCES alert_rules (rule_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE alert_states (
    rule_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    state VARCHAR(8) NOT NULL,
    since TIMESTAMP NOT NULL,
    value REAL NOT NULL DEFAULT 0,
    threshold REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (rule_id, server_id, app_id),
    FOREIGN KEY (rule_id) REFERENCES alert_rules (rule_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE TABLE alert_events (
    event_id VARCHAR(36) PRIMARY KEY,
    organization_id VARCHAR(36) NOT NULL,
    rule_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    server_name VARCHAR(64) NOT NULL DEFAULT '',
    app_name VARCHAR(255) NOT NULL DEFAULT '',
    target_kind VARCHAR(16) NOT NULL,
    metric VARCHAR(32) NOT NULL,
    kind VARCHAR(8) NOT NULL,
    value REAL NOT NULL DEFAULT 0,
    threshold REAL NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    message TEXT NOT NULL,
    deliveries TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMP NOT NULL,
    FOREIGN KEY (organization_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_alert_events_org_created ON alert_events (organization_id, created_at);
CREATE INDEX idx_alert_events_created ON alert_events (created_at);
