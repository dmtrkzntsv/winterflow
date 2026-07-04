package model

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
