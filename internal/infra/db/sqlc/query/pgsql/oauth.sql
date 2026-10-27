-- OAuth authorization server (MCP clients): clients, codes, grants.

-- name: InsertOAuthClient :exec
INSERT INTO oauth_clients (id, name, redirect_uris, secret_hash, created_at, last_used_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetOAuthClient :one
SELECT * FROM oauth_clients WHERE id = $1;

-- name: MarkOAuthClientUsed :exec
UPDATE oauth_clients SET last_used_at = $1 WHERE id = $2;

-- name: PurgeUnusedOAuthClients :execrows
DELETE FROM oauth_clients WHERE last_used_at IS NULL AND created_at < $1;

-- name: CountUnusedOAuthClients :one
SELECT COUNT(*) FROM oauth_clients WHERE last_used_at IS NULL;

-- name: InsertOAuthCode :exec
INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, resource, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ConsumeOAuthCode :one
-- Single use: the DELETE is the read, so two concurrent exchanges of one code
-- cannot both get a row.
DELETE FROM oauth_codes WHERE code_hash = $1
RETURNING code_hash, client_id, user_id, redirect_uri, code_challenge, resource, created_at, expires_at;

-- name: PurgeExpiredOAuthCodes :exec
DELETE FROM oauth_codes WHERE expires_at < $1;

-- name: InsertOAuthGrant :exec
INSERT INTO oauth_grants (id, user_id, client_id, refresh_token_hash, prev_refresh_token_hash, rotated_at, created_at, last_used_at, expires_at, revoked_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetOAuthGrant :one
SELECT * FROM oauth_grants WHERE id = $1;

-- name: GetOAuthGrantByRefreshHash :one
SELECT * FROM oauth_grants WHERE refresh_token_hash = $1;

-- name: GetOAuthGrantBySpentRefreshHash :one
SELECT g.* FROM oauth_refresh_tokens_spent s
JOIN oauth_grants g ON g.id = s.grant_id
WHERE s.token_hash = $1;

-- name: InsertOAuthSpentRefreshHash :exec
INSERT INTO oauth_refresh_tokens_spent (token_hash, grant_id, spent_at)
VALUES ($1, $2, $3);

-- name: RotateOAuthGrant :execrows
-- Conditional on the hash being rotated away, so of two concurrent refreshes
-- presenting the same token exactly one rotates.
UPDATE oauth_grants
SET prev_refresh_token_hash = refresh_token_hash, refresh_token_hash = $1, rotated_at = $2, last_used_at = $3, expires_at = $4
WHERE id = $5 AND refresh_token_hash = $6 AND revoked_at IS NULL;

-- name: RevokeOAuthGrant :execrows
UPDATE oauth_grants SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL;

-- name: RevokeUserOAuthGrants :execrows
UPDATE oauth_grants SET revoked_at = $1 WHERE user_id = $2 AND revoked_at IS NULL;

-- name: RevokeOtherOAuthGrants :many
-- Re-authorizing a client replaces the user's earlier connection to it: every
-- other unrevoked grant for the same (user, client) goes, returning the ids so
-- their access tokens can be revoked too.
UPDATE oauth_grants SET revoked_at = $1
WHERE user_id = $2 AND client_id = $3 AND id <> $4 AND revoked_at IS NULL
RETURNING id;

-- LockUserOAuthGrants row-locks the user inside a code exchange's
-- transaction before its grant is inserted, so two exchanges for one user
-- run one after the other and the later one's RevokeOtherOAuthGrants sees
-- the earlier one's committed grant (under READ COMMITTED it would
-- otherwise see neither, leaving both live). FOR NO KEY UPDATE leaves
-- inserts that only reference the user (KEY SHARE) unblocked.
-- name: LockUserOAuthGrants :one
SELECT user_id FROM users WHERE user_id = $1
FOR NO KEY UPDATE;

-- name: ListUnrevokedOAuthGrants :many
SELECT g.id, g.user_id, g.client_id, g.refresh_token_hash, g.prev_refresh_token_hash, g.rotated_at,
       g.created_at, g.last_used_at, g.expires_at, g.revoked_at, c.name, c.redirect_uris
FROM oauth_grants g
JOIN oauth_clients c ON c.id = g.client_id
WHERE g.user_id = $1 AND g.revoked_at IS NULL
ORDER BY g.created_at, g.id;

-- name: DeleteDeadOAuthGrants :execrows
DELETE FROM oauth_grants
WHERE (revoked_at IS NOT NULL AND revoked_at < $1)
   OR expires_at < $2;

-- Sign-in with a provider: linked identities (user_connected_accounts),
-- in-flight states.

-- name: GetIdentityByProviderSubject :one
SELECT provider, external_id, user_id, email, created_at, issuer
FROM user_connected_accounts
WHERE provider = $1 AND external_id = $2;

-- name: GetIdentityByUserProvider :one
SELECT provider, external_id, user_id, email, created_at, issuer
FROM user_connected_accounts
WHERE user_id = $1 AND provider = $2;

-- The 'local' rows are the email+password accounts' own marker, not a
-- linked provider: the identity lists and counts leave them out.

-- name: ListIdentitiesByUser :many
SELECT provider, external_id, user_id, email, created_at, issuer
FROM user_connected_accounts
WHERE user_id = $1 AND provider <> 'local'
ORDER BY created_at, provider;

-- name: CountIdentitiesByUser :one
SELECT COUNT(*) FROM user_connected_accounts WHERE user_id = $1 AND provider <> 'local';

-- name: InsertIdentity :exec
INSERT INTO user_connected_accounts (provider, external_id, user_id, email, created_at, issuer)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: InsertIdentityIfAbsent :execrows
-- 0 rows: the provider account is already linked (to anyone).
INSERT INTO user_connected_accounts (provider, external_id, user_id, email, created_at, issuer)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (provider, external_id) DO NOTHING;

-- name: RekeyIdentity :execrows
-- Moves a legacy row (Google accounts linked by the previous auth library
-- were keyed by the SHA-1 of the subject) to the subject itself.
UPDATE user_connected_accounts SET external_id = sqlc.arg(subject) WHERE provider = sqlc.arg(provider) AND external_id = sqlc.arg(legacy_id);

-- LockUserIdentities row-locks the user inside an identity write's
-- transaction, so a link and an unlink (or two unlinks) of one user run
-- one after the other.
-- name: LockUserIdentities :one
SELECT user_id FROM users WHERE user_id = $1
FOR UPDATE;

-- name: UserHasPassword :one
SELECT EXISTS (SELECT 1 FROM user_credentials WHERE user_id = $1);

-- name: SignInEmailInUse :one
-- Whether an account already answers to this address: a password
-- account's email, or the email a linked provider vouched for.
SELECT EXISTS (
    SELECT 1 FROM user_credentials c WHERE c.email = sqlc.arg(address)
    UNION ALL
    SELECT 1 FROM user_connected_accounts a WHERE a.provider <> 'local' AND a.email = sqlc.arg(address)
);

-- name: UpdateIdentityEmail :execrows
UPDATE user_connected_accounts SET email = $1 WHERE provider = $2 AND external_id = $3;

-- name: DeleteIdentityByUserProvider :execrows
DELETE FROM user_connected_accounts WHERE user_id = $1 AND provider = $2;

-- name: InsertOAuthLoginState :exec
INSERT INTO oauth_login_states (state_hash, provider, nonce, code_verifier, flow_hash, intent, return_to, link_user_id, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetOAuthLoginState :one
SELECT * FROM oauth_login_states WHERE state_hash = $1;

-- name: ConsumeOAuthLoginState :one
-- Single use: the DELETE is the read, so two callbacks presenting one state
-- cannot both get the row.
DELETE FROM oauth_login_states WHERE state_hash = $1
RETURNING state_hash, provider, nonce, code_verifier, flow_hash, intent, return_to, link_user_id, created_at, expires_at;

-- name: DeleteOAuthLoginState :execrows
DELETE FROM oauth_login_states WHERE state_hash = $1;

-- name: DeleteOAuthLoginStatesByLinkUser :execrows
DELETE FROM oauth_login_states WHERE link_user_id = $1;

-- name: DeleteExpiredOAuthLoginStates :execrows
DELETE FROM oauth_login_states WHERE expires_at < $1;
