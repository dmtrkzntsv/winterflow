-- Results of MCP-dispatched commands.

-- name: InsertCommandResult :exec
INSERT INTO command_results (request_id, org_id, server_id, kind, status, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: FinishCommandResult :execrows
UPDATE command_results SET status = $1, result = $2, error = $3, finished_at = $4
WHERE request_id = $5 AND status IN ('running', 'unknown');

-- name: GetCommandResult :one
SELECT request_id, org_id, server_id, kind, status, result, error, created_at, finished_at
FROM command_results
WHERE request_id = $1 AND org_id = $2;

-- name: MarkStaleCommandResults :execrows
UPDATE command_results SET status = 'unknown', finished_at = $1
WHERE status = 'running' AND created_at < $2;

-- name: PruneCommandResults :execrows
DELETE FROM command_results
WHERE finished_at IS NOT NULL AND finished_at < $1;
