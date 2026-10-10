package docker

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
)

// Routes is the docker handler's slice of the route table. Every route is
// server-addressed (server_id query) and agent-bound (202 + request_id).
// Any org member may call any of them; 403 means the server belongs to
// another org.
func (h *Handler) Routes(api.Guards) []api.Route {
	errs := []int{http.StatusBadRequest, http.StatusForbidden}
	route := func(method, path string, hf http.HandlerFunc, summary string, req, result any) api.Route {
		return api.Route{
			Method: method, Path: path, Handler: hf, Auth: api.User,
			Summary: summary, Tag: "docker", Query: api.ServerQuery{}, Request: req,
			Accepted: true, Result: result, Errors: errs,
		}
	}
	return []api.Route{
		route(http.MethodGet, "/api/v1/registry/list", h.ListRegistries,
			"List the agent's Docker registry logins", nil, command.ListRegistriesResponse{}),
		route(http.MethodPost, "/api/v1/registry/create", h.CreateRegistry,
			"Log the agent in to a registry", command.CreateRegistryRequest{}, command.CreateRegistryResponse{}),
		route(http.MethodPost, "/api/v1/registry/delete", h.DeleteRegistry,
			"Log the agent out of a registry", command.DeleteRegistryRequest{}, command.DeleteRegistryResponse{}),
		route(http.MethodGet, "/api/v1/network/list", h.ListNetworks,
			"List Docker networks", nil, command.ListNetworksResponse{}),
		route(http.MethodPost, "/api/v1/network/create", h.CreateNetwork,
			"Create a Docker network", command.CreateNetworkRequest{}, command.CreateNetworkResponse{}),
		route(http.MethodPost, "/api/v1/network/delete", h.DeleteNetwork,
			"Delete a Docker network", command.DeleteNetworkRequest{}, command.DeleteNetworkResponse{}),
		route(http.MethodGet, "/api/v1/volume/list", h.ListVolumes,
			"List Docker volumes", nil, command.ListVolumesResponse{}),
		route(http.MethodPost, "/api/v1/volume/create", h.CreateVolume,
			"Create a Docker volume", command.CreateVolumeRequest{}, command.CreateVolumeResponse{}),
		route(http.MethodPost, "/api/v1/volume/delete", h.DeleteVolume,
			"Delete a Docker volume", command.DeleteVolumeRequest{}, command.DeleteVolumeResponse{}),
		route(http.MethodPost, "/api/v1/agent/update", h.UpdateAgent,
			"Self-update the agent binary", command.UpdateAgentRequest{}, command.UpdateAgentResponse{}),
	}
}
