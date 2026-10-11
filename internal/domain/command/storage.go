package command

import (
	"encoding/json"

	"winterflow/internal/domain/model"
)

// Storages: folders and S3-compatible buckets registered per server. The
// agent's {AGENT_DATA_DIR}/storages/ is the source of truth; the API mirrors
// name, type and location into its server_storages index. A secret access
// key travels only as ECIES ciphertext (browser → agent) and comes back as
// the "<encrypted>" placeholder.

// StorageInfo is one storage as the agent reports it: the registration with
// its secret masked, its location, and the ids of the apps whose compose
// files reference it through WF_STORAGE_ variables.
type StorageInfo struct {
	model.Storage
	Location string   `json:"location"`
	Apps     []string `json:"apps"`
}

// MarshalJSON renders a nil Apps as [] so clients can iterate it unconditionally.
func (s StorageInfo) MarshalJSON() ([]byte, error) {
	type plain StorageInfo
	if s.Apps == nil {
		s.Apps = []string{}
	}
	return json.Marshal(plain(s))
}

type ListStoragesRequest struct{}

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

// SaveStorageRequest adds or replaces a storage. The agent tests it first and
// stores nothing when the test fails. "<encrypted>" as the secret keeps the
// stored one (editing without re-entering it). Renaming is not supported.
type SaveStorageRequest struct {
	model.Storage
}

func (r SaveStorageRequest) Validate() error { return r.Storage.Validate() }

// SaveStorageResponse is the stored storage plus the apps redeployed so they
// pick up the new values. Warnings are redeploys that failed or were skipped;
// the storage itself was saved.
type SaveStorageResponse struct {
	Storage    StorageInfo `json:"storage"`
	Redeployed []string    `json:"redeployed"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// MarshalJSON renders a nil Redeployed as [].
func (r SaveStorageResponse) MarshalJSON() ([]byte, error) {
	type plain SaveStorageResponse
	if r.Redeployed == nil {
		r.Redeployed = []string{}
	}
	return json.Marshal(plain(r))
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

// StorageVariable is a WF_STORAGE_ variable an app's compose file references,
// as app.get reports it for the Variables panel: never with a value.
type StorageVariable struct {
	Name    string `json:"name"`
	Storage string `json:"storage"`
	Secret  bool   `json:"secret"`
}
