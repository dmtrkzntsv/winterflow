package model

type OrganizationRole string

const RoleOwner OrganizationRole = "owner"

func (or OrganizationRole) Value() string {
	return string(or)
}
