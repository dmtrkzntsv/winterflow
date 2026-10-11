-- migrate:foreign_keys=off
-- A hostname becomes unique per server instead of globally: the key moves
-- from (domain) to (server_id, domain). SQLite cannot alter a primary key, so
-- the table is rebuilt; the runner runs this file in one transaction with
-- foreign keys off and checks PRAGMA foreign_key_check before committing.

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
