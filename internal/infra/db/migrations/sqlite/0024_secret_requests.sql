-- Secret links: an MCP agent asks the user for an app's secret values; the
-- user opens /secrets/<token> signed in as a member of org_id, the browser
-- encrypts each value to the server's key and the API stores only the
-- ciphertexts (a JSON object NAME -> ECIES ciphertext), which the agent's
-- next save_app (secrets_from) puts in as those variables' values.
-- status is pending, submitted, claimed, used or expired. expires_at is the
-- current deadline: 24 h after creation while pending, 24 h after submission
-- while submitted. A save claims a submitted request (claim_id, until
-- claimed_until) so only one save dispatches its ciphertexts; it then ends
-- it used, or releases it to submitted; a claim past claimed_until counts as
-- released. ended_at is set when the request becomes used or expired; the
-- row is pruned a week later. Only the token's SHA-256 is stored.
CREATE TABLE secret_requests (
    id           VARCHAR(36) PRIMARY KEY,
    org_id       VARCHAR(36) NOT NULL,
    server_id    VARCHAR(36) NOT NULL,
    app_id       TEXT,
    created_by   VARCHAR(36) NOT NULL,
    names        TEXT NOT NULL,
    notes        TEXT NOT NULL,
    message      TEXT NOT NULL,
    token_hash   VARCHAR(64) NOT NULL UNIQUE,
    status       VARCHAR(16) NOT NULL,
    ciphertexts  TEXT,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   TIMESTAMP NOT NULL,
    submitted_at TIMESTAMP,
    used_at      TIMESTAMP,
    ended_at     TIMESTAMP,
    claim_id     VARCHAR(36),
    claimed_until TIMESTAMP,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE,
    FOREIGN KEY (created_by) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX idx_secret_requests_status_expires ON secret_requests (status, expires_at);
CREATE INDEX idx_secret_requests_ended_at ON secret_requests (ended_at);
