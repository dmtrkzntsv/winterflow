-- OAuth authorization server for MCP clients: registered clients, one-shot
-- authorization codes and grants (one per approval, holding the rotating
-- refresh token). Access tokens stay in access_tokens (kind oauth), linked to
-- their grant by grant_id.
CREATE TABLE oauth_clients (
    id            UUID PRIMARY KEY,
    name          VARCHAR(255) NOT NULL,
    redirect_uris TEXT NOT NULL,
    secret_hash   VARCHAR(64),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ
);

CREATE INDEX idx_oauth_clients_unused ON oauth_clients (created_at) WHERE last_used_at IS NULL;

CREATE TABLE oauth_codes (
    code_hash      VARCHAR(64) PRIMARY KEY,
    client_id      UUID NOT NULL,
    user_id        UUID NOT NULL,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    resource       TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (client_id) REFERENCES oauth_clients (id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);

CREATE INDEX idx_oauth_codes_expires_at ON oauth_codes (expires_at);

CREATE TABLE oauth_grants (
    id                      UUID PRIMARY KEY,
    user_id                 UUID NOT NULL,
    client_id               UUID NOT NULL,
    refresh_token_hash      VARCHAR(64) NOT NULL UNIQUE,
    prev_refresh_token_hash VARCHAR(64),
    rotated_at              TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at              TIMESTAMPTZ NOT NULL,
    revoked_at              TIMESTAMPTZ,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
    FOREIGN KEY (client_id) REFERENCES oauth_clients (id) ON DELETE CASCADE
);

CREATE INDEX idx_oauth_grants_user_id ON oauth_grants (user_id);

-- Every refresh-token hash a grant has rotated away, kept as long as the grant
-- row lives: replaying any of them, however many rotations ago, is theft;
-- oauth_grants alone remembers only the latest one.
CREATE TABLE oauth_refresh_tokens_spent (
    token_hash VARCHAR(64) PRIMARY KEY,
    grant_id   UUID NOT NULL,
    spent_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
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
    link_user_id  UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_oauth_login_states_expires_at ON oauth_login_states (expires_at);

-- The issuer a linked identity was verified against. A subject is unique only
-- per issuer (some IdPs use small integers), so a sign-in through a provider
-- slot now pointing at another issuer must not match it. NULL on rows that
-- predate this column: Google's (fixed issuer).
ALTER TABLE user_connected_accounts ADD COLUMN issuer TEXT;
