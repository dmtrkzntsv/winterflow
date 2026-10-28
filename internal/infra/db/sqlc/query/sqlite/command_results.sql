-- Results of MCP-dispatched commands.

-- name: InsertCommandResult :exec
INSERT INTO command_results (request_id, org_id, server_id, kind, status, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: FinishCommandResult :execrows
UPDATE command_results SET status = ?, result = ?, error = ?, finished_at = ?
WHERE request_id = ? AND status IN ('running', 'unknown');

-- name: GetCommandResult :one
SELECT request_id, org_id, server_id, kind, status, result, error, created_at, finished_at
FROM command_results
WHERE request_id = ? AND org_id = ?;

-- name: MarkStaleCommandResults :execrows
UPDATE command_results SET status = 'unknown', finished_at = ?
WHERE status = 'running' AND created_at < ?;

-- name: PruneCommandResults :execrows
DELETE FROM command_results
WHERE finished_at IS NOT NULL AND finished_at < ?;
