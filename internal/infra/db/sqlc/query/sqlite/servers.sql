-- Servers, their capabilities/features, registrations and certificates.

-- name: ListServersByUser :many
SELECT * FROM servers
WHERE organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY created_at ASC, server_id ASC;

-- name: ListCapabilitiesByUser :many
SELECT c.server_id, c.name, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY c.server_id ASC, c.name ASC;

-- name: ListFeaturesByUser :many
SELECT f.server_id, f.name, f.is_enabled
FROM server_features AS f
JOIN servers AS s ON s.server_id = f.server_id
WHERE s.organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
ORDER BY f.server_id ASC, f.name ASC;

-- name: ListServerUserIDs :many
SELECT user_id FROM organization_users
WHERE organization_id IN (SELECT organization_id FROM servers WHERE server_id = ?);

-- name: UserOwnsServer :one
SELECT EXISTS (
    SELECT 1 FROM servers
    WHERE server_id = ?
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
);

-- name: HasAnyServer :one
SELECT EXISTS (SELECT 1 FROM servers);

-- name: GetFirstServerID :one
SELECT server_id FROM servers ORDER BY created_at ASC, server_id ASC LIMIT 1;

-- name: InsertServer :exec
INSERT INTO servers (server_id, organization_id, name, created_at)
VALUES (?, ?, ?, ?);

-- name: TouchServerLastSeen :exec
UPDATE servers SET last_seen = ? WHERE server_id = ?;

-- name: UpsertCapability :exec
INSERT INTO server_capabilities (server_id, name, value, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET value = excluded.value, updated_at = excluded.updated_at;

-- name: UpsertFeature :exec
INSERT INTO server_features (server_id, name, is_enabled)
VALUES (?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET is_enabled = excluded.is_enabled;

-- name: GetCapability :one
SELECT value FROM server_capabilities WHERE server_id = ? AND name = ?;

-- name: InsertRegistration :exec
INSERT INTO server_registrations (server_id, certificate_id, hostname, code, expires_at, certificate, certificate_expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteRegistration :exec
DELETE FROM server_registrations WHERE server_id = ?;

-- name: DeleteAllRegistrations :exec
DELETE FROM server_registrations;

-- name: GetRegistrationByCode :one
SELECT * FROM server_registrations WHERE code = ?;

-- name: GetLatestRegistrationCode :one
SELECT code FROM server_registrations ORDER BY created_at DESC LIMIT 1;

-- name: InsertCertificate :exec
INSERT INTO server_certificates (certificate_id, server_id, certificate, is_active, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?);
