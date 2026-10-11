-- The server_domains index (mirrors each agent's domain registrations).

-- name: ListServerDomains :many
SELECT * FROM server_domains WHERE server_id = $1 ORDER BY name ASC;

-- name: UpsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (server_id, name) DO UPDATE
SET tls = EXCLUDED.tls,
    not_after = EXCLUDED.not_after,
    issuer = EXCLUDED.issuer,
    updated_at = EXCLUDED.updated_at;

-- name: InsertServerDomain :exec
INSERT INTO server_domains (server_id, name, tls, not_after, issuer, updated_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: DeleteServerDomain :exec
DELETE FROM server_domains WHERE server_id = $1 AND name = $2;

-- name: DeleteServerDomainsByServer :exec
DELETE FROM server_domains WHERE server_id = $1;
