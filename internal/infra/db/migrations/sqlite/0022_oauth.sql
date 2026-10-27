-- OAuth authorization server for MCP clients: registered clients, one-shot
-- authorization codes and grants (one per approval, holding the rotating
-- refresh token). Access tokens stay in access_tokens (kind oauth), linked to
-- their grant by grant_id.
CREATE TABLE oauth_clients (
    id            VARCHAR(36) PRIMARY KEY,
    name          VARCHAR(255) NOT NULL,
    redirect_uris TEXT NOT NULL,
    secret_hash   VARCHAR(64),
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at  TIMESTAMP
);

CREATE INDEX idx_oauth_clients_unused ON oauth_clients (created_at) WHERE last_used_at IS NULL;

CREATE TABLE oauth_codes (
    code_hash      VARCHAR(64) PRIMARY KEY,
    client_id      VARCHAR(36) NOT NULL,
    user_id        VARCHAR(36) NOT NULL,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    resource       TEXT NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at     TIMESTAMP NOT NULL,
    FOREIGN KEY (client_id) REFERENCES oauth_clients (id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX idx_oauth_codes_expires_at ON oauth_codes (expires_at);

CREATE TABLE oauth_grants (
    id                      VARCHAR(36) PRIMARY KEY,
    user_id                 VARCHAR(36) NOT NULL,
    client_id               VARCHAR(36) NOT NULL,
    refresh_token_hash      VARCHAR(64) NOT NULL UNIQUE,
    prev_refresh_token_hash VARCHAR(64),
    rotated_at              TIMESTAMP,
    created_at              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at            TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at              TIMESTAMP NOT NULL,
    revoked_at              TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
    FOREIGN KEY (client_id) REFERENCES oauth_clients (id) ON DELETE CASCADE
);

CREATE INDEX idx_oauth_grants_user_id ON oauth_grants (user_id);

-- Every refresh-token hash a grant has rotated away, kept as long as the grant
-- row lives: replaying any of them, however many rotations ago, is theft;
-- oauth_grants alone remembers only the latest one.
CREATE TABLE oauth_refresh_tokens_spent (
    token_hash VARCHAR(64) PRIMARY KEY,
    grant_id   VARCHAR(36) NOT NULL,
    spent_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (grant_id) REFERENCES oauth_grants (id) ON DELETE CASCADE
);

CREATE INDEX idx_oauth_refresh_spent_grant_id ON oauth_refresh_tokens_spent (grant_id);

-- Sign-in with a provider (Winterflow as the OAuth client). oauth_login_states
-- holds the in-flight authorization requests (state and PKCE verifier kept
-- server-side; Apple's callback is a cross-site form POST that carries no
-- SameSite cookie); flow_hash is the SHA-256 of a secret handed to the browser
-- that started the flow, so a forged callback cannot sign a victim into an
-- attacker's account. intent is 'login' or 'link' (link_user_id then names the
-- session's user).
CREATE TABLE oauth_login_states (
    state_hash    VARCHAR(64) PRIMARY KEY,
    provider      VARCHAR(16) NOT NULL,
    nonce         TEXT NOT NULL,
    code_verifier TEXT NOT NULL DEFAULT '',
    flow_hash     VARCHAR(64) NOT NULL DEFAULT '',
    intent        VARCHAR(16) NOT NULL,
    return_to     TEXT NOT NULL DEFAULT '',
    link_user_id  VARCHAR(36),
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at    TIMESTAMP NOT NULL
);

CREATE INDEX idx_oauth_login_states_expires_at ON oauth_login_states (expires_at);

-- One-shot codes a client exchanges for a session (kind 'login') or for the
-- identity write a link callback defers to the initiating browser (kind
-- 'link'), so no token travels in a URL.
CREATE TABLE oauth_handoffs (
    code_hash  VARCHAR(64) PRIMARY KEY,
    kind       VARCHAR(16) NOT NULL DEFAULT 'login',
    user_id    VARCHAR(36) NOT NULL,
    provider   VARCHAR(16) NOT NULL,
    subject    TEXT NOT NULL DEFAULT '',
    email      TEXT NOT NULL DEFAULT '',
    flow_hash  VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX idx_oauth_handoffs_expires_at ON oauth_handoffs (expires_at);
