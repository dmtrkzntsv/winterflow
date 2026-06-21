package command

// Docker resource payloads: registries (docker login/logout) and networks
// (docker network ls/create/rm). These are agent operations — the agent's
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
