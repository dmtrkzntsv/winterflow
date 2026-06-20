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

// GetAppsRequest / GetAppsResponse back the read-only API listing of apps known
// to an agent (model.App is the catalog-level record, distinct from AppPayload).
type GetAppsRequest struct {
	ServerID string `json:"server_id"`
}

type GetAppsResponse struct {
	Apps []model.App `json:"apps"`
}
