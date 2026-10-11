-- Local (email + password) credentials: one row per local user, bcrypt hash only.

CREATE TABLE IF NOT EXISTS user_credentials (
    user_id              VARCHAR(36) PRIMARY KEY,
    email                VARCHAR(255) NOT NULL UNIQUE,
    password_hash        VARCHAR(100) NOT NULL,
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
);
