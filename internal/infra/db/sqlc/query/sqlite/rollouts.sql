-- The rollout queue. Every worker write names its lease_owner, so a worker
-- that lost its lease changes nothing.

-- name: InsertRollout :execrows
-- An active rollout of the same (kind, scope_key) inserts nothing (0 rows).
INSERT INTO rollouts (id, org_id, kind, scope, scope_key, state, created_at, created_by)
VALUES (?, ?, ?, ?, ?, 'queued', ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetRollout :one
SELECT * FROM rollouts WHERE id = ?;

-- name: GetActiveRollout :one
SELECT * FROM rollouts WHERE kind = ? AND scope_key = ? AND state IN ('queued', 'running');

-- name: ListRolloutsForScope :many
-- The active rollout first, then the ended ones newest first.
SELECT * FROM rollouts
WHERE org_id = ? AND kind = ? AND scope_key = ?
ORDER BY CASE WHEN state IN ('queued', 'running') THEN 0 ELSE 1 END, created_at DESC, id DESC
LIMIT 21;

-- name: ListRolloutsOfKind :many
SELECT * FROM rollouts
WHERE org_id = ? AND kind = ?
ORDER BY CASE WHEN state IN ('queued', 'running') THEN 0 ELSE 1 END, created_at DESC, id DESC
LIMIT 100;

-- name: ExtendRollout :execrows
-- Optimistic: 0 rows when the scope moved on (another extension), the
-- rollout ended or a stop was requested (it would end stopped, losing the
-- extension).
UPDATE rollouts SET scope = ?, scope_rev = scope_rev + 1
WHERE id = ? AND scope_rev = ? AND state IN ('queued', 'running') AND NOT stop_requested;

-- name: RequestRolloutStop :execrows
UPDATE rollouts SET stop_requested = TRUE
WHERE id = ? AND org_id = ? AND state IN ('queued', 'running');

-- name: ListClaimableRollouts :many
-- The worker's idle query: active rollouts nobody holds a live lease on.
-- Times are stored to the second, so a lease ending now counts as ended
-- (<=): a parked rollout is due on the poll that follows its park.
SELECT id FROM rollouts
WHERE state IN ('queued', 'running') AND (lease_until IS NULL OR lease_until <= ?)
ORDER BY created_at ASC, id ASC
LIMIT 10;

-- name: ClaimRollout :execrows
UPDATE rollouts SET lease_owner = ?, lease_until = ?, state = 'running'
WHERE id = ? AND state IN ('queued', 'running') AND (lease_until IS NULL OR lease_until <= ?);

-- name: RenewRolloutLease :execrows
UPDATE rollouts SET lease_until = ?
WHERE id = ? AND lease_owner = ? AND state = 'running';

-- name: ReleaseRolloutLease :execrows
-- Gives the lease back: lease_until NULL (a graceful shutdown) is claimable
-- at once; a time parks a rollout that waits on a server until the next poll
-- is due, so the claimable list (LIMIT) does not fill with waiting ones.
UPDATE rollouts SET lease_owner = '', lease_until = ?
WHERE id = ? AND lease_owner = ?;

-- name: StopUnleasedRollout :execrows
-- Ends a rollout nobody is driving (queued, parked, or whose owner's lease
-- expired) as stopped. 0 rows: someone holds a live lease (stop_requested
-- takes over) or it is not the org's / not active.
UPDATE rollouts
SET state = 'stopped', stop_requested = TRUE, current_app_id = '', current_server_id = '',
    lease_owner = '', lease_until = NULL, finished_at = ?
WHERE id = ? AND org_id = ? AND state IN ('queued', 'running')
  AND (lease_owner = '' OR lease_until IS NULL OR lease_until <= ?);

-- name: StartRolloutStep :execrows
UPDATE rollouts SET current_app_id = ?, current_server_id = ?, total = ?
WHERE id = ? AND lease_owner = ? AND state = 'running';

-- name: CountRolloutStep :execrows
UPDATE rollouts SET done = done + ?, skipped = skipped + ?, skip_ids = ?
WHERE id = ? AND lease_owner = ? AND state = 'running';

-- name: FinishRollout :execrows
-- Stopped or failed. A failure keeps the current app: it names what failed.
UPDATE rollouts
SET state = ?, error = ?, current_app_id = ?, current_server_id = ?, lease_owner = '', lease_until = NULL, finished_at = ?
WHERE id = ? AND lease_owner = ? AND state = 'running';

-- name: CompleteRollout :execrows
-- Done, unless the scope was extended since the worker read it (0 rows: it
-- looks again).
UPDATE rollouts
SET state = 'done', current_app_id = '', current_server_id = '', lease_owner = '', lease_until = NULL, finished_at = ?
WHERE id = ? AND lease_owner = ? AND state = 'running' AND scope_rev = ?;

-- name: PruneRollouts :exec
-- Keeps the newest ended rollouts of a scope.
DELETE FROM rollouts
WHERE rollouts.kind = sqlc.arg(kind) AND rollouts.scope_key = sqlc.arg(scope_key)
  AND rollouts.state IN ('done', 'stopped', 'failed')
  AND rollouts.id NOT IN (
    SELECT r.id FROM rollouts AS r
    WHERE r.kind = sqlc.arg(kind) AND r.scope_key = sqlc.arg(scope_key) AND r.state IN ('done', 'stopped', 'failed')
    ORDER BY r.finished_at DESC, r.id DESC
    LIMIT sqlc.arg(keep));
