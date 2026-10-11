-- The apps catalog (a reconciled cache of what agents report).

-- name: ListAppsByServer :many
SELECT * FROM apps
WHERE server_id = ?
ORDER BY created_at ASC, app_id ASC;

-- name: ListAppIDsByServer :many
SELECT app_id FROM apps WHERE server_id = ?;

-- name: GetApp :one
SELECT * FROM apps WHERE app_id = ?;

-- name: UpsertApp :exec
INSERT INTO apps (app_id, server_id, template_id, name, version, icon, color, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (app_id) DO UPDATE
SET name = excluded.name,
    version = excluded.version,
    icon = excluded.icon,
    color = excluded.color,
    template_id = excluded.template_id;

-- name: DeleteApp :exec
DELETE FROM apps WHERE app_id = ?;

-- name: RenameApp :exec
UPDATE apps SET name = ? WHERE app_id = ?;
