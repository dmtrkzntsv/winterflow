-- Access tokens (access_tokens): login sessions, personal access tokens
-- (kind pat) and OAuth server tokens (kind oauth), hashed at rest. Liveness
-- (revoked/expired) is evaluated in Go, not SQL, so list/get return raw rows.

-- name: InsertAccessToken :exec
INSERT INTO access_tokens (id, user_id, kind, token_hash, scope, name, token_prefix, user_agent, grant_id, created_at, last_used_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: GetAccessTokenByHash :one
SELECT * FROM access_tokens WHERE token_hash = $1;

-- name: GetAccessTokenByID :one
SELECT * FROM access_tokens WHERE id = $1;

-- name: TouchAccessToken :execrows
-- The sliding-expiry touch never writes revoked_at and never touches a revoked
-- row, so a request that read the row before a revoke cannot undo it.
UPDATE access_tokens SET last_used_at = $1, expires_at = $2 WHERE id = $3 AND revoked_at IS NULL;

-- name: RevokeAccessToken :exec
UPDATE access_tokens SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL;

-- name: RevokeUserAccessToken :execrows
-- One of the user's live tokens of a kind; zero rows when it is another
-- user's, another kind, unknown or already revoked.
UPDATE access_tokens SET revoked_at = $1 WHERE id = $2 AND user_id = $3 AND kind = $4 AND revoked_at IS NULL;

-- name: RevokeUserAccessTokens :exec
-- Set-based: one statement revokes the user's live tokens of a kind.
UPDATE access_tokens SET revoked_at = $1 WHERE user_id = $2 AND kind = $3 AND revoked_at IS NULL;

-- name: RevokeAccessTokensByGrant :exec
UPDATE access_tokens SET revoked_at = $1 WHERE grant_id = $2 AND revoked_at IS NULL;

-- name: ListAccessTokensByUser :many
SELECT * FROM access_tokens WHERE user_id = $1 AND kind = $2 ORDER BY created_at, id;

-- name: DeleteAccessToken :exec
DELETE FROM access_tokens WHERE id = $1;

-- name: DeleteDeadAccessTokens :execrows
DELETE FROM access_tokens
WHERE (revoked_at IS NOT NULL AND revoked_at < $1)
   OR (expires_at IS NOT NULL AND expires_at < $2);
