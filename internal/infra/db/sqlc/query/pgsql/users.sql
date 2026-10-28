-- Users, their connected accounts and local credentials.

-- name: GetUser :one
SELECT * FROM users WHERE user_id = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: InsertUser :exec
INSERT INTO users (user_id, name, avatar, created_at, last_seen)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteUser :exec
DELETE FROM users WHERE user_id = $1;

-- name: GetConnectedAccountUserID :one
SELECT user_id FROM user_connected_accounts WHERE provider = $1 AND external_id = $2;

-- name: InsertConnectedAccount :exec
INSERT INTO user_connected_accounts (provider, external_id, user_id)
VALUES ($1, $2, $3);

-- name: GetFirstConnectedAccountProvider :one
SELECT provider FROM user_connected_accounts
WHERE user_id = $1
ORDER BY provider ASC, external_id ASC
LIMIT 1;

-- name: InsertCredentials :exec
INSERT INTO user_credentials (user_id, email, password_hash, must_change_password, updated_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetCredentialsByEmail :one
SELECT * FROM user_credentials WHERE email = $1;

-- name: GetCredentialsByUser :one
SELECT * FROM user_credentials WHERE user_id = $1;

-- name: EmailTakenByOther :one
-- EmailTaken for a user adding a password: a password account's email, or
-- the email another user's linked sign-in provider vouched for (the user's
-- own identities do not count).
SELECT EXISTS (
    SELECT 1 FROM user_credentials c WHERE lower(c.email) = sqlc.arg(email)::text
    UNION ALL
    SELECT 1 FROM user_connected_accounts a
    WHERE a.provider <> 'local' AND lower(a.email) = sqlc.arg(email)::text AND a.user_id <> sqlc.arg(user_id)
);

-- name: UserHasIdentityEmail :one
-- Whether one of the user's linked sign-in providers vouched for this
-- (normalized, lower-case) address.
SELECT EXISTS (
    SELECT 1 FROM user_connected_accounts
    WHERE user_id = sqlc.arg(user_id) AND provider <> 'local' AND lower(email) = sqlc.arg(email)::text
);

-- name: EmailTaken :one
-- Whether an account already answers to this (normalized, lower-case)
-- address: a password account's email, or the email a linked sign-in
-- provider vouched for. Compared case-insensitively.
SELECT EXISTS (
    SELECT 1 FROM user_credentials c WHERE lower(c.email) = sqlc.arg(email)::text
    UNION ALL
    SELECT 1 FROM user_connected_accounts a WHERE a.provider <> 'local' AND lower(a.email) = sqlc.arg(email)::text
);

-- LockUserCreation serializes account creation on Postgres: a
-- transaction-scoped advisory lock on one fixed key, taken first by every
-- transaction that creates a user, so the first-user (owner) decision and
-- the email-in-use check each see every account committed before the
-- insert (under READ COMMITTED two creators would otherwise miss each
-- other's uncommitted rows). The two-int4 form keeps the key out of the
-- single-key space the other advisory locks use.
-- name: LockUserCreation :exec
SELECT pg_advisory_xact_lock(hashtext('winterflow'), hashtext('user-creation'));

-- name: UpdatePassword :execrows
UPDATE user_credentials
SET password_hash = $1, must_change_password = $2, updated_at = $3
WHERE user_id = $4;

-- name: RehashPassword :execrows
-- Replaces a verified legacy hash only while it is still the stored one, so a
-- password changed meanwhile is never overwritten with the old one.
UPDATE user_credentials
SET password_hash = $1, updated_at = $2
WHERE user_id = $3 AND password_hash = sqlc.arg(old_hash);
