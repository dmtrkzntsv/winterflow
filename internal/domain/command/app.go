package command

import (
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

// ValidAppID reports whether id is safe to use as a single path element under
// the agent's apps-data dir. It is the one rule shared by the API handlers
// (reject early with 400) and the agent dispatcher (fail closed): a legit id
// is a generated UUID, so anything empty, "."/"..", or containing a path
// separator (or NUL) is refused rather than normalised.
func ValidAppID(id string) bool { return model.ValidAppID(id) }

// validateAppID is ValidAppID as an error, shared by the app-scoped requests'
// Validate methods (the agent dispatcher calls Validate on every decoded
// request before it reaches the orchestrator).
func validateAppID(id string) error { return model.ValidateAppID(id) }

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

// ItemName is the item's key: its Name, or its ID when the name is empty.
func (c ContentItem) ItemName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.ID
}

// EncryptedPlaceholder is the sentinel value meaning "preserve the existing
// stored secret" — the browser sends it instead of re-encrypting an unchanged
// secret on edit.
const EncryptedPlaceholder = model.EncryptedSecret

// --- app.save -----------------------------------------------------------------

type SaveAppRequest struct {
	App AppPayload `json:"app"`
	// Draft commits the changes without deploying them; the commit sits ahead
	// of the deployed mark until explicitly deployed.
	Draft bool `json:"draft,omitempty"`
}

// Validate checks the app id, that the config blob is a JSON object, and
// refuses user variables named like storage variables (WF_STORAGE_,
// WF_BACKUP_): those only come from storages.
func (r SaveAppRequest) Validate() error {
	if err := validateAppID(r.App.AppID); err != nil {
		return err
	}
	if err := model.CheckConfigObject(r.App.Config); err != nil {
		return err
	}
	for _, v := range r.App.Variables {
		if err := model.ReservedVariableError(v.ItemName()); err != nil {
			return err
		}
	}
	return nil
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

func (r GetAppRequest) Validate() error { return validateAppID(r.AppID) }

type GetAppResponse struct {
	App AppPayload `json:"app"`
	// StorageVariables are the WF_STORAGE_ variables the app's compose files
	// reference: shown read-only in the Variables panel, never with values.
	StorageVariables []StorageVariable `json:"storage_variables,omitempty"`
	// Backups are the app's effective backups (recipe + overrides);
	// BackupsError says why they could not be read (an invalid recipe).
	Backups      []recipe.EffectiveBackup `json:"backups,omitempty"`
	BackupsError string                   `json:"backups_error,omitempty"`
	// BackupsBase identifies the stored backups list (the overrides): the
	// base an update-backups sent from this view carries.
	BackupsBase string `json:"backups_base,omitempty"`
	// BackupTargets are where a backup added in the UI can point (the
	// compose files' volumes and each service's mounts); nil when a compose
	// file can't be read.
	BackupTargets *recipe.Targets `json:"backup_targets,omitempty"`
	// RetentionDays is the server's BACKUP_RETENTION_DAYS (0: no ceiling).
	RetentionDays int `json:"retention_days,omitempty"`
	// DeclaredVariables are the variables the compose files declare under
	// x-winterflow (the editor's form), Expose their expose entries with
	// the host ports the agent allocated, XWinterflowWarnings what the block
	// gets away with (a literal volume name, an unused variable). Empty
	// when the compose files can't be read (BackupsError says why).
	DeclaredVariables   []xwinterflow.DeclaredVariable `json:"declared_variables,omitempty"`
	Expose              []ExposedPort                  `json:"expose,omitempty"`
	XWinterflowWarnings []string                       `json:"x_winterflow_warnings,omitempty"`
}

// ExposedPort is one x-winterflow expose entry with its host port.
type ExposedPort struct {
	ID        string `json:"id"` // <container>:<port>/<protocol>, the key of its pin
	Container string `json:"container"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"` // http | tcp | udp
	// HostPort is 0 until a save allocates one.
	HostPort int `json:"host_port"`
	// Bind is 127.0.0.1 for http (reached through the ingress), 0.0.0.0
	// for tcp and udp (reached on the server's address).
	Bind   string `json:"bind"`
	Pinned bool   `json:"pinned"`
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

func (r GetRevisionsRequest) Validate() error { return validateAppID(r.AppID) }

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

func (r RollbackAppRequest) Validate() error { return validateAppID(r.AppID) }

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
	// StaleVars names the shared variables whose rendered values differ from
	// the agent's current snapshot; empty = fresh. A stale app needs a
	// vars.reload (or its next deploy) to pick the new values up.
	StaleVars []string `json:"stale_vars,omitempty"`
	// Backups is the health of each of the app's backups (none: omitted).
	// It never changes StatusCode.
	Backups []BackupHealth `json:"backups,omitempty"`
}

type GetAppsStatusRequest struct{}

type GetAppsStatusResponse struct {
	Apps []AppStatus `json:"apps"`
	// VarsRevision is the agent's shared-variables snapshot revision. Nil
	// from agents that predate shared variables: the API never drift-pushes
	// vars.sync to those.
	VarsRevision *int64 `json:"vars_revision,omitempty"`
}

// AppsStatusPayload is the payload of an apps_status SSE notification
// (model.NotificationAppsStatus): one server's latest container status,
// pushed to the org's members when it changes.
type AppsStatusPayload struct {
	ServerID string      `json:"server_id"`
	Apps     []AppStatus `json:"apps"`
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

func (r ControlAppRequest) Validate() error { return validateAppID(r.AppID) }

type ControlAppResponse struct {
	AppID  string        `json:"app_id"`
	Action ControlAction `json:"action"`
}

// --- app.delete ---------------------------------------------------------------

type DeleteAppRequest struct {
	AppID string `json:"app_id"`
	// DeleteVolumes also removes the app's compose-owned Docker volumes
	// (never external ones). False keeps them, as before.
	DeleteVolumes bool `json:"delete_volumes,omitempty"`
	// DeleteBackups also deletes the app's backups (every copy, in every
	// storage its backups use). False keeps them, marked as a deleted app's,
	// for BACKUP_RETENTION_DAYS.
	DeleteBackups bool `json:"delete_backups,omitempty"`
}

func (r DeleteAppRequest) Validate() error { return validateAppID(r.AppID) }

type DeleteAppResponse struct {
	AppID string `json:"app_id"`
	// Warnings lists cleanup steps that failed after the app was removed
	// (e.g. a volume still in use).
	Warnings []string `json:"warnings,omitempty"`
	// VolumesDeleted is true only when the agent honoured delete_volumes
	// (the volume sweep ran). An agent that predates the flag never sets it,
	// which lets the UI tell "volumes removed" from "request ignored".
	VolumesDeleted bool `json:"volumes_deleted,omitempty"`
	// BackupsDeleted is true only when the agent honoured delete_backups
	// (an agent that predates the flag keeps them).
	BackupsDeleted bool `json:"backups_deleted,omitempty"`
}

// --- app.rename ---------------------------------------------------------------

type RenameAppRequest struct {
	AppID string `json:"app_id"`
	Name  string `json:"name"`
}

func (r RenameAppRequest) Validate() error { return validateAppID(r.AppID) }

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

func (r GetLogsRequest) Validate() error { return validateAppID(r.AppID) }

type GetLogsResponse struct {
	AppID string     `json:"app_id"`
	Logs  []LogEntry `json:"logs"`
}
