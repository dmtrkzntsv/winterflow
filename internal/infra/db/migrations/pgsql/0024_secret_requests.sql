-- Secret links: an MCP agent asks the user for an app's secret values; the
-- user opens /secrets/<token> signed in as a member of org_id, the browser
-- encrypts each value to the server's key and the API stores only the
-- ciphertexts (a JSON object NAME -> ECIES ciphertext), which the agent's
-- next save_app (secrets_from) puts in as those variables' values.
-- status is pending, submitted, claimed, dispatched, used or expired.
-- expires_at is the current deadline: 24 h after creation while pending, 24 h
-- after submission while submitted. A save claims a submitted request
-- (claim_id, until claimed_until) so only one save dispatches its
-- ciphertexts; a claim past claimed_until counts as released. Right before
-- it sends the save, the claim's holder marks it dispatched, which no claim
-- takes again: the holder then ends it used (or, when the send failed,
-- releases it to submitted); a dispatched request whose holder went away is
-- ended used by the sweep once claimed_until passes. A refused save
-- releases its claim to submitted. ended_at is set when the request becomes
-- used or expired; the row is pruned a week later. Only the token's SHA-256
-- is stored.
CREATE TABLE secret_requests (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL,
    server_id    UUID NOT NULL,
    app_id       TEXT,
    created_by   UUID NOT NULL,
    names        TEXT NOT NULL,
    notes        TEXT NOT NULL,
    message      TEXT NOT NULL,
    token_hash   VARCHAR(64) NOT NULL UNIQUE,
    status       VARCHAR(16) NOT NULL,
    ciphertexts  TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL,
    submitted_at TIMESTAMPTZ,
    used_at      TIMESTAMPTZ,
    ended_at     TIMESTAMPTZ,
    claim_id     VARCHAR(36),
    claimed_until TIMESTAMPTZ,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE,
    FOREIGN KEY (created_by) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX idx_secret_requests_status_expires ON secret_requests (status, expires_at);
CREATE INDEX idx_secret_requests_ended_at ON secret_requests (ended_at);
