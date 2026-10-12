-- Alerts: channels, rules, overrides, states, events (see migration 0013).

-- name: ListUnseededOrganizationIDs :many
SELECT organization_id FROM organizations
WHERE alert_defaults_seeded = FALSE
ORDER BY organization_id ASC;

-- name: MarkAlertDefaultsSeeded :execrows
UPDATE organizations SET alert_defaults_seeded = TRUE
WHERE organization_id = $1 AND alert_defaults_seeded = FALSE;

-- name: GetAlertServerOrganizationID :one
SELECT organization_id FROM servers WHERE server_id = $1;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels
WHERE organization_id = $1
ORDER BY created_at ASC, channel_id ASC;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels
WHERE organization_id = $1 AND channel_id = $2;

-- name: InsertNotificationChannel :exec
INSERT INTO notification_channels (
    channel_id, organization_id, name, service, fields, url_encrypted,
    is_default, enabled, last_error, last_sent_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '', NULL, $9);

-- name: UpdateNotificationChannel :execrows
UPDATE notification_channels
SET name = $1, service = $2, fields = $3, url_encrypted = $4, is_default = $5, enabled = $6, last_error = $7
WHERE organization_id = $8 AND channel_id = $9;

-- name: DeleteNotificationChannel :execrows
DELETE FROM notification_channels WHERE organization_id = $1 AND channel_id = $2;

-- name: RecordChannelSuccess :exec
UPDATE notification_channels SET last_error = '', last_sent_at = $1 WHERE channel_id = $2;

-- name: RecordChannelFailure :exec
UPDATE notification_channels SET last_error = $1 WHERE channel_id = $2;

-- name: ListAlertRules :many
SELECT * FROM alert_rules
WHERE organization_id = $1
ORDER BY target_kind DESC, metric ASC, threshold ASC, created_at ASC, rule_id ASC;

-- name: GetAlertRule :one
SELECT * FROM alert_rules WHERE organization_id = $1 AND rule_id = $2;

