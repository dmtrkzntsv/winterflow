-- Hub certificates the API trusts (agents pin the active set).

-- name: InsertHubCertificate :exec
INSERT INTO hub_certificates (fingerprint, hub_id, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (fingerprint) DO NOTHING;

-- name: GetHubCertificate :one
SELECT hub_id, retired_at FROM hub_certificates WHERE fingerprint = $1;

-- name: RetireOtherHubCertificates :exec
UPDATE hub_certificates SET retired_at = $1
WHERE hub_id = $2 AND fingerprint <> $3 AND retired_at IS NULL;

-- name: RetireHubCertificatesByHub :execrows
UPDATE hub_certificates SET retired_at = $1
WHERE hub_id = $2 AND retired_at IS NULL;

-- name: RetireHubCertificateByFingerprint :execrows
UPDATE hub_certificates SET retired_at = $1
WHERE fingerprint = $2 AND retired_at IS NULL;

-- name: CountActiveHubCertificatesByHub :one
SELECT COUNT(*) FROM hub_certificates
WHERE hub_id = $1 AND retired_at IS NULL;

-- name: CountHubCertificatesByHub :one
SELECT COUNT(*) FROM hub_certificates WHERE hub_id = $1;

-- name: ListActiveHubCertificates :many
SELECT fingerprint FROM hub_certificates
WHERE retired_at IS NULL
ORDER BY fingerprint;

-- LockHubCertificates serializes Trust per hub id: a transaction-scoped
-- advisory lock, so two concurrent Trusts of one hub id cannot both leave
-- their certificate active (each would miss the other's uncommitted row).
-- name: LockHubCertificates :exec
SELECT pg_advisory_xact_lock(hashtext(sqlc.arg(hub_id)::text));
