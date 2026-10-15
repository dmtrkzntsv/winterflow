-- Templates, their immutable revisions and their instances. A template is
-- an org's own (source org, org_id set) or the catalog's (source catalog,
-- org_id NULL): an org reads its own and every catalog template, and
-- changes only its own.

-- name: InsertTemplate :execrows
-- A taken org name or catalog slug inserts nothing (0 rows): the caller
-- answers 400 (save-template) or skips the entry (the catalog sync lost
-- the race to another replica).
INSERT INTO templates (id, source, org_id, slug, name, latest_revision, withdrawn_at, catalog, catalog_feed_at, created_at, created_by, updated_at)
VALUES ($1, $2, $3, $4, $5, 1, $6, $7, $8, $9, $10, $11)
ON CONFLICT DO NOTHING;

-- name: BumpTemplateRevision :execrows
-- An org template's next revision. Optimistic: 0 rows when latest_revision
-- moved past the caller's base.
UPDATE templates
SET latest_revision = latest_revision + 1, name = $1, updated_at = $2
WHERE id = $3 AND source = 'org' AND org_id = $4 AND latest_revision = $5;

-- name: GetTemplate :one
-- A template the org may read: its own or a catalog one.
SELECT * FROM templates WHERE id = $1 AND (org_id = $2 OR source = 'catalog');

-- name: GetTemplateIDByName :one
SELECT id FROM templates WHERE source = 'org' AND org_id = $1 AND name = $2;

-- name: ListTemplates :many
-- The org's templates, then the catalog's (a withdrawn one only while the
-- org has live instances of it), each by name. One pass over the org's
-- live instances: instances counts them, behind those on a revision older
-- than the latest.
SELECT t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.catalog, t.created_at, t.created_by, t.updated_at,
    COUNT(i.id) AS instances,
    CAST(COALESCE(SUM(CASE WHEN i.revision < t.latest_revision THEN 1 ELSE 0 END), 0) AS BIGINT) AS behind
FROM templates AS t
LEFT JOIN template_instances AS i ON i.template_id = t.id AND i.org_id = $1 AND i.state IN ('pending', 'created')
WHERE t.org_id = $2 OR t.source = 'catalog'
GROUP BY t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.catalog, t.created_at, t.created_by, t.updated_at
HAVING t.source = 'org' OR t.withdrawn_at IS NULL OR COUNT(i.id) > 0
ORDER BY t.source DESC, t.name ASC, t.id ASC;

-- name: ListTemplateNames :many
-- The names of the templates the org may read (the instance list shows
-- them).
SELECT id, name FROM templates WHERE org_id = $1 OR source = 'catalog';

-- name: DeleteTemplate :execrows
-- An org template; refused (0 rows) while an instance is pending or created.
DELETE FROM templates
WHERE templates.id = $1 AND templates.source = 'org' AND templates.org_id = $2
  AND NOT EXISTS (
    SELECT 1 FROM template_instances AS i
    WHERE i.template_id = templates.id AND i.state IN ('pending', 'created'));

-- name: DeleteTemplateRollouts :exec
-- A deleted template's upgrade rollouts (scope key: <org_id>:<template_id>).
DELETE FROM rollouts WHERE kind = 'template_upgrade' AND scope_key = $1;

-- name: ListCatalogTemplates :many
-- Every catalog template with its latest revision's digest: what the
-- catalog sync compares a feed with.
SELECT t.id, t.slug, t.name, t.catalog, t.latest_revision, t.withdrawn_at, t.catalog_feed_at, r.digest
FROM templates AS t
JOIN template_revisions AS r ON r.template_id = t.id AND r.revision = t.latest_revision
WHERE t.source = 'catalog'
ORDER BY t.slug;

-- name: BumpCatalogTemplate :execrows
-- A catalog template's next revision from the base the sync read, its
-- name and metadata, withdrawn_at cleared: 0 rows when another replica
-- moved latest_revision, or a newer feed wrote the template.
UPDATE templates
SET latest_revision = latest_revision + 1, name = sqlc.arg(name), catalog = sqlc.arg(catalog), withdrawn_at = NULL,
    catalog_feed_at = sqlc.arg(feed_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND source = 'catalog' AND latest_revision = sqlc.arg(latest_revision)
  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));

