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

-- name: RevokeUserAccessTokens :exec
-- Set-based: one statement revokes the user's live tokens of a kind. The
-- excepted id is the presenting token (an id matching nothing revokes all).
UPDATE access_tokens SET revoked_at = $1 WHERE user_id = $2 AND kind = $3 AND revoked_at IS NULL AND id <> $4;

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

-- name: InsertToken :exec
-- Transitional: the user repository's personal-token code, until the
-- authentication rewrite replaces it. Always a full-scope pat.
INSERT INTO access_tokens (id, user_id, kind, token_hash, scope, name, token_prefix, created_at, last_used_at, expires_at)
VALUES ($1, $2, 'pat', $3, 'full', $4, $5, $6, $7, $8);

-- name: GetTokenByHash :one
-- Transitional, see InsertToken.
SELECT * FROM access_tokens WHERE token_hash = $1 AND kind = 'pat' AND revoked_at IS NULL;

-- name: TouchTokenLastUsed :exec
-- Transitional, see InsertToken.
UPDATE access_tokens SET last_used_at = $1 WHERE id = $2;

-- name: ListTokensByUser :many
-- Transitional, see InsertToken.
SELECT * FROM access_tokens
WHERE user_id = $1 AND kind = 'pat' AND revoked_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: DeleteToken :execrows
-- Transitional, see InsertToken.
DELETE FROM access_tokens WHERE id = $1 AND user_id = $2 AND kind = 'pat';
