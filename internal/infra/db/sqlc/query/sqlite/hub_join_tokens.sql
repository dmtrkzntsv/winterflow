-- One-time hub join tokens (hashed at rest).

-- name: InsertHubJoinToken :exec
INSERT INTO hub_join_tokens (token_hash, expires_at, created_at)
VALUES (?, ?, ?);

-- name: ConsumeHubJoinToken :execrows
UPDATE hub_join_tokens SET used_at = ?, used_by_hub = ?
WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?;

-- name: PruneHubJoinTokens :exec
DELETE FROM hub_join_tokens
WHERE (used_at IS NULL AND expires_at < ?) OR used_at < ?;
