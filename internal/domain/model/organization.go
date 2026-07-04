package model

// Organization is the tenant: servers belong to it, users are members of it.
// Standalone instances have exactly one.
type Organization struct {
	ID    string `json:"org_id"`
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

type OrganizationRole string

const (
	RoleOwner  OrganizationRole = "owner"
	RoleAdmin  OrganizationRole = "admin"
	RoleMember OrganizationRole = "member"
)

// IsAdmin reports whether the role may administer the organization
// (manage members, servers, and infrastructure settings).
func (or OrganizationRole) IsAdmin() bool {
	return or == RoleOwner || or == RoleAdmin
}

func (or OrganizationRole) Value() string {
	return string(or)
}
