package command

import (
	"errors"
	"regexp"
)

// Docker resource payloads: registries (docker login/logout), networks
// (docker network ls/create/rm) and volumes (docker volume ls/create/rm). These are agent operations — the agent's
// Docker daemon is the source of truth — so list operations also round-trip to
// the agent and return over SSE.

// --- registries ---------------------------------------------------------------

// Registry is a configured Docker registry. Only the address is reported back;
// credentials are never returned.
type Registry struct {
	Address string `json:"address"`
}

type ListRegistriesRequest struct{}

type ListRegistriesResponse struct {
	Registries []Registry `json:"registries"`
}

// CreateRegistryRequest logs in to a registry. Password may be an ECIES payload
// when Encrypted is set (the agent decrypts it before `docker login`), mirroring
// the app-secret scheme.
type CreateRegistryRequest struct {
	Address   string `json:"address"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

type CreateRegistryResponse struct {
	Address string `json:"address"`
}

type DeleteRegistryRequest struct {
	Address string `json:"address"`
}

type DeleteRegistryResponse struct {
	Address string `json:"address"`
}

// --- networks -----------------------------------------------------------------

// Network is a Docker network.
type Network struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Driver string `json:"driver,omitempty"`
	Scope  string `json:"scope,omitempty"`
}

type ListNetworksRequest struct{}

type ListNetworksResponse struct {
	Networks []Network `json:"networks"`
}

type CreateNetworkRequest struct {
	Name   string `json:"name"`
	Driver string `json:"driver,omitempty"` // default "bridge" when empty
}

type CreateNetworkResponse struct {
	Name string `json:"name"`
}

type DeleteNetworkRequest struct {
	Name string `json:"name"`
}

type DeleteNetworkResponse struct {
	Name string `json:"name"`
}

// --- volumes ------------------------------------------------------------------

// Volume is a Docker volume as seen by the agent. AppID is derived from the
// compose project label wf-<appID>; Orphan means that label names an app that
// no longer exists on the agent.
type Volume struct {
	Name      string `json:"name"`
	Driver    string `json:"driver,omitempty"`
	AppID     string `json:"app_id,omitempty"`
	Project   string `json:"project,omitempty"`
	InUse     bool   `json:"in_use"`
	Orphan    bool   `json:"orphan"`
	Anonymous bool   `json:"anonymous"`
}

type ListVolumesRequest struct{}

type ListVolumesResponse struct {
	Volumes []Volume `json:"volumes"`
}

type CreateVolumeRequest struct {
	Name string `json:"name"`
}

func (r CreateVolumeRequest) Validate() error { return ValidateVolumeName(r.Name) }

type CreateVolumeResponse struct {
	Name string `json:"name"`
}

type DeleteVolumeRequest struct {
	Name string `json:"name"`
}

func (r DeleteVolumeRequest) Validate() error { return ValidateVolumeName(r.Name) }

type DeleteVolumeResponse struct {
	Name string `json:"name"`
}

var volumeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidateVolumeName applies Docker's volume-name rule. Because the first
// character must be alphanumeric, a name can never be parsed as a CLI flag.
func ValidateVolumeName(name string) error {
	if name == "" {
		return errors.New("volume name is required")
	}
	if len(name) > 255 {
		return errors.New("volume name is too long (max 255)")
	}
	if !volumeNameRe.MatchString(name) {
		return errors.New("volume name must start with a letter or digit and contain only letters, digits, '_', '.' or '-'")
	}
	return nil
}

// --- image.tags -------------------------------------------------------------

type ImageTagsRequest struct {
	Image string `json:"image"` // e.g. "nginx", "ghcr.io/org/app:tag"
}

type ImageTagsResponse struct {
	Image string   `json:"image"`
	Tags  []string `json:"tags"`
}
