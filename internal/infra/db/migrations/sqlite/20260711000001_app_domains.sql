-- The rebuildable ingress index: one row per hostname an app claims (routes
-- plus domain-level redirect sources). The key is the hostname alone here;
-- 20261010000002 moves it to (server_id, domain).

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
