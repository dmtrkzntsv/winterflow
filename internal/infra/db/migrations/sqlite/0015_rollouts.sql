-- Rollouts: queued, resumable, one-app-at-a-time jobs (upgrade a template's
-- instances, reload the apps a shared-variable change made stale). One API
-- process drives a rollout at a time through a lease; the partial unique
-- index allows one active rollout per (kind, scope_key).

CREATE TABLE rollouts (
    id VARCHAR(36) PRIMARY KEY,
    org_id VARCHAR(36) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    scope TEXT NOT NULL,
    scope_key VARCHAR(36) NOT NULL,
    scope_rev INTEGER NOT NULL DEFAULT 1,
    state VARCHAR(16) NOT NULL,
    stop_requested BOOLEAN NOT NULL DEFAULT FALSE,
    current_app_id VARCHAR(36) NOT NULL DEFAULT '',
    current_server_id VARCHAR(36) NOT NULL DEFAULT '',
    done INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    total INTEGER NOT NULL DEFAULT 0,
    skip_ids TEXT NOT NULL DEFAULT '[]',
    error TEXT NOT NULL DEFAULT '',
    lease_owner VARCHAR(36) NOT NULL DEFAULT '',
    lease_until TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    finished_at TIMESTAMP,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

-- One active rollout per scope: a template's upgrade, an org's reloads.
CREATE UNIQUE INDEX idx_rollouts_active ON rollouts (kind, scope_key) WHERE state IN ('queued', 'running');
-- The worker's once-a-minute look for claimable work.
CREATE INDEX idx_rollouts_claim ON rollouts (state, lease_until);
CREATE INDEX idx_rollouts_scope ON rollouts (org_id, kind, scope_key, created_at);
