-- Templates, their immutable revisions and their instances.

-- name: InsertTemplate :execrows
-- A taken name inserts nothing (0 rows): the caller answers 400.
INSERT INTO templates (id, org_id, name, latest_revision, created_at, created_by, updated_at)
VALUES ($1, $2, $3, 1, $4, $5, $6)
ON CONFLICT DO NOTHING;

-- name: BumpTemplateRevision :execrows
-- Optimistic: 0 rows when latest_revision moved past the caller's base.
UPDATE templates
SET latest_revision = latest_revision + 1, name = $1, updated_at = $2
WHERE id = $3 AND org_id = $4 AND latest_revision = $5;

-- name: GetTemplate :one
SELECT * FROM templates WHERE id = $1 AND org_id = $2;

-- name: GetTemplateIDByName :one
SELECT id FROM templates WHERE org_id = $1 AND name = $2;

-- name: ListTemplates :many
SELECT t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at,
    (SELECT COUNT(*) FROM template_instances AS i
     WHERE i.template_id = t.id AND i.state IN ('pending', 'created')) AS instances,
    (SELECT COUNT(*) FROM template_instances AS i
     WHERE i.template_id = t.id AND i.state IN ('pending', 'created') AND i.revision < t.latest_revision) AS behind
FROM templates AS t
WHERE t.org_id = $1
ORDER BY t.name ASC;

-- name: DeleteTemplate :execrows
-- Refused (0 rows) while an instance is pending or created.
DELETE FROM templates
WHERE templates.id = $1 AND templates.org_id = $2
  AND NOT EXISTS (
    SELECT 1 FROM template_instances AS i
    WHERE i.template_id = templates.id AND i.state IN ('pending', 'created'));

-- name: InsertTemplateRevision :exec
INSERT INTO template_revisions (template_id, revision, body, note, created_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetTemplateRevision :one
SELECT * FROM template_revisions WHERE template_id = $1 AND revision = $2;

-- name: ListTemplateRevisions :many
SELECT revision, note, created_at, created_by
FROM template_revisions
WHERE template_id = $1
ORDER BY revision DESC;

-- name: ClaimTemplateInstance :execrows
-- A live key (the partial unique index) inserts nothing (0 rows).
INSERT INTO template_instances (id, org_id, instance_key, app_id, server_id, template_id, revision,
    params, secret_names, request_hash, state, last_error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', '', $11, $12)
ON CONFLICT DO NOTHING;

-- name: GetLiveTemplateInstance :one
SELECT * FROM template_instances WHERE org_id = $1 AND instance_key = $2 AND state <> 'unlinked';

-- name: ListLiveTemplateInstances :many
SELECT * FROM template_instances
WHERE org_id = $1 AND state IN ('pending', 'created')
ORDER BY created_at ASC, id ASC;

-- name: MarkTemplateInstanceSaved :execrows
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
UPDATE template_instances
SET revision = $1, params = $2, secret_names = $3, state = 'created', last_error = '', updated_at = $4
WHERE id = $5 AND state IN ('pending', 'created');

-- name: SetTemplateInstanceState :execrows
UPDATE template_instances
SET state = $1, last_error = '', updated_at = $2
WHERE id = $3 AND state IN ('pending', 'created');

-- name: SetTemplateInstanceError :exec
UPDATE template_instances SET last_error = $1, updated_at = $2 WHERE id = $3;
