package model

import "time"

type NotificationType string

const (
	NotificationOperationResult NotificationType = "operation_result"
)

type NotificationStatus int

const (
	NotificationStatusError   NotificationStatus = 1
	NotificationStatusSuccess NotificationStatus = 0
)

type Notification struct {
	Type      NotificationType   `json:"type"`
	Ref       string             `json:"ref"`
	Status    NotificationStatus `json:"status,omitempty"`
	Payload   interface{}        `json:"payload,omitempty"`
	Error     string             `json:"error,omitempty"`
	Timestamp time.Time          `json:"timestamp"`
}
