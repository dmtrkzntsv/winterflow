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
SELECT t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at,
    (SELECT COUNT(*) FROM template_instances AS i
     WHERE i.template_id = t.id AND i.state IN ('pending', 'created')) AS instances,
    (SELECT COUNT(*) FROM template_instances AS i
     WHERE i.template_id = t.id AND i.state IN ('pending', 'created') AND i.revision < t.latest_revision) AS behind
FROM templates AS t
WHERE t.org_id = ?
ORDER BY t.name ASC;

-- name: DeleteTemplate :execrows
-- Refused (0 rows) while an instance is pending or created.
DELETE FROM templates
WHERE templates.id = ? AND templates.org_id = ?
  AND NOT EXISTS (
    SELECT 1 FROM template_instances AS i
    WHERE i.template_id = templates.id AND i.state IN ('pending', 'created'));

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

-- name: MarkTemplateInstanceSaved :execrows
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
UPDATE template_instances
SET revision = ?, params = ?, secret_names = ?, state = 'created', last_error = '', updated_at = ?
WHERE id = ? AND state IN ('pending', 'created');

-- name: SetTemplateInstanceState :execrows
UPDATE template_instances
SET state = ?, last_error = '', updated_at = ?
WHERE id = ? AND state IN ('pending', 'created');

-- name: SetTemplateInstanceError :exec
UPDATE template_instances SET last_error = ?, updated_at = ? WHERE id = ?;
