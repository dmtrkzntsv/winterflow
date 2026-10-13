-- Templates: an org's stack definitions, each a sequence of immutable
-- revisions (the JSON body). Instances: apps created from a revision,
-- addressed by the caller's key, unique in the org among instances that
-- are not unlinked. The rollout queue arrives in its own migration (D1b).

CREATE TABLE templates (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    name VARCHAR(63) NOT NULL,
    latest_revision INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (org_id, name),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE TABLE template_revisions (
    template_id UUID NOT NULL,
    revision INTEGER NOT NULL,
    body TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
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
    params TEXT NOT NULL,
    secret_names TEXT NOT NULL,
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
