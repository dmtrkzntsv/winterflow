-- The server_storages index (mirrors each agent's storages; never a secret).

-- name: ListServerStorages :many
SELECT * FROM server_storages WHERE server_id = $1 ORDER BY name ASC;

-- name: UpsertServerStorage :exec
INSERT INTO server_storages (server_id, name, type, location, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (server_id, name) DO UPDATE
SET type = EXCLUDED.type,
    location = EXCLUDED.location,
    updated_at = EXCLUDED.updated_at;

-- name: InsertServerStorage :exec
INSERT INTO server_storages (server_id, name, type, location, updated_at)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteServerStorage :exec
DELETE FROM server_storages WHERE server_id = $1 AND name = $2;

-- name: DeleteServerStoragesByServer :exec
DELETE FROM server_storages WHERE server_id = $1;
