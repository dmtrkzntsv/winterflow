-- name: FindUserToken :one
SELECT *
FROM user_tokens
WHERE token = ? AND user_id = ?
  AND (expires_at IS NULL OR expires_at > datetime('now'))
LIMIT 1;


-- name: CreateUserPAT :one
INSERT INTO user_tokens (token_id,
                         user_id,
                         token,
                         token_type,
                         expires_at,
                         created_at)
VALUES (?, ?, ?, 'pat', ?, datetime('now'))
RETURNING *;

-- name: DeleteUserToken :exec
DELETE
FROM user_tokens
WHERE token = ?;
