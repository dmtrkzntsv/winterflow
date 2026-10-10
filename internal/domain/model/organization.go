package model

// Organization is the tenant: servers belong to it, users are members of it.
// Standalone instances have exactly one.
type Organization struct {
	ID    string `json:"org_id"`
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

// OrganizationRole is the value stored in organization_users.role. Winterflow
// has no roles (every member may do everything) and nothing reads the
// column. It is still written explicitly, rather than left to its 'member'
// default, so that an older, role-aware binary run against this database
// treats every new account as an administrator.
type OrganizationRole string

const (
	// RoleOwner marks the account that created the org (claim, self-signup,
	// first OAuth login).
	RoleOwner OrganizationRole = "owner"
	// RoleAdmin marks an account created inside an existing org.
	RoleAdmin OrganizationRole = "admin"
)

func (or OrganizationRole) Value() string {
	return string(or)
}
