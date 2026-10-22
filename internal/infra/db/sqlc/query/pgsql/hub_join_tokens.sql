-- One-time hub join tokens (hashed at rest).

-- name: InsertHubJoinToken :exec
INSERT INTO hub_join_tokens (token_hash, expires_at, created_at)
VALUES ($1, $2, $3);

-- name: ConsumeHubJoinToken :execrows
UPDATE hub_join_tokens SET used_at = $1, used_by_hub = $2
WHERE token_hash = $3 AND used_at IS NULL AND expires_at > $4;

-- name: PruneHubJoinTokens :exec
DELETE FROM hub_join_tokens
WHERE (used_at IS NULL AND expires_at < $1) OR used_at < $2;
