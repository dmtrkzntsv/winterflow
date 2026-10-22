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

-- name: ListActiveHubCertificates :many
SELECT fingerprint FROM hub_certificates
WHERE retired_at IS NULL
ORDER BY fingerprint;
