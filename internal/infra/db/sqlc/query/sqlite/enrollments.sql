-- Agent enrollments (code-based pairing).

-- name: InsertEnrollment :exec
INSERT INTO agent_enrollments (enrollment_id, poll_secret_hash, csr, hostname, code, code_expires_at, codes_issued, state, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetEnrollmentBySecretHash :one
SELECT * FROM agent_enrollments WHERE poll_secret_hash = ?;

-- name: GetEnrollmentByCode :one
SELECT * FROM agent_enrollments WHERE code = ?;

-- name: GetEnrollmentByID :one
SELECT * FROM agent_enrollments WHERE enrollment_id = ?;

-- name: ReplaceEnrollmentCode :execrows
UPDATE agent_enrollments SET code = ?, code_expires_at = ?, codes_issued = ?
WHERE enrollment_id = ? AND state = 'pending';

-- name: MarkEnrollmentFailed :exec
UPDATE agent_enrollments SET state = 'failed', code = NULL, code_expires_at = NULL
WHERE enrollment_id = ? AND state = 'pending';

-- name: ClaimEnrollment :execrows
UPDATE agent_enrollments
SET state = 'claimed', code = NULL, code_expires_at = NULL, server_id = ?, certificate = ?, claimed_at = ?
WHERE enrollment_id = ? AND code = ? AND state = 'pending' AND code_expires_at > ?;

-- name: InsertServerWithHub :exec
INSERT INTO servers (server_id, organization_id, name, created_at, hub_id)
VALUES (?, ?, ?, ?, ?);

-- name: CountPendingEnrollments :one
SELECT COUNT(*) FROM agent_enrollments WHERE state = 'pending';

-- name: PruneEnrollments :exec
DELETE FROM agent_enrollments
WHERE (state IN ('pending', 'failed') AND created_at < ?)
   OR (state = 'claimed' AND claimed_at < ?);
