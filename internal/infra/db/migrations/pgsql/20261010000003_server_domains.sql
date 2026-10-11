-- The API's index of each agent's domain registrations (the agent's domains/
-- dir is the source of truth), rebuilt from domain.list. Timestamps are plain
-- TIMESTAMP on both engines, as the Bun migration created them.

CREATE TABLE server_domains (
    server_id UUID NOT NULL,
    name VARCHAR(253) NOT NULL,
    tls VARCHAR(16) NOT NULL,
    not_after TIMESTAMP,
    issuer VARCHAR(255) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_server_domains_name ON server_domains (name);
