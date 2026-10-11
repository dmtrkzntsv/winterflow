-- The apps catalog (a reconciled cache of what agents report).

-- name: ListAppsByServer :many
SELECT * FROM apps
WHERE server_id = $1
ORDER BY created_at ASC, app_id ASC;

-- name: ListAppIDsByServer :many
SELECT app_id FROM apps WHERE server_id = $1;

-- name: GetApp :one
SELECT * FROM apps WHERE app_id = $1;

-- name: UpsertApp :exec
INSERT INTO apps (app_id, server_id, template_id, name, version, icon, color, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (app_id) DO UPDATE
SET name = EXCLUDED.name,
    version = EXCLUDED.version,
    icon = EXCLUDED.icon,
    color = EXCLUDED.color,
    template_id = EXCLUDED.template_id;

-- name: DeleteApp :exec
DELETE FROM apps WHERE app_id = $1;

-- name: RenameApp :exec
UPDATE apps SET name = $1 WHERE app_id = $2;
