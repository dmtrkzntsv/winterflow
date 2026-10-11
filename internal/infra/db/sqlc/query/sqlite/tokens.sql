-- Personal access tokens (hashed at rest).

-- name: InsertToken :exec
INSERT INTO user_tokens (token_id, user_id, name, token_prefix, token_hash, token_type, expires_at, last_used_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTokenByHash :one
SELECT * FROM user_tokens WHERE token_hash = ?;

-- name: TouchTokenLastUsed :exec
UPDATE user_tokens SET last_used_at = ? WHERE token_id = ?;

-- name: ListTokensByUser :many
SELECT * FROM user_tokens
WHERE user_id = ?
ORDER BY created_at DESC, token_id DESC;

-- name: DeleteToken :execrows
DELETE FROM user_tokens WHERE token_id = ? AND user_id = ?;
