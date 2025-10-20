-- name: GetUserConnectedAccount :one
SELECT *
FROM user_connected_accounts
WHERE provider = ?
  AND external_id = ?
LIMIT 1;

-- name: CreateUserConnectedAccount :one
INSERT INTO user_connected_accounts (provider,
                                     external_id,
                                     user_id)
VALUES (?, ?, ?)
RETURNING *;


