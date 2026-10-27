-- Secret links (secret_requests).

-- name: InsertSecretRequest :exec
INSERT INTO secret_requests (id, org_id, server_id, app_id, created_by, names, notes, message, token_hash, status, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?);

-- name: GetSecretRequestByTokenHash :one
SELECT id, org_id, server_id, app_id, created_by, names, notes, message, token_hash, status, ciphertexts,
       created_at, expires_at, submitted_at, used_at, ended_at
FROM secret_requests
WHERE token_hash = ?;

-- name: GetSecretRequest :one
SELECT id, org_id, server_id, app_id, created_by, names, notes, message, token_hash, status, ciphertexts,
       created_at, expires_at, submitted_at, used_at, ended_at
FROM secret_requests
WHERE id = ? AND org_id = ?;

-- name: SubmitSecretRequest :execrows
UPDATE secret_requests
SET status = 'submitted', ciphertexts = sqlc.arg(ciphertexts), submitted_at = sqlc.arg(submitted_at), expires_at = sqlc.arg(expires_at)
WHERE id = sqlc.arg(id) AND status = 'pending' AND expires_at > sqlc.arg(now);

-- name: UseSecretRequest :execrows
UPDATE secret_requests SET status = 'used', ciphertexts = NULL, used_at = ?, ended_at = ?
WHERE id = ? AND status = 'submitted';

-- name: ExpireSecretRequests :execrows
UPDATE secret_requests SET status = 'expired', ciphertexts = NULL, ended_at = ?
WHERE status IN ('pending', 'submitted') AND expires_at <= ?;

-- name: PruneSecretRequests :execrows
DELETE FROM secret_requests
WHERE ended_at IS NOT NULL AND ended_at < ?;
