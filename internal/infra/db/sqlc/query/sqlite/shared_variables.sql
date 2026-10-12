-- Org- and server-level shared variables and their per-server ciphertexts.

-- name: ListOrgSharedVariables :many
SELECT * FROM shared_variables WHERE org_id = ? AND server_id IS NULL ORDER BY name ASC;

-- name: ListServerSharedVariables :many
SELECT * FROM shared_variables WHERE server_id = ? ORDER BY name ASC;

-- name: GetSharedVariable :one
SELECT * FROM shared_variables WHERE id = ?;

-- name: GetOrgSharedVariableByName :one
SELECT * FROM shared_variables WHERE org_id = ? AND server_id IS NULL AND name = ?;

-- name: GetServerSharedVariableByName :one
SELECT * FROM shared_variables WHERE server_id = ? AND name = ?;

-- name: InsertSharedVariable :exec
INSERT INTO shared_variables (id, org_id, server_id, name, is_secret, value, description, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateSharedVariable :exec
UPDATE shared_variables SET value = ?, description = ?, updated_at = ?, updated_by = ? WHERE id = ?;

-- name: DeleteSharedVariable :exec
DELETE FROM shared_variables WHERE id = ?;

-- name: ListCiphertextServersByOrg :many
SELECT c.variable_id, c.server_id
FROM shared_secret_ciphertexts AS c
JOIN shared_variables AS v ON v.id = c.variable_id
WHERE v.org_id = ?
ORDER BY c.variable_id ASC, c.server_id ASC;

-- name: ListSharedCiphertextsForServer :many
SELECT variable_id, ciphertext FROM shared_secret_ciphertexts WHERE server_id = ?;

-- name: UpsertSharedCiphertext :exec
INSERT INTO shared_secret_ciphertexts (variable_id, server_id, ciphertext)
VALUES (?, ?, ?)
ON CONFLICT (variable_id, server_id) DO UPDATE SET ciphertext = excluded.ciphertext;

-- name: DeleteSharedCiphertextsByVariable :exec
DELETE FROM shared_secret_ciphertexts WHERE variable_id = ?;

-- name: DeleteSharedCiphertext :exec
DELETE FROM shared_secret_ciphertexts WHERE variable_id = ? AND server_id = ?;

-- name: DeleteSharedCiphertextsByServer :exec
DELETE FROM shared_secret_ciphertexts WHERE server_id = ?;

-- name: BumpServerVarsRevision :exec
UPDATE servers SET vars_revision = vars_revision + 1 WHERE server_id = ?;

-- name: RaiseServerVarsRevision :exec
-- Lifts vars_revision past an agent-reported revision the API never issued
-- (the API database was restored); never lowers it.
UPDATE servers SET vars_revision = sqlc.arg(revision)
WHERE server_id = sqlc.arg(server_id) AND vars_revision < sqlc.arg(revision);

-- name: GetServerVarsInfo :one
SELECT server_id, organization_id, name, vars_revision FROM servers WHERE server_id = ?;

-- name: ListOrgServers :many
SELECT server_id, name FROM servers WHERE organization_id = ? ORDER BY created_at ASC, server_id ASC;

-- name: ListOrgPublicKeys :many
SELECT c.server_id, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id = ? AND c.name = 'public_key';
