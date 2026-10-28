-- Users, their connected accounts and local credentials.

-- name: GetUser :one
SELECT * FROM users WHERE user_id = ?;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (user_id, name, avatar, created_at, last_seen)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteUser :exec
DELETE FROM users WHERE user_id = ?;

-- name: GetConnectedAccountUserID :one
SELECT user_id FROM user_connected_accounts WHERE provider = ? AND external_id = ?;

-- name: InsertConnectedAccount :exec
INSERT INTO user_connected_accounts (provider, external_id, user_id)
VALUES (?, ?, ?);

-- name: GetFirstConnectedAccountProvider :one
SELECT provider FROM user_connected_accounts
WHERE user_id = ?
ORDER BY provider ASC, external_id ASC
LIMIT 1;

-- name: InsertCredentials :exec
INSERT INTO user_credentials (user_id, email, password_hash, must_change_password, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetCredentialsByEmail :one
SELECT * FROM user_credentials WHERE email = ?;

-- name: GetCredentialsByUser :one
SELECT * FROM user_credentials WHERE user_id = ?;

-- name: EmailTakenByOther :one
-- EmailTaken for a user adding a password: a password account's email, or
-- the email another user's linked sign-in provider vouched for (the user's
-- own identities do not count).
SELECT EXISTS (
    SELECT 1 FROM user_credentials c WHERE lower(c.email) = CAST(sqlc.arg(email) AS TEXT)
    UNION ALL
    SELECT 1 FROM user_connected_accounts a
    WHERE a.provider <> 'local' AND lower(a.email) = CAST(sqlc.arg(email) AS TEXT) AND a.user_id <> sqlc.arg(user_id)
);

-- name: UserHasIdentityEmail :one
-- Whether one of the user's linked sign-in providers vouched for this
-- (normalized, lower-case) address.
SELECT EXISTS (
    SELECT 1 FROM user_connected_accounts
    WHERE user_id = sqlc.arg(user_id) AND provider <> 'local' AND lower(email) = CAST(sqlc.arg(email) AS TEXT)
);

-- name: EmailTaken :one
-- Whether an account already answers to this (normalized, lower-case)
-- address: a password account's email, or the email a linked sign-in
-- provider vouched for. Compared case-insensitively.
SELECT EXISTS (
    SELECT 1 FROM user_credentials c WHERE lower(c.email) = CAST(sqlc.arg(email) AS TEXT)
    UNION ALL
    SELECT 1 FROM user_connected_accounts a WHERE a.provider <> 'local' AND lower(a.email) = CAST(sqlc.arg(email) AS TEXT)
);

-- LockUserCreation serializes account creation on Postgres; SQLite has
-- one connection and serializes transactions already, so this is a no-op.
-- name: LockUserCreation :exec
SELECT 1;

-- name: UpdatePassword :execrows
UPDATE user_credentials
SET password_hash = ?, must_change_password = ?, updated_at = ?
WHERE user_id = ?;

-- name: RehashPassword :execrows
-- Replaces a verified legacy hash only while it is still the stored one, so a
-- password changed meanwhile is never overwritten with the old one.
UPDATE user_credentials
SET password_hash = ?, updated_at = ?
WHERE user_id = ? AND password_hash = sqlc.arg(old_hash);
