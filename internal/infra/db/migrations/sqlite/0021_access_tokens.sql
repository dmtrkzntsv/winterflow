-- Opaque access tokens: login sessions, personal access tokens (pat) and the
-- OAuth server's tokens (oauth), one table. Only the SHA-256 hash of a token is
-- stored. scope is 'full'; grant_id links an oauth token to its grant so a
-- grant revoke revokes them in one statement (no foreign key: oauth_grants is
-- created by the next migration). Liveness (revoked/expired) is judged in Go.
CREATE TABLE access_tokens (
    id           VARCHAR(36) PRIMARY KEY,
    user_id      VARCHAR(36) NOT NULL,
    kind         VARCHAR(16) NOT NULL,
    token_hash   VARCHAR(64) NOT NULL UNIQUE,
    scope        VARCHAR(16) NOT NULL,
    name         VARCHAR(64),
    token_prefix VARCHAR(16),
    user_agent   TEXT,
    grant_id     VARCHAR(36),
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP,
    expires_at   TIMESTAMP,
    revoked_at   TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX idx_access_tokens_user_kind ON access_tokens (user_id, kind);
CREATE INDEX idx_access_tokens_grant_id ON access_tokens (grant_id);
-- The dead-token purge filters on these cutoffs.
CREATE INDEX idx_access_tokens_revoked_at ON access_tokens (revoked_at);
CREATE INDEX idx_access_tokens_expires_at ON access_tokens (expires_at);

-- Personal access tokens move over unchanged (same id, same hash).
INSERT INTO access_tokens (id, user_id, kind, token_hash, scope, name, token_prefix, created_at, last_used_at, expires_at)
SELECT token_id, user_id, 'pat', token_hash, 'full', name, token_prefix, created_at, last_used_at, expires_at
FROM user_tokens;

DROP TABLE user_tokens;

-- user_connected_accounts is the linked-identities table: it also keeps the
-- provider's email for display and when the link was made (rows that predate
-- this migration read the epoch).
ALTER TABLE user_connected_accounts ADD COLUMN email TEXT;
ALTER TABLE user_connected_accounts ADD COLUMN created_at TIMESTAMP NOT NULL DEFAULT '1970-01-01 00:00:00';

-- One identity per provider per user.
CREATE UNIQUE INDEX uq_user_connected_accounts_user_provider ON user_connected_accounts (user_id, provider);