-- name: InsertAlertRule :exec
INSERT INTO alert_rules (
    rule_id, organization_id, target_kind, metric, threshold, duration_minutes,
    channel_ids, enabled, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UpdateAlertRule :execrows
UPDATE alert_rules SET threshold = $1, duration_minutes = $2, channel_ids = $3, enabled = $4
WHERE organization_id = $5 AND rule_id = $6;

-- name: DeleteAlertRule :execrows
DELETE FROM alert_rules WHERE organization_id = $1 AND rule_id = $2;

-- name: ListAlertOverrides :many
SELECT o.rule_id, o.server_id, o.app_id, o.disabled, o.threshold, o.duration_minutes, o.channel_ids
FROM alert_overrides AS o
JOIN alert_rules AS r ON r.rule_id = o.rule_id
WHERE r.organization_id = $1
ORDER BY o.rule_id ASC, o.server_id ASC, o.app_id ASC;

-- name: ListTargetAlertOverrides :many
SELECT * FROM alert_overrides
WHERE server_id = $1 AND app_id = $2
ORDER BY rule_id ASC;

-- name: UpsertAlertOverride :exec
INSERT INTO alert_overrides (rule_id, server_id, app_id, disabled, threshold, duration_minutes, channel_ids)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (rule_id, server_id, app_id) DO UPDATE
SET disabled = excluded.disabled, threshold = excluded.threshold,
    duration_minutes = excluded.duration_minutes, channel_ids = excluded.channel_ids;

-- name: DeleteAlertOverride :execrows
DELETE FROM alert_overrides WHERE rule_id = $1 AND server_id = $2 AND app_id = $3;

-- FireAlertState moves a (rule, target) to firing unless it already is;
-- the row count says whether this call made the transition (several API
-- replicas evaluate the same events).
-- name: FireAlertState :execrows
INSERT INTO alert_states (rule_id, server_id, app_id, state, since, value, threshold)
VALUES ($1, $2, $3, 'firing', $4, $5, $6)
ON CONFLICT (rule_id, server_id, app_id) DO UPDATE
SET state = 'firing', since = excluded.since, value = excluded.value, threshold = excluded.threshold
WHERE alert_states.state <> 'firing';

-- name: ResolveAlertState :execrows
UPDATE alert_states SET state = 'ok', since = $1, value = $2
WHERE rule_id = $3 AND server_id = $4 AND app_id = $5 AND state = 'firing';

-- name: DeleteAlertState :exec
DELETE FROM alert_states WHERE rule_id = $1 AND server_id = $2 AND app_id = $3;

-- name: ListFiringAlertStates :many
SELECT s.rule_id, s.server_id, s.app_id, s.since, s.value, s.threshold, r.target_kind, r.metric
FROM alert_states AS s
JOIN alert_rules AS r ON r.rule_id = s.rule_id
WHERE s.state = 'firing'
ORDER BY s.rule_id ASC, s.server_id ASC, s.app_id ASC;

-- name: ListOrgFiringAlertStates :many
SELECT s.rule_id, s.server_id, s.app_id, s.since, s.value, s.threshold, r.target_kind, r.metric
FROM alert_states AS s
JOIN alert_rules AS r ON r.rule_id = s.rule_id
WHERE r.organization_id = $1 AND s.state = 'firing'
ORDER BY s.since DESC, s.rule_id ASC;

-- name: CountFiringAlertsByServer :many
SELECT server_id, COUNT(*) AS firing FROM alert_states
WHERE state = 'firing'
GROUP BY server_id;

-- name: InsertAlertEvent :exec
INSERT INTO alert_events (
    event_id, organization_id, rule_id, server_id, app_id, server_name, app_name,
    target_kind, metric, kind, value, threshold, title, message, deliveries, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, '[]', $15);

-- name: SetAlertEventDeliveries :exec
UPDATE alert_events SET deliveries = $1 WHERE event_id = $2;

-- ListAlertEvents pages an org's history newest first; an empty filter
-- matches everything, and the page starts strictly before
-- (before_at, before_id).
-- name: ListAlertEvents :many
SELECT * FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
  AND (sqlc.arg(server_id)::text = '' OR server_id::text = sqlc.arg(server_id)::text)
  AND (sqlc.arg(app_id)::text = '' OR app_id = sqlc.arg(app_id)::text)
  AND (sqlc.arg(rule_id)::text = '' OR rule_id::text = sqlc.arg(rule_id)::text)
  AND (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind)::text)
  AND (created_at < sqlc.arg(before_at)
       OR (created_at = sqlc.arg(before_at) AND event_id < sqlc.arg(before_id)::text))
ORDER BY created_at DESC, event_id DESC
LIMIT sqlc.arg(row_limit)::bigint;

-- name: DeleteAlertEventsBefore :execrows
DELETE FROM alert_events WHERE created_at < $1;

-- ListOversizedAlertOrganizations lists the orgs past the history cap. The
-- literal is alert repository's EventsPerOrg (sqlc cannot bind a HAVING
-- parameter); keep them in step.
-- name: ListOversizedAlertOrganizations :many
SELECT organization_id FROM alert_events
GROUP BY organization_id
HAVING COUNT(*) > 2000;

-- GetAlertEventTrimCutoff is the newest event of an org that falls outside
-- the newest `keep` (none when the org has at most `keep`).
-- name: GetAlertEventTrimCutoff :one
SELECT created_at, event_id FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
ORDER BY created_at DESC, event_id DESC
LIMIT 1 OFFSET sqlc.arg(keep)::bigint;

-- name: DeleteAlertEventsUpTo :execrows
DELETE FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
  AND (created_at < sqlc.arg(cutoff_at)
       OR (created_at = sqlc.arg(cutoff_at) AND event_id <= sqlc.arg(cutoff_id)::text));

-- name: ListAlertDirectoryServers :many
SELECT server_id, organization_id, name FROM servers ORDER BY server_id ASC;

-- name: ListAlertDirectoryApps :many
SELECT app_id, server_id, name FROM apps ORDER BY app_id ASC;

-- name: DeleteAlertOverridesByApp :exec
DELETE FROM alert_overrides WHERE app_id = $1;

-- name: DeleteAlertStatesByApp :exec
DELETE FROM alert_states WHERE app_id = $1;

-- name: DeleteAlertEventsByApp :exec
DELETE FROM alert_events WHERE app_id = $1;
