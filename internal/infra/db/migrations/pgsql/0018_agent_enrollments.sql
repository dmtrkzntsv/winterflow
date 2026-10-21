-- Code-based agent enrollment (distributed topology). An agent posts its CSR
-- and polls with a secret (only its SHA-256 is stored); a user claims the
-- 6-character code in the UI, which issues the certificate and creates the
-- server. client_ip is the address that started the enrollment (pending ones
-- are capped per address). servers.hub_id records the hub the server was
-- assigned to.
CREATE TABLE IF NOT EXISTS agent_enrollments (
    enrollment_id UUID PRIMARY KEY,
    poll_secret_hash TEXT NOT NULL UNIQUE,
    csr TEXT NOT NULL,
    hostname TEXT NOT NULL,
    code VARCHAR(6) UNIQUE,
    code_expires_at TIMESTAMPTZ,
    codes_issued INTEGER NOT NULL,
    state TEXT NOT NULL,
    server_id UUID,
    certificate TEXT,
    hub_addr TEXT,
    client_ip TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_enrollments_created_at ON agent_enrollments (created_at);
CREATE INDEX IF NOT EXISTS idx_agent_enrollments_client_ip ON agent_enrollments (client_ip, state);

ALTER TABLE servers ADD COLUMN hub_id TEXT;
CREATE INDEX IF NOT EXISTS idx_servers_hub_id ON servers (hub_id);
