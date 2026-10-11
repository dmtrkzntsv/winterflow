-- Organizations and memberships.

-- name: InsertOrganization :exec
INSERT INTO organizations (organization_id, name, created_at)
VALUES ($1, $2, $3);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE organization_id = $1;

-- name: UpdateOrganization :execrows
UPDATE organizations SET name = $1, icon = $2, color = $3
WHERE organization_id = $4;

-- name: InsertOrganizationUser :exec
INSERT INTO organization_users (organization_id, user_id, role, created_at)
VALUES ($1, $2, $3, $4);

-- name: GetPrimaryOrganizationID :one
SELECT organization_id FROM organization_users
WHERE user_id = $1
ORDER BY created_at ASC
LIMIT 1;

-- LockOrganizationMember re-reads and row-locks the caller's membership
-- inside RemoveMember's transaction, so two members removing each other
-- concurrently cannot empty the org.
-- name: LockOrganizationMember :one
SELECT user_id FROM organization_users
WHERE organization_id = $1 AND user_id = $2
FOR UPDATE;

-- name: IsOrganizationMember :one
SELECT EXISTS (
    SELECT 1 FROM organization_users WHERE organization_id = $1 AND user_id = $2
);

-- name: ListOrganizationMembers :many
SELECT u.user_id, u.name, u.avatar, u.created_at, u.last_seen
FROM organization_users AS ou
JOIN users AS u ON u.user_id = ou.user_id
WHERE ou.organization_id = $1
ORDER BY ou.created_at ASC, ou.user_id ASC;
