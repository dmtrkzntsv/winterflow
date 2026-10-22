-- One-time tokens a new hub redeems at POST /api/v1/hub/enroll to get its
-- server certificate. Only the SHA-256 of the token is stored; a row is
-- single use (used_at / used_by_hub) and pruned a day after expiry or use.
CREATE TABLE IF NOT EXISTS hub_join_tokens (
    token_hash TEXT PRIMARY KEY,
    expires_at TIMESTAMP NOT NULL,
    used_at TIMESTAMP,
    used_by_hub TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
