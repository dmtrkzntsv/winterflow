-- Code-based agent enrollment (distributed topology). An agent posts its CSR
-- and polls with a secret (only its SHA-256 is stored); a user claims the
-- 6-character code in the UI, which issues the certificate and creates the
-- server. servers.hub_id records the hub the server was assigned to.
CREATE TABLE IF NOT EXISTS agent_enrollments (
    enrollment_id VARCHAR(36) PRIMARY KEY,
    poll_secret_hash TEXT NOT NULL UNIQUE,
    csr TEXT NOT NULL,
    hostname TEXT NOT NULL,
    code VARCHAR(6) UNIQUE,
    code_expires_at TIMESTAMP,
    codes_issued INTEGER NOT NULL,
    state TEXT NOT NULL,
    server_id VARCHAR(36),
    certificate TEXT,
    hub_addr TEXT,
    claimed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_enrollments_created_at ON agent_enrollments (created_at);

ALTER TABLE servers ADD COLUMN hub_id TEXT;
CREATE INDEX IF NOT EXISTS idx_servers_hub_id ON servers (hub_id);
