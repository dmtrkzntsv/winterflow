-- Personal access tokens (hashed at rest).

-- name: InsertToken :exec
INSERT INTO user_tokens (token_id, user_id, name, token_prefix, token_hash, token_type, expires_at, last_used_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetTokenByHash :one
SELECT * FROM user_tokens WHERE token_hash = $1;

-- name: TouchTokenLastUsed :exec
UPDATE user_tokens SET last_used_at = $1 WHERE token_id = $2;

-- name: ListTokensByUser :many
SELECT * FROM user_tokens
WHERE user_id = $1
ORDER BY created_at DESC, token_id DESC;

-- name: DeleteToken :execrows
DELETE FROM user_tokens WHERE token_id = $1 AND user_id = $2;
