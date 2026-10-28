-- Secret links (secret_requests).

-- name: InsertSecretRequest :exec
INSERT INTO secret_requests (id, org_id, server_id, app_id, created_by, names, notes, message, token_hash, status, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending', $10, $11);

-- name: GetSecretRequestByTokenHash :one
SELECT id, org_id, server_id, app_id, created_by, names, notes, message, token_hash, status, ciphertexts,
       created_at, expires_at, submitted_at, used_at, ended_at, claim_id, claimed_until
FROM secret_requests
WHERE token_hash = $1;

-- name: GetSecretRequest :one
SELECT id, org_id, server_id, app_id, created_by, names, notes, message, token_hash, status, ciphertexts,
       created_at, expires_at, submitted_at, used_at, ended_at, claim_id, claimed_until
FROM secret_requests
WHERE id = $1 AND org_id = $2;

-- name: SubmitSecretRequest :execrows
UPDATE secret_requests
SET status = 'submitted', ciphertexts = sqlc.arg(ciphertexts), submitted_at = sqlc.arg(submitted_at), expires_at = sqlc.arg(expires_at)
WHERE id = sqlc.arg(id) AND status = 'pending' AND expires_at > sqlc.arg(now);

-- name: ClaimSecretRequest :execrows
UPDATE secret_requests SET status = 'claimed', claim_id = sqlc.arg(claim_id), claimed_until = sqlc.arg(claimed_until)
WHERE id = sqlc.arg(id) AND expires_at > sqlc.arg(now)
  AND (status = 'submitted' OR (status = 'claimed' AND claimed_until <= sqlc.arg(now)));

-- name: DispatchSecretRequest :execrows
UPDATE secret_requests SET status = 'dispatched', claimed_until = sqlc.arg(claimed_until)
WHERE id = sqlc.arg(id) AND status = 'claimed' AND claim_id = sqlc.arg(claim_id);

-- name: UseSecretRequest :execrows
UPDATE secret_requests
SET status = 'used', ciphertexts = NULL, used_at = sqlc.arg(used_at), ended_at = sqlc.arg(used_at), claim_id = NULL, claimed_until = NULL
WHERE id = sqlc.arg(id) AND status = 'dispatched' AND claim_id = sqlc.arg(claim_id);

-- name: ReleaseSecretRequest :execrows
-- Only a claim that was never dispatched returns to submitted: a dispatched
-- request's ciphertexts may have reached the agent.
UPDATE secret_requests SET status = 'submitted', claim_id = NULL, claimed_until = NULL
WHERE id = sqlc.arg(id) AND status = 'claimed' AND claim_id = sqlc.arg(claim_id);

-- name: ExpireSecretRequests :execrows
UPDATE secret_requests SET status = 'expired', ciphertexts = NULL, ended_at = sqlc.arg(now), claim_id = NULL, claimed_until = NULL
WHERE expires_at <= sqlc.arg(now)
  AND (status IN ('pending', 'submitted') OR (status = 'claimed' AND claimed_until <= sqlc.arg(now)));

-- name: EndDispatchedSecretRequests :execrows
UPDATE secret_requests
SET status = 'used', ciphertexts = NULL, used_at = sqlc.arg(now), ended_at = sqlc.arg(now), claim_id = NULL, claimed_until = NULL
WHERE status = 'dispatched' AND claimed_until <= sqlc.arg(now);

-- name: PruneSecretRequests :execrows
DELETE FROM secret_requests
WHERE ended_at IS NOT NULL AND ended_at < $1;
