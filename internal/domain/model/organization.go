package model

import (
	"time"
)

type OrganizationRole string

const (
	RoleOwner OrganizationRole = "owner"
	RoleAdmin OrganizationRole = "admin"
)

func (or OrganizationRole) Value() string {
	return string(or)
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
