-- Replace the never-used v1 user_tokens table with the PAT shape: hashed
-- token, display prefix, name, last-used. The v1 table never held rows.

DROP TABLE IF EXISTS user_tokens;

CREATE TABLE user_tokens (
    token_id     VARCHAR(36) PRIMARY KEY,
    user_id      VARCHAR(36) NOT NULL,
    name         VARCHAR(64) NOT NULL,
    token_prefix VARCHAR(16) NOT NULL,
    token_hash   VARCHAR(64) NOT NULL UNIQUE,
    token_type   VARCHAR(16) NOT NULL CHECK (token_type IN ('pat')),
    expires_at   TIMESTAMP,
    last_used_at TIMESTAMP,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_tokens_user_id ON user_tokens (user_id);
