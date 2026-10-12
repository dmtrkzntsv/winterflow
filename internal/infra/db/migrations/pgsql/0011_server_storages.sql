-- The API's index of each agent's storages (the agent's storages/ dir is the
-- source of truth): name, type and location, never a secret. Rebuilt from
-- storage.list. location's 1024 cap matches the model's normalized cap, so
-- one oversized row can never fail a whole index sync. Timestamps are plain
-- TIMESTAMP on both engines, like server_domains.

CREATE TABLE server_storages (
    server_id UUID NOT NULL,
    name VARCHAR(32) NOT NULL,
    type VARCHAR(16) NOT NULL,
    location VARCHAR(1024) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (server_id, name),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);
