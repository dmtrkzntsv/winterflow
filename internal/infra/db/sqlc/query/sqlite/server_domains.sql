-- The server_domains index (mirrors each agent's domain registrations).

-- name: ListServerDomains :many
SELECT * FROM server_domains WHERE server_id = ? ORDER BY name ASC;

-- name: UpsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, name) DO UPDATE
SET tls = excluded.tls,
    not_after = excluded.not_after,
    issuer = excluded.issuer,
    updated_at = excluded.updated_at;

-- name: InsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteServerDomain :exec
DELETE FROM server_domains WHERE server_id = ? AND name = ?;

-- name: DeleteServerDomainsByServer :exec
DELETE FROM server_domains WHERE server_id = ?;
