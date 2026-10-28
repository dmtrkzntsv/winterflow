-- Access tokens (access_tokens): login sessions, personal access tokens
-- (kind pat) and OAuth server tokens (kind oauth), hashed at rest. Liveness
-- (revoked/expired) is evaluated in Go, not SQL, so list/get return raw rows.

-- name: InsertAccessToken :exec
INSERT INTO access_tokens (id, user_id, kind, token_hash, scope, name, token_prefix, user_agent, grant_id, created_at, last_used_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAccessTokenByHash :one
SELECT * FROM access_tokens WHERE token_hash = ?;

-- name: GetAccessTokenByID :one
SELECT * FROM access_tokens WHERE id = ?;

-- name: TouchAccessToken :execrows
-- The sliding-expiry touch never writes revoked_at and never touches a revoked
-- row, so a request that read the row before a revoke cannot undo it.
UPDATE access_tokens SET last_used_at = ?, expires_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeAccessToken :exec
UPDATE access_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeUserAccessToken :execrows
-- One of the user's live tokens of a kind; zero rows when it is another
-- user's, another kind, unknown or already revoked.
UPDATE access_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND kind = ? AND revoked_at IS NULL;

-- name: RevokeUserAccessTokens :exec
-- Set-based: one statement revokes the user's live tokens of a kind.
UPDATE access_tokens SET revoked_at = ? WHERE user_id = ? AND kind = ? AND revoked_at IS NULL;

-- name: RevokeAccessTokensByGrant :exec
UPDATE access_tokens SET revoked_at = ? WHERE grant_id = ? AND revoked_at IS NULL;

-- name: ListAccessTokensByUser :many
SELECT * FROM access_tokens WHERE user_id = ? AND kind = ? ORDER BY created_at, id;

-- name: DeleteAccessToken :exec
DELETE FROM access_tokens WHERE id = ?;

-- name: DeleteDeadAccessTokens :execrows
DELETE FROM access_tokens
WHERE (revoked_at IS NOT NULL AND revoked_at < ?)
   OR (expires_at IS NOT NULL AND expires_at < ?);
