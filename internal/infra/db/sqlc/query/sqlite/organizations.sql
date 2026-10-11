-- Organizations and memberships.

-- name: InsertOrganization :exec
INSERT INTO organizations (organization_id, name, created_at)
VALUES (?, ?, ?);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE organization_id = ?;

-- name: UpdateOrganization :execrows
UPDATE organizations SET name = ?, icon = ?, color = ?
WHERE organization_id = ?;

-- name: InsertOrganizationUser :exec
INSERT INTO organization_users (organization_id, user_id, role, created_at)
VALUES (?, ?, ?, ?);

-- name: GetPrimaryOrganizationID :one
SELECT organization_id FROM organization_users
WHERE user_id = ?
ORDER BY created_at ASC
LIMIT 1;

-- LockOrganizationMember re-reads the caller's membership inside
-- RemoveMember's transaction. SQLite has no row locks (its writers are
-- serialized), so this is a plain SELECT; Postgres adds FOR UPDATE.
-- name: LockOrganizationMember :one
SELECT user_id FROM organization_users
WHERE organization_id = ? AND user_id = ?;

-- name: IsOrganizationMember :one
SELECT EXISTS (
    SELECT 1 FROM organization_users WHERE organization_id = ? AND user_id = ?
);

-- name: ListOrganizationMembers :many
SELECT u.user_id, u.name, u.avatar, u.created_at, u.last_seen
FROM organization_users AS ou
JOIN users AS u ON u.user_id = ou.user_id
WHERE ou.organization_id = ?
ORDER BY ou.created_at ASC, ou.user_id ASC;
