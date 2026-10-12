-- Org- and server-level shared variables. Plain values live here; a secret
-- keeps one ECIES ciphertext per server it was encrypted for (browser
-- fan-out; the API never sees plaintext). servers.vars_revision counts the
-- changes that affect a server, so a behind agent is pushed again.

ALTER TABLE servers ADD COLUMN vars_revision INTEGER NOT NULL DEFAULT 0;

CREATE TABLE shared_variables (
    id VARCHAR(36) PRIMARY KEY,
    org_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36),
    name VARCHAR(255) NOT NULL,
    is_secret BOOLEAN NOT NULL DEFAULT FALSE,
    value TEXT,
    description TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    updated_by VARCHAR(36) NOT NULL DEFAULT '',
    UNIQUE (org_id, server_id, name),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

-- NULLs are distinct in a UNIQUE constraint, so org scope (server_id NULL)
-- needs its own partial index.
CREATE UNIQUE INDEX idx_shared_variables_org_name ON shared_variables (org_id, name) WHERE server_id IS NULL;

CREATE TABLE shared_secret_ciphertexts (
    variable_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    ciphertext TEXT NOT NULL,
    PRIMARY KEY (variable_id, server_id),
    FOREIGN KEY (variable_id) REFERENCES shared_variables (id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_shared_secret_ciphertexts_server ON shared_secret_ciphertexts (server_id);
