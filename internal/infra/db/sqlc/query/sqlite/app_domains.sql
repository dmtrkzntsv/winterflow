-- The app_domains ingress index (rebuildable from the agents' app configs).

-- name: GetDomainClaim :one
SELECT d.domain, d.app_id, d.server_id, COALESCE(a.name, '') AS app_name
FROM app_domains AS d
LEFT JOIN apps AS a ON a.app_id = d.app_id
WHERE d.server_id = ? AND d.domain = ?;

-- name: InsertAppDomain :exec
INSERT INTO app_domains (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAppDomainsByApp :exec
DELETE FROM app_domains WHERE app_id = ?;

-- name: DeleteAppDomainsByServer :exec
DELETE FROM app_domains WHERE server_id = ?;

-- name: ListAppDomainsByServer :many
SELECT * FROM app_domains WHERE server_id = ? ORDER BY domain ASC;

-- name: ListAppDomainNames :many
SELECT domain FROM app_domains
WHERE server_id = ? AND app_id = ?
ORDER BY domain ASC;
