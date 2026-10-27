-- Users, their connected accounts and local credentials.

-- name: GetUser :one
SELECT * FROM users WHERE user_id = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (user_id, name, avatar, created_at, last_seen)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteUser :exec
DELETE FROM users WHERE user_id = $1;

-- name: GetConnectedAccountUserID :one
SELECT user_id FROM user_connected_accounts WHERE provider = $1 AND external_id = $2;

-- name: InsertConnectedAccount :exec
INSERT INTO user_connected_accounts (provider, external_id, user_id)
VALUES ($1, $2, $3);

-- name: GetFirstConnectedAccountProvider :one
SELECT provider FROM user_connected_accounts
WHERE user_id = $1
ORDER BY provider ASC, external_id ASC
LIMIT 1;

-- name: InsertCredentials :exec
INSERT INTO user_credentials (user_id, email, password_hash, must_change_password, updated_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetCredentialsByEmail :one
SELECT * FROM user_credentials WHERE email = $1;

-- name: GetCredentialsByUser :one
SELECT * FROM user_credentials WHERE user_id = $1;

-- name: EmailTaken :one
SELECT EXISTS (SELECT 1 FROM user_credentials WHERE email = $1);

-- name: UpdatePassword :execrows
UPDATE user_credentials
SET password_hash = $1, must_change_password = $2, updated_at = $3
WHERE user_id = $4;

-- name: RehashPassword :execrows
-- Replaces a verified legacy hash only while it is still the stored one, so a
-- password changed meanwhile is never overwritten with the old one.
UPDATE user_credentials
SET password_hash = $1, updated_at = $2
WHERE user_id = $3 AND password_hash = sqlc.arg(old_hash);
