package model

import (
	"time"
)

type SubscriptionStatus string

const (
	SubscriptionStatusPaid   SubscriptionStatus = "paid"
	SubscriptionStatusUnpaid SubscriptionStatus = "unpaid"
)

func (s SubscriptionStatus) Value() string {
	return string(s)
}

type OrganizationRole string

const (
	RoleOwner OrganizationRole = "owner"
	RoleAdmin OrganizationRole = "admin"
)

func (or OrganizationRole) Value() string {
	return string(or)
}

type Organization struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	SubscriptionStatus SubscriptionStatus `json:"subscription_status"`
	CreatedAt          time.Time          `json:"created_at"`
}
