package command

import (
	"winterflow/internal/domain/model"
)

// Storages: folders and S3-compatible buckets registered per server. The
// agent's {AGENT_DATA_DIR}/storages/ is the source of truth; the API mirrors
// name, type and location into its server_storages index. A secret access
// key travels only as ECIES ciphertext (browser → agent) and comes back as
// the "<encrypted>" placeholder.

// StorageInfo is one storage as the agent reports it: the registration with
// its secret masked, its location, and the ids of the apps whose compose
// files reference it through WF_STORAGE_ variables. The agent always sends
// Apps as a list ([] for none), so clients can iterate it unconditionally.
type StorageInfo struct {
	model.Storage
	Location string   `json:"location"`
	Apps     []string `json:"apps"`
}

// Summary is the index row of the storage as reported.
func (i StorageInfo) Summary() model.StorageSummary {
	return model.StorageSummary{Name: i.Name, Type: i.Type, Location: i.Location}
}

// ListStoragesRequest lists the storages. RegistryOnly skips finding the
// apps that use each one (a read of every app's compose files): the API's
// index sync needs the registry alone.
type ListStoragesRequest struct {
	RegistryOnly bool `json:"registry_only,omitempty"`
}

type ListStoragesResponse struct {
	Storages []StorageInfo `json:"storages"`
}

// TestStorageRequest checks a storage without saving it: the agent writes and
// deletes a probe object. The secret is ECIES ciphertext, or "<encrypted>"
// to reuse the one stored under the same name.
type TestStorageRequest struct {
	model.Storage
}

func (r TestStorageRequest) Validate() error { return r.Storage.Validate() }

// TestStorageResponse reports the probe: OK, or the reason it failed.
type TestStorageResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// SaveStorageRequest adds (Create) or edits a storage. The agent tests it
// first and stores nothing when the test fails. Create refuses a name the
// server already has; an edit refuses a name it doesn't have and a change of
// type (apps reference the type's fields). "<encrypted>" as the secret keeps
// the stored one (editing without re-entering it). Renaming is not supported.
type SaveStorageRequest struct {
	model.Storage
	Create bool `json:"create"`
}

func (r SaveStorageRequest) Validate() error { return r.Storage.Validate() }

// SaveStorageResponse is the stored storage plus the apps redeployed so they
// pick up the new values ([] for none). Warnings are redeploys that failed or
// were skipped; the storage itself was saved.
type SaveStorageResponse struct {
	Storage    StorageInfo `json:"storage"`
	Redeployed []string    `json:"redeployed"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// DeleteStorageRequest removes a storage; the agent refuses while an app
// references it.
type DeleteStorageRequest struct {
	Name string `json:"name"`
}

func (r DeleteStorageRequest) Validate() error { return model.ValidateStorageName(r.Name) }

type DeleteStorageResponse struct {
	Name string `json:"name"`
}

// StorageVariable is a WF_STORAGE_ or WF_BACKUP_ variable an app's compose
// file references, as app.get reports it for the Variables panel: never
// with a value. Backup names the backup of a WF_BACKUP_ variable (Storage
// is then the storage that backup uses, "" while it has none).
type StorageVariable struct {
	Name    string `json:"name"`
	Storage string `json:"storage"`
	Backup  string `json:"backup,omitempty"`
	Secret  bool   `json:"secret"`
}

// --- storage.orphans, storage.delete-orphan ---------------------------------

// ListOrphansRequest lists the backups in a storage whose app is not on this
// server: a deleted app's, or another server's sharing the storage.
type ListOrphansRequest struct {
	Name string `json:"name"`
}

func (r ListOrphansRequest) Validate() error { return model.ValidateStorageName(r.Name) }

// Orphan is one app's backup folder in a storage, the app not being on this
// server. AppName and DeletedAt come from the folder's .deleted marker (an
// app deleted here); PurgeAt is when the daily sweep deletes the last copy
// (the newest copy plus BACKUP_RETENTION_DAYS), 0 without a marker (another
// server's folder is never deleted automatically) or without a ceiling.
// Times are unix seconds; Bytes counts every object in the folder.
type Orphan struct {
	AppID     string `json:"app_id"`
	AppName   string `json:"app_name,omitempty"`
	DeletedAt int64  `json:"deleted_at,omitempty"`
	PurgeAt   int64  `json:"purge_at,omitempty"`
	Copies    int    `json:"copies"`
	Bytes     int64  `json:"bytes"`
	Newest    int64  `json:"newest,omitempty"`
}

// ListOrphansResponse lists a storage's orphans by app id ([] for none).
type ListOrphansResponse struct {
	Name    string   `json:"name"`
	Orphans []Orphan `json:"orphans"`
}

// DeleteOrphanRequest deletes every object of an orphan's folder; the agent
// refuses when the app is on this server.
type DeleteOrphanRequest struct {
	Name  string `json:"name"`
	AppID string `json:"app_id"`
}

func (r DeleteOrphanRequest) Validate() error {
	if err := model.ValidateStorageName(r.Name); err != nil {
		return err
	}
	return validateAppID(r.AppID)
}

// DeleteOrphanResponse names the folder deleted and how many objects it held.
type DeleteOrphanResponse struct {
	Name    string `json:"name"`
	AppID   string `json:"app_id"`
	Deleted int    `json:"deleted"`
}