-- name: UpdateCatalogTemplate :execrows
-- A catalog template's name and metadata in place, withdrawn_at cleared:
-- 0 rows when a newer feed wrote it.
UPDATE templates
SET name = sqlc.arg(name), catalog = sqlc.arg(catalog), withdrawn_at = NULL,
    catalog_feed_at = sqlc.arg(feed_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND source = 'catalog'
  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));

-- name: WithdrawCatalogTemplate :execrows
-- A catalog template the feed no longer lists: 0 rows when it is withdrawn
-- already or a newer feed wrote it.
UPDATE templates
SET withdrawn_at = sqlc.arg(withdrawn_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND source = 'catalog' AND withdrawn_at IS NULL
  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));

-- name: StampCatalogTemplates :exec
-- The feed every catalog template now reflects: a later sync of an older
-- feed is refused as a whole.
UPDATE templates
SET catalog_feed_at = sqlc.arg(feed_at)
WHERE source = 'catalog' AND (catalog_feed_at IS NULL OR catalog_feed_at < sqlc.arg(feed_at));

-- name: InsertTemplateRevision :exec
INSERT INTO template_revisions (template_id, revision, body, note, digest, created_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7);

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
    name, params, secret_names, hosts, backups, request_hash, state, last_error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'pending', '', $14, $15)
ON CONFLICT DO NOTHING;

-- name: GetLiveTemplateInstance :one
SELECT * FROM template_instances WHERE org_id = $1 AND instance_key = $2 AND state <> 'unlinked';

-- name: ListLiveTemplateInstances :many
SELECT * FROM template_instances
WHERE org_id = $1 AND state IN ('pending', 'created')
ORDER BY created_at ASC, id ASC;

-- name: ListUpgradableInstances :many
-- The org's created instances of a template below a revision on the org's
-- own servers, oldest first: what a template_upgrade rollout of the org may
-- still touch.
SELECT i.* FROM template_instances AS i
JOIN servers AS s ON s.server_id = i.server_id AND s.organization_id = i.org_id
WHERE i.org_id = $1 AND i.template_id = $2 AND i.state = 'created' AND i.revision < $3
ORDER BY i.created_at ASC, i.id ASC;

-- name: MarkTemplateInstanceSaved :exec
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
UPDATE template_instances
SET revision = $1, params = $2, secret_names = $3, hosts = $4, state = 'created', last_error = '', updated_at = $5
WHERE id = $6 AND state IN ('pending', 'created');

-- name: SetTemplateInstanceBackups :exec
-- A pending instance's backup settings; a confirmed one's belong to the app.
UPDATE template_instances SET backups = $1, updated_at = $2 WHERE id = $3 AND state = 'pending';

-- name: SetTemplateInstanceState :exec
UPDATE template_instances
SET state = $1, last_error = '', updated_at = $2
WHERE id = $3 AND state IN ('pending', 'created');

-- name: SetTemplateInstanceError :exec
UPDATE template_instances SET last_error = $1, updated_at = $2 WHERE id = $3;

-- name: GetTemplateInstanceByID :one
SELECT * FROM template_instances WHERE id = $1;

-- name: GetLiveTemplateInstanceForApp :one
-- The pending or created instance an app is, with its template's name
-- ('' when the template is gone).
SELECT CAST(COALESCE(t.name, '') AS TEXT) AS template_name
FROM template_instances AS i
LEFT JOIN templates AS t ON t.id = i.template_id
WHERE i.server_id = $1 AND i.app_id = $2 AND i.state IN ('pending', 'created')
ORDER BY i.created_at ASC, i.id ASC
LIMIT 1;

-- name: RetireTemplateInstancesForApp :exec
-- The app was deleted outside the instance routes: its instance's key
-- stays retired.
UPDATE template_instances
SET state = 'deleted', last_error = '', updated_at = $1
WHERE server_id = $2 AND app_id = $3 AND state IN ('pending', 'created');
