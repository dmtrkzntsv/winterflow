-- Templates use x-winterflow (sub-project 2): a revision's body is the
-- app's files plus summary, description, icon and color; parameters,
-- exposed ports and backups come from the compose file's x-winterflow
-- block. Nothing is migrated (no backward compatibility): the three tables
-- are recreated empty and the upgrade rollouts that named their rows go.
--
-- templates.source is org (an org's own, editable) or catalog (the same
-- for every org, read-only; org_id NULL, slug set, withdrawn_at once the
-- feed drops it). template_instances.name is the app name the create
-- asked for (renders use the app's current name once it has one) and hosts
-- the instance's ingress hostnames ([{host, expose}]), rendered into every
-- save.
--
-- A template_upgrade rollout's scope key becomes <org_id>:<template_id>
-- (73 characters): rollouts.scope_key is widened to hold it.

DROP TABLE template_instances;
DROP TABLE template_revisions;
DROP TABLE templates;
DELETE FROM rollouts WHERE kind = 'template_upgrade';
ALTER TABLE rollouts ALTER COLUMN scope_key TYPE VARCHAR(80);

CREATE TABLE templates (
    id UUID PRIMARY KEY,
    source VARCHAR(16) NOT NULL,
    org_id UUID,
    slug VARCHAR(128) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    latest_revision INTEGER NOT NULL,
    withdrawn_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    CHECK ((source = 'org' AND org_id IS NOT NULL AND slug = '' AND withdrawn_at IS NULL)
        OR (source = 'catalog' AND org_id IS NULL AND slug <> '')),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

-- An org's template names are unique in the org; a catalog slug is unique.
CREATE UNIQUE INDEX idx_templates_org_name ON templates (org_id, name) WHERE source = 'org';
CREATE UNIQUE INDEX idx_templates_catalog_slug ON templates (slug) WHERE source = 'catalog';

CREATE TABLE template_revisions (
    template_id UUID NOT NULL,
    revision INTEGER NOT NULL,
    body TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    digest VARCHAR(80) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    PRIMARY KEY (template_id, revision),
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE CASCADE
);

-- template_id is NULL once the template is gone (only deleted and unlinked
-- rows can outlive it: a template with live instances can't be deleted).
CREATE TABLE template_instances (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    instance_key VARCHAR(128) NOT NULL,
    app_id UUID NOT NULL,
    server_id UUID NOT NULL,
    template_id UUID,
    revision INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    params TEXT NOT NULL,
    secret_names TEXT NOT NULL,
    hosts TEXT NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    state VARCHAR(16) NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE,
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE SET NULL
);

-- One live key per org: an unlinked instance frees its key.
CREATE UNIQUE INDEX idx_template_instances_key ON template_instances (org_id, instance_key) WHERE state <> 'unlinked';
-- The live instances of a template (its counts, the delete guard).
CREATE INDEX idx_template_instances_template ON template_instances (template_id) WHERE state IN ('pending', 'created');
-- The org's live instances in list order.
CREATE INDEX idx_template_instances_live ON template_instances (org_id, created_at, id) WHERE state IN ('pending', 'created');
-- An app's instance (the save-app guard, delete-app's retire). Not partial:
-- its leading column also serves the servers foreign key's cascade.
CREATE INDEX idx_template_instances_app ON template_instances (server_id, app_id);
