package command

import "winterflow/internal/domain/model"

// AppPayload is the on-the-wire form of an app to deploy. It mirrors v1's AppV1:
// a JSON config blob plus the variable and file contents the agent renders into
// the deployment. Contents are kept as bytes so binary files survive the trip.
type AppPayload struct {
	AppID     string        `json:"app_id"`
	Config    []byte        `json:"config"`    // JSON-encoded model.AppConfig (ported later)
	Variables []ContentItem `json:"variables"` // id -> content
	Files     []ContentItem `json:"files"`     // id -> content
}

// ContentItem is a single variable or file: a UUID and its raw content.
type ContentItem struct {
	ID      string `json:"id"`
	Content []byte `json:"content"`
}

// --- app.save -----------------------------------------------------------------

type SaveAppRequest struct {
	App AppPayload `json:"app"`
}

type SaveAppResponse struct {
	AppID    string `json:"app_id"`
	Revision uint32 `json:"revision"`
}

// --- app.get ------------------------------------------------------------------

type GetAppRequest struct {
	AppID    string `json:"app_id"`
	Revision uint32 `json:"revision,omitempty"` // 0 = latest
}

type GetAppResponse struct {
	App                AppPayload `json:"app"`
	Revision           uint32     `json:"revision"`
	AvailableRevisions []uint32   `json:"available_revisions"`
}

// --- apps.status --------------------------------------------------------------

// ContainerStatusCode mirrors v1's ContainerStatusCode enum.
type ContainerStatusCode int

const (
	ContainerStatusUnknown ContainerStatusCode = iota
	ContainerStatusActive
	ContainerStatusIdle
	ContainerStatusRestarting
	ContainerStatusProblematic
	ContainerStatusStopped
)

type ContainerStatus struct {
	ContainerID string              `json:"container_id"`
	Name        string              `json:"name"`
	StatusCode  ContainerStatusCode `json:"status_code"`
	ExitCode    int32               `json:"exit_code"`
	Error       string              `json:"error,omitempty"`
}

type AppStatus struct {
	AppID      string              `json:"app_id"`
	StatusCode ContainerStatusCode `json:"status_code"`
	Containers []ContainerStatus   `json:"containers"`
}

type GetAppsStatusRequest struct{}

type GetAppsStatusResponse struct {
	Apps []AppStatus `json:"apps"`
}

// --- apps.list ----------------------------------------------------------------

// ListAppsRequest asks the agent for the apps it actually has deployed (its
// filesystem is the source of truth). The API reconciles its DB cache against
// the response (model.App is the catalog-level info record, distinct from
// AppPayload, which carries the deployable config/vars/files).
type ListAppsRequest struct{}

type ListAppsResponse struct {
	Apps []model.App `json:"apps"`
}

// --- app.control --------------------------------------------------------------

// ControlAction identifies a lifecycle action for an app.
type ControlAction string

const (
	ControlStart   ControlAction = "start"
	ControlStop    ControlAction = "stop"
	ControlRestart ControlAction = "restart"
	ControlUpdate  ControlAction = "update"
)

type ControlAppRequest struct {
	AppID  string        `json:"app_id"`
	Action ControlAction `json:"action"`
}

type ControlAppResponse struct {
	AppID  string        `json:"app_id"`
	Action ControlAction `json:"action"`
}

// --- app.delete ---------------------------------------------------------------

type DeleteAppRequest struct {
	AppID string `json:"app_id"`
}

type DeleteAppResponse struct {
	AppID string `json:"app_id"`
}

// --- app.rename ---------------------------------------------------------------

type RenameAppRequest struct {
	AppID string `json:"app_id"`
	Name  string `json:"name"`
}

type RenameAppResponse struct {
	AppID string `json:"app_id"`
	Name  string `json:"name"`
}

// --- app.logs -----------------------------------------------------------------

// LogLevel mirrors v1's best-effort log level classification.
type LogLevel int8

const (
	LogLevelUnknown LogLevel = iota
	LogLevelTrace
	LogLevelDebug
	LogLevelInfo
	LogLevelWarn
	LogLevelError
	LogLevelFatal
)

type LogEntry struct {
	Timestamp   int64    `json:"timestamp"`
	Level       LogLevel `json:"level"`
	Message     string   `json:"message"`
	ContainerID string   `json:"container_id,omitempty"`
	Container   string   `json:"container,omitempty"`
}

type GetLogsRequest struct {
	AppID string `json:"app_id"`
	Since int64  `json:"since,omitempty"` // unix seconds; 0 = no lower bound
	Tail  int32  `json:"tail,omitempty"`  // 0 = all
}

type GetLogsResponse struct {
	AppID string     `json:"app_id"`
	Logs  []LogEntry `json:"logs"`
}
