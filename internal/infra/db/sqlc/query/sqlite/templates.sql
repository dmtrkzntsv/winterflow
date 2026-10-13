-- Templates, their immutable revisions and their instances.

-- name: InsertTemplate :execrows
-- A taken name inserts nothing (0 rows): the caller answers 400.
INSERT INTO templates (id, org_id, name, latest_revision, created_at, created_by, updated_at)
VALUES (?, ?, ?, 1, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: BumpTemplateRevision :execrows
-- Optimistic: 0 rows when latest_revision moved past the caller's base.
UPDATE templates
SET latest_revision = latest_revision + 1, name = ?, updated_at = ?
WHERE id = ? AND org_id = ? AND latest_revision = ?;

-- name: GetTemplate :one
SELECT * FROM templates WHERE id = ? AND org_id = ?;

-- name: GetTemplateIDByName :one
SELECT id FROM templates WHERE org_id = ? AND name = ?;

-- name: ListTemplates :many
-- One pass over the live instances: instances counts them, behind those on
-- a revision older than the latest.
SELECT t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at,
    COUNT(i.id) AS instances,
    CAST(COALESCE(SUM(CASE WHEN i.revision < t.latest_revision THEN 1 ELSE 0 END), 0) AS INTEGER) AS behind
FROM templates AS t
LEFT JOIN template_instances AS i ON i.template_id = t.id AND i.state IN ('pending', 'created')
WHERE t.org_id = ?
GROUP BY t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at
ORDER BY t.name ASC;

-- name: ListTemplateNames :many
-- Names only (the instance list shows them).
SELECT id, name FROM templates WHERE org_id = ?;

-- name: DeleteTemplate :execrows
-- Refused (0 rows) while an instance is pending or created.
DELETE FROM templates
WHERE templates.id = ? AND templates.org_id = ?
  AND NOT EXISTS (
    SELECT 1 FROM template_instances AS i
    WHERE i.template_id = templates.id AND i.state IN ('pending', 'created'));

-- name: DeleteTemplateRollouts :exec
-- A deleted template's upgrade rollouts (scope key: the template id).
DELETE FROM rollouts WHERE kind = 'template_upgrade' AND scope_key = ?;

-- name: InsertTemplateRevision :exec
INSERT INTO template_revisions (template_id, revision, body, note, created_at, created_by)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetTemplateRevision :one
SELECT * FROM template_revisions WHERE template_id = ? AND revision = ?;

-- name: ListTemplateRevisions :many
SELECT revision, note, created_at, created_by
FROM template_revisions
WHERE template_id = ?
ORDER BY revision DESC;

-- name: ClaimTemplateInstance :execrows
-- A live key (the partial unique index) inserts nothing (0 rows).
INSERT INTO template_instances (id, org_id, instance_key, app_id, server_id, template_id, revision,
    params, secret_names, request_hash, state, last_error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetLiveTemplateInstance :one
SELECT * FROM template_instances WHERE org_id = ? AND instance_key = ? AND state <> 'unlinked';

-- name: ListLiveTemplateInstances :many
SELECT * FROM template_instances
WHERE org_id = ? AND state IN ('pending', 'created')
ORDER BY created_at ASC, id ASC;

-- name: ListUpgradableInstances :many
-- The org's created instances of a template below a revision on the org's
-- own servers, oldest first: what a template_upgrade rollout of the org may
-- still touch.
SELECT i.* FROM template_instances AS i
JOIN servers AS s ON s.server_id = i.server_id AND s.organization_id = i.org_id
WHERE i.org_id = ? AND i.template_id = ? AND i.state = 'created' AND i.revision < ?
ORDER BY i.created_at ASC, i.id ASC;

-- name: MarkTemplateInstanceSaved :exec
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
UPDATE template_instances
SET revision = ?, params = ?, secret_names = ?, state = 'created', last_error = '', updated_at = ?
WHERE id = ? AND state IN ('pending', 'created');

-- name: SetTemplateInstanceState :exec
UPDATE template_instances
SET state = ?, last_error = '', updated_at = ?
WHERE id = ? AND state IN ('pending', 'created');

-- name: SetTemplateInstanceError :exec
UPDATE template_instances SET last_error = ?, updated_at = ? WHERE id = ?;

-- name: GetTemplateInstanceByID :one
SELECT * FROM template_instances WHERE id = ?;

-- name: GetLiveTemplateInstanceForApp :one
-- The pending or created instance an app is, with its template's name
-- ('' when the template is gone).
SELECT CAST(COALESCE(t.name, '') AS TEXT) AS template_name
FROM template_instances AS i
LEFT JOIN templates AS t ON t.id = i.template_id
WHERE i.server_id = ? AND i.app_id = ? AND i.state IN ('pending', 'created')
ORDER BY i.created_at ASC, i.id ASC
LIMIT 1;

-- name: RetireTemplateInstancesForApp :exec
-- The app was deleted outside the instance routes: its instance's key
-- stays retired.
UPDATE template_instances
SET state = 'deleted', last_error = '', updated_at = ?
WHERE server_id = ? AND app_id = ? AND state IN ('pending', 'created');
