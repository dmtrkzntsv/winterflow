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
	// server's apps (Ref is empty; Payload is command.AppsStatusPayload).
	NotificationAppsStatus NotificationType = "apps_status"
	// NotificationAlert is an alert transition (fired/resolved) on one of
	// the org's servers or apps (Ref is empty; Payload is alert.Notice).
	NotificationAlert NotificationType = "alert"
	// NotificationVarsSynced reports that a server's agent stored a
	// shared-variables snapshot (Payload is VarsSyncedPayload).
	NotificationVarsSynced NotificationType = "vars.synced"
	// NotificationVarsSyncFailed reports that an agent refused a snapshot
	// (Payload is VarsSyncFailedPayload).
	NotificationVarsSyncFailed NotificationType = "vars.sync_failed"
	// NotificationVarsReloaded is one app's shared-variable reload outcome
	// (Payload is command.VarsReloadedPayload).
	NotificationVarsReloaded NotificationType = "vars.reloaded"
)

// VarsSyncedPayload is the payload of a vars.synced notification:
// Undecryptable names the secrets the agent could not decrypt (their
// ciphertexts were dropped, so they now show as missing on this server).
type VarsSyncedPayload struct {
	ServerID      string   `json:"server_id"`
	Revision      int64    `json:"revision"`
	Undecryptable []string `json:"undecryptable"`
}

// VarsSyncFailedPayload is the payload of a vars.sync_failed notification.
type VarsSyncFailedPayload struct {
	ServerID string `json:"server_id"`
	Error    string `json:"error"`
}

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
