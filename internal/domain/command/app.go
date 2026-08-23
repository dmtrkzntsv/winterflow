package command

import "winterflow/internal/domain/model"

// AppPayload is the on-the-wire form of an app to deploy. It mirrors v1's AppV1:
// a JSON config blob plus the variable and file contents the agent renders into
// the deployment. Contents are kept as bytes so binary files survive the trip.
type AppPayload struct {
	AppID     string        `json:"app_id"`
	Config    []byte        `json:"config"`    // JSON-encoded app config blob
	Variables []ContentItem `json:"variables"` // id -> content
	Files     []ContentItem `json:"files"`     // id -> content
	// Source, when set, makes this a git-sourced app: the agent clones the
	// repo into the app's source/ dir and pins the deployed SHA per save.
	Source *SourcePayload `json:"source,omitempty"`
}

// SourcePayload configures deployment from an upstream git repository.
type SourcePayload struct {
	RepoURL     string `json:"repo_url"`
	Branch      string `json:"branch"`
	ComposePath string `json:"compose_path,omitempty"` // compose file inside the repo
	AutoUpdate  bool   `json:"auto_update"`
	PollSeconds int    `json:"poll_seconds,omitempty"` // 0 = default (120)
	// Token is ECIES ciphertext for private repos, the "<encrypted>"
	// placeholder to keep the stored token, or empty for anonymous access.
	Token []byte `json:"token,omitempty"`
}

// ContentItem is a single variable or file carried in an app payload.
//
//   - Name is the authoritative key: the ${VAR} name for a variable, or the
//     filename (relative path) for a file.
//   - Encrypted marks a secret. When set, Content is an ECIES payload (base64)
//     that the agent decrypts with its private key before writing/substituting,
//     unless Content is the sentinel "<encrypted>", which means "keep the value
//     already stored for this item" (used when editing without re-entering a
//     secret).
type ContentItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Content   []byte `json:"content"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

// EncryptedPlaceholder is the sentinel value meaning "preserve the existing
// stored secret" — the browser sends it instead of re-encrypting an unchanged
// secret on edit.
const EncryptedPlaceholder = "<encrypted>"

// --- app.save -----------------------------------------------------------------

type SaveAppRequest struct {
	App AppPayload `json:"app"`
	// Draft commits the changes without deploying them; the commit sits ahead
	// of the deployed mark until explicitly deployed.
	Draft bool `json:"draft,omitempty"`
}

type SaveAppResponse struct {
	AppID    string `json:"app_id"`
	Revision string `json:"revision"` // git commit hash of the save
	// Warnings are non-fatal ingress problems (config excluded, reload
	// failed): the save itself succeeded.
	Warnings []string `json:"warnings,omitempty"`
}

// --- app.get ------------------------------------------------------------------

type GetAppRequest struct {
	AppID string `json:"app_id"`
}

type GetAppResponse struct {
	App AppPayload `json:"app"`
}

// --- app.revisions --------------------------------------------------------

// RevisionInfo is one commit of an app's git history.
type RevisionInfo struct {
	Hash      string `json:"hash"`
	Subject   string `json:"subject"`
	Timestamp int64  `json:"timestamp"` // unix seconds
}

type GetRevisionsRequest struct {
	AppID string `json:"app_id"`
}

type GetRevisionsResponse struct {
	AppID     string         `json:"app_id"`
	Current   string         `json:"current"`  // HEAD hash
	Deployed  string         `json:"deployed"` // last successfully deployed hash ("" = unknown)
	Revisions []RevisionInfo `json:"revisions"`
}

// --- app.rollback ---------------------------------------------------------

type RollbackAppRequest struct {
	AppID string `json:"app_id"`
	Hash  string `json:"hash"`
}

type RollbackAppResponse struct {
	AppID    string `json:"app_id"`
	Revision string `json:"revision"` // the new HEAD created by the rollback
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
