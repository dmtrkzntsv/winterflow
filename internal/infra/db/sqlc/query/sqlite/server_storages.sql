-- The server_storages index (mirrors each agent's storages; never a secret).

-- name: ListServerStorages :many
SELECT * FROM server_storages WHERE server_id = ? ORDER BY name ASC;

-- name: UpsertServerStorage :exec
INSERT INTO server_storages (server_id, name, type, location, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET type = excluded.type,
    location = excluded.location,
    updated_at = excluded.updated_at;

-- name: InsertServerStorage :exec
INSERT INTO server_storages (server_id, name, type, location, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteServerStorage :exec
DELETE FROM server_storages WHERE server_id = ? AND name = ?;

-- name: DeleteServerStoragesByServer :exec
DELETE FROM server_storages WHERE server_id = ?;
