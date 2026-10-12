-- Org- and server-level shared variables and their per-server ciphertexts.

-- name: ListOrgSharedVariables :many
SELECT * FROM shared_variables WHERE org_id = $1 AND server_id IS NULL ORDER BY name ASC;

-- name: ListServerSharedVariables :many
SELECT * FROM shared_variables WHERE server_id = $1 ORDER BY name ASC;

-- name: GetSharedVariable :one
SELECT * FROM shared_variables WHERE id = $1;

-- name: GetOrgSharedVariableByName :one
SELECT * FROM shared_variables WHERE org_id = $1 AND server_id IS NULL AND name = $2;

-- name: GetServerSharedVariableByName :one
SELECT * FROM shared_variables WHERE server_id = $1 AND name = $2;

-- name: InsertSharedVariable :exec
INSERT INTO shared_variables (id, org_id, server_id, name, is_secret, value, description, updated_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UpdateSharedVariable :exec
UPDATE shared_variables SET value = $1, description = $2, updated_at = $3, updated_by = $4 WHERE id = $5;

-- name: DeleteSharedVariable :exec
DELETE FROM shared_variables WHERE id = $1;

-- name: ListCiphertextServersByOrg :many
SELECT c.variable_id, c.server_id
FROM shared_secret_ciphertexts AS c
JOIN shared_variables AS v ON v.id = c.variable_id
WHERE v.org_id = $1
ORDER BY c.variable_id ASC, c.server_id ASC;

-- name: ListSharedCiphertextsForServer :many
SELECT variable_id, ciphertext FROM shared_secret_ciphertexts WHERE server_id = $1;

-- name: UpsertSharedCiphertext :exec
INSERT INTO shared_secret_ciphertexts (variable_id, server_id, ciphertext)
VALUES ($1, $2, $3)
ON CONFLICT (variable_id, server_id) DO UPDATE SET ciphertext = EXCLUDED.ciphertext;

-- name: InsertSharedCiphertextIfAbsent :exec
-- Gap fill: never overwrites a copy (a concurrent rotation's new value wins).
INSERT INTO shared_secret_ciphertexts (variable_id, server_id, ciphertext)
VALUES ($1, $2, $3)
ON CONFLICT (variable_id, server_id) DO NOTHING;

-- name: DeleteSharedCiphertextsByVariable :exec
DELETE FROM shared_secret_ciphertexts WHERE variable_id = $1;

-- name: DeleteSharedCiphertext :exec
DELETE FROM shared_secret_ciphertexts WHERE variable_id = $1 AND server_id = $2;

-- name: DeleteSharedCiphertextsByServer :exec
DELETE FROM shared_secret_ciphertexts WHERE server_id = $1;

-- name: BumpServerVarsRevision :exec
UPDATE servers SET vars_revision = vars_revision + 1 WHERE server_id = $1;

-- name: RaiseServerVarsRevision :exec
-- Lifts vars_revision past an agent-reported revision the API never issued
-- (the API database was restored); never lowers it.
UPDATE servers SET vars_revision = sqlc.arg(revision)
WHERE server_id = sqlc.arg(server_id) AND vars_revision < sqlc.arg(revision);

-- name: GetServerVarsInfo :one
SELECT server_id, organization_id, name, vars_revision FROM servers WHERE server_id = $1;

-- name: ListOrgServers :many
SELECT server_id, name FROM servers WHERE organization_id = $1 ORDER BY created_at ASC, server_id ASC;

-- name: ListOrgPublicKeys :many
SELECT c.server_id, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id = $1 AND c.name = 'public_key';
