-- Hub certificates the API trusts (agents pin the active set).

-- name: InsertHubCertificate :exec
INSERT INTO hub_certificates (fingerprint, hub_id, created_at)
VALUES (?, ?, ?)
ON CONFLICT (fingerprint) DO NOTHING;

-- name: GetHubCertificate :one
SELECT hub_id, retired_at FROM hub_certificates WHERE fingerprint = ?;

-- name: RetireOtherHubCertificates :exec
UPDATE hub_certificates SET retired_at = ?
WHERE hub_id = ? AND fingerprint <> ? AND retired_at IS NULL;

-- name: RetireHubCertificatesByHub :execrows
UPDATE hub_certificates SET retired_at = ?
WHERE hub_id = ? AND retired_at IS NULL;

-- name: RetireHubCertificateByFingerprint :execrows
UPDATE hub_certificates SET retired_at = ?
WHERE fingerprint = ? AND retired_at IS NULL;

-- name: ListActiveHubCertificates :many
SELECT fingerprint FROM hub_certificates
WHERE retired_at IS NULL
ORDER BY fingerprint;

-- LockHubCertificates serializes Trust per hub id on Postgres; SQLite has
-- one connection and serializes transactions already, so this is a no-op.
-- name: LockHubCertificates :exec
SELECT CAST(sqlc.arg(hub_id) AS TEXT);
