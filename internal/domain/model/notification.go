package model

import "time"

type NotificationType string

const (
	// NotificationOperationResult carries the result of a dispatched command,
	// correlated to its caller by Ref (= request_id).
	NotificationOperationResult NotificationType = "operation_result"
	// NotificationServerStatus is an unsolicited liveness transition for a
	// server (Ref is empty; Payload is ServerStatusPayload).
	NotificationServerStatus NotificationType = "server_status"
	// NotificationAppsStatus is an unsolicited container-status report for a
	// server's apps (Ref is empty; Payload is {server_id, apps}).
	NotificationAppsStatus NotificationType = "apps_status"
)

// ServerStatusPayload is the payload of a server_status notification.
type ServerStatusPayload struct {
	ServerID string `json:"server_id"`
	Liveness string `json:"liveness"` // status.Liveness value: "online" | "unknown"
}

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
