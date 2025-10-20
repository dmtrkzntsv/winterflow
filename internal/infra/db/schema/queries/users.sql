-- name: GetUser :one
SELECT *
FROM users
WHERE user_id = ?
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (user_id,
                   name,
                   avatar,
                   created_at,
                   last_seen)
VALUES (?, ?, ?, datetime('now'), datetime('now'))
RETURNING *;

-- name: UpdateUser :one
UPDATE users
SET name      = ?,
    avatar    = ?,
    last_seen = datetime('now')
WHERE user_id = ?
RETURNING *;
