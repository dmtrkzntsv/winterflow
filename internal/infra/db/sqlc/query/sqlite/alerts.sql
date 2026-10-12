-- Alerts: channels, rules, overrides, states, events (see migration 0013).

-- name: ListUnseededOrganizationIDs :many
SELECT organization_id FROM organizations
WHERE alert_defaults_seeded = FALSE
ORDER BY organization_id ASC;

-- name: MarkAlertDefaultsSeeded :execrows
UPDATE organizations SET alert_defaults_seeded = TRUE
WHERE organization_id = ? AND alert_defaults_seeded = FALSE;

-- name: GetAlertServerOrganizationID :one
SELECT organization_id FROM servers WHERE server_id = ?;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels
WHERE organization_id = ?
ORDER BY created_at ASC, channel_id ASC;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels
WHERE organization_id = ? AND channel_id = ?;

-- name: InsertNotificationChannel :exec
INSERT INTO notification_channels (
    channel_id, organization_id, name, service, fields, url_encrypted,
    is_default, enabled, last_error, last_sent_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', NULL, ?);

-- name: UpdateNotificationChannel :execrows
UPDATE notification_channels
SET name = ?, service = ?, fields = ?, url_encrypted = ?, is_default = ?, enabled = ?, last_error = ?
WHERE organization_id = ? AND channel_id = ?;

-- name: DeleteNotificationChannel :execrows
DELETE FROM notification_channels WHERE organization_id = ? AND channel_id = ?;

-- name: RecordChannelSuccess :exec
UPDATE notification_channels SET last_error = '', last_sent_at = ? WHERE channel_id = ?;

-- name: RecordChannelFailure :exec
UPDATE notification_channels SET last_error = ? WHERE channel_id = ?;

-- name: ListAlertRules :many
SELECT * FROM alert_rules
WHERE organization_id = ?
ORDER BY target_kind DESC, metric ASC, threshold ASC, created_at ASC, rule_id ASC;

-- name: GetAlertRule :one
SELECT * FROM alert_rules WHERE organization_id = ? AND rule_id = ?;

-- name: InsertAlertRule :exec
INSERT INTO alert_rules (
    rule_id, organization_id, target_kind, metric, threshold, duration_minutes,
    channel_ids, enabled, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateAlertRule :execrows
UPDATE alert_rules SET threshold = ?, duration_minutes = ?, channel_ids = ?, enabled = ?
WHERE organization_id = ? AND rule_id = ?;

-- name: DeleteAlertRule :execrows
DELETE FROM alert_rules WHERE organization_id = ? AND rule_id = ?;

-- name: ListAlertOverrides :many
SELECT o.rule_id, o.server_id, o.app_id, o.disabled, o.threshold, o.duration_minutes, o.channel_ids
FROM alert_overrides AS o
JOIN alert_rules AS r ON r.rule_id = o.rule_id
WHERE r.organization_id = ?
ORDER BY o.rule_id ASC, o.server_id ASC, o.app_id ASC;

-- name: ListTargetAlertOverrides :many
SELECT * FROM alert_overrides
WHERE server_id = ? AND app_id = ?
ORDER BY rule_id ASC;

-- name: UpsertAlertOverride :exec
INSERT INTO alert_overrides (rule_id, server_id, app_id, disabled, threshold, duration_minutes, channel_ids)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (rule_id, server_id, app_id) DO UPDATE
SET disabled = excluded.disabled, threshold = excluded.threshold,
    duration_minutes = excluded.duration_minutes, channel_ids = excluded.channel_ids;

-- name: DeleteAlertOverride :execrows
DELETE FROM alert_overrides WHERE rule_id = ? AND server_id = ? AND app_id = ?;

-- FireAlertState moves a (rule, target) to firing unless it already is;
-- the row count says whether this call made the transition (several API
-- replicas evaluate the same events).
-- name: FireAlertState :execrows
INSERT INTO alert_states (rule_id, server_id, app_id, state, since, value, threshold)
VALUES (?, ?, ?, 'firing', ?, ?, ?)
ON CONFLICT (rule_id, server_id, app_id) DO UPDATE
SET state = 'firing', since = excluded.since, value = excluded.value, threshold = excluded.threshold
WHERE alert_states.state <> 'firing';

-- name: ResolveAlertState :execrows
UPDATE alert_states SET state = 'ok', since = ?, value = ?
WHERE rule_id = ? AND server_id = ? AND app_id = ? AND state = 'firing';

-- name: DeleteAlertState :exec
DELETE FROM alert_states WHERE rule_id = ? AND server_id = ? AND app_id = ?;

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
WHERE r.organization_id = ? AND s.state = 'firing'
ORDER BY s.since DESC, s.rule_id ASC;

-- name: CountFiringAlertsByServer :many
SELECT server_id, COUNT(*) AS firing FROM alert_states
WHERE state = 'firing'
GROUP BY server_id;

-- name: InsertAlertEvent :exec
INSERT INTO alert_events (
    event_id, organization_id, rule_id, server_id, app_id, server_name, app_name,
    target_kind, metric, kind, value, threshold, title, message, deliveries, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', ?);

-- name: SetAlertEventDeliveries :exec
UPDATE alert_events SET deliveries = ? WHERE event_id = ?;

-- ListAlertEvents pages an org's history newest first; an empty filter
-- matches everything, and the page starts strictly before
-- (before_at, before_id).
-- name: ListAlertEvents :many
SELECT * FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
  AND (CAST(sqlc.arg(server_id) AS TEXT) = '' OR server_id = CAST(sqlc.arg(server_id) AS TEXT))
  AND (CAST(sqlc.arg(app_id) AS TEXT) = '' OR app_id = CAST(sqlc.arg(app_id) AS TEXT))
  AND (CAST(sqlc.arg(rule_id) AS TEXT) = '' OR rule_id = CAST(sqlc.arg(rule_id) AS TEXT))
  AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR kind = CAST(sqlc.arg(kind) AS TEXT))
  AND (created_at < sqlc.arg(before_at)
       OR (created_at = sqlc.arg(before_at) AND event_id < CAST(sqlc.arg(before_id) AS TEXT)))
ORDER BY created_at DESC, event_id DESC
LIMIT sqlc.arg(row_limit);

-- name: DeleteAlertEventsBefore :execrows
DELETE FROM alert_events WHERE created_at < ?;

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
LIMIT 1 OFFSET sqlc.arg(keep);

-- name: DeleteAlertEventsUpTo :execrows
DELETE FROM alert_events
WHERE organization_id = sqlc.arg(organization_id)
  AND (created_at < sqlc.arg(cutoff_at)
       OR (created_at = sqlc.arg(cutoff_at) AND event_id <= CAST(sqlc.arg(cutoff_id) AS TEXT)));

-- name: ListAlertDirectoryServers :many
SELECT server_id, organization_id, name FROM servers ORDER BY server_id ASC;

-- name: ListAlertDirectoryApps :many
SELECT app_id, server_id, name FROM apps ORDER BY app_id ASC;

-- name: DeleteAlertOverridesByApp :exec
DELETE FROM alert_overrides WHERE app_id = ?;

-- name: DeleteAlertStatesByApp :exec
DELETE FROM alert_states WHERE app_id = ?;

-- name: DeleteAlertEventsByApp :exec
DELETE FROM alert_events WHERE app_id = ?;
