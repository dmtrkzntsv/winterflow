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

-- name: EmailTaken :one
SELECT EXISTS (SELECT 1 FROM user_credentials WHERE email = ?);

-- name: UpdatePassword :execrows
UPDATE user_credentials
SET password_hash = ?, must_change_password = ?, updated_at = ?
WHERE user_id = ?;
