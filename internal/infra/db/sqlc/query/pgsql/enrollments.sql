-- Agent enrollments (code-based pairing).

-- name: InsertEnrollment :exec
INSERT INTO agent_enrollments (enrollment_id, poll_secret_hash, csr, hostname, code, code_expires_at, codes_issued, state, created_at, client_ip)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetEnrollmentBySecretHash :one
SELECT * FROM agent_enrollments WHERE poll_secret_hash = $1;

-- name: GetEnrollmentByCode :one
SELECT * FROM agent_enrollments WHERE code = $1;

-- name: GetEnrollmentByID :one
SELECT * FROM agent_enrollments WHERE enrollment_id = $1;

-- name: ReplaceEnrollmentCode :execrows
UPDATE agent_enrollments SET code = $1, code_expires_at = $2, codes_issued = $3
WHERE enrollment_id = $4 AND state = 'pending';

-- name: MarkEnrollmentFailed :exec
UPDATE agent_enrollments SET state = 'failed', code = NULL, code_expires_at = NULL
WHERE enrollment_id = $1 AND state = 'pending';

-- name: ClaimEnrollment :execrows
UPDATE agent_enrollments
SET state = 'claimed', code = NULL, code_expires_at = NULL, server_id = $1, certificate = $2, hub_addr = $3, claimed_at = $4
WHERE enrollment_id = $5 AND code = $6 AND state = 'pending' AND code_expires_at > $7;

-- name: InsertServerWithHub :exec
INSERT INTO servers (server_id, organization_id, name, created_at, hub_id)
VALUES ($1, $2, $3, $4, $5);

-- name: CountPendingEnrollments :one
SELECT COUNT(*) FROM agent_enrollments WHERE state = 'pending';

-- name: CountPendingEnrollmentsByIP :one
SELECT COUNT(*) FROM agent_enrollments WHERE state = 'pending' AND client_ip = $1;

-- name: PruneEnrollments :exec
DELETE FROM agent_enrollments
WHERE (state IN ('pending', 'failed') AND created_at < $1)
   OR (state = 'claimed' AND claimed_at < $2);
