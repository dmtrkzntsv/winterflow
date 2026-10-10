package docker

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
)

// Routes is the docker handler's slice of the route table. Every route is
// server-addressed (server_id query) and agent-bound (202 + request_id).
// Mutations are admin-only via g.Admin.
func (h *Handler) Routes(g api.Guards) []api.Route {
	read := []int{http.StatusBadRequest, http.StatusForbidden}
	admin := []api.Middleware{g.Admin}
	route := func(method, path string, hf http.HandlerFunc, mw []api.Middleware, summary string, req, result any) api.Route {
		return api.Route{
			Method: method, Path: path, Handler: hf, Auth: api.User, Middleware: mw,
			Summary: summary, Tag: "docker", Query: api.ServerQuery{}, Request: req,
			Accepted: true, Result: result, Errors: read,
		}
	}
	return []api.Route{
		route(http.MethodGet, "/api/v1/registry/list", h.ListRegistries, nil,
			"List the agent's Docker registry logins", nil, command.ListRegistriesResponse{}),
		route(http.MethodPost, "/api/v1/registry/create", h.CreateRegistry, admin,
			"Log the agent in to a registry", command.CreateRegistryRequest{}, command.CreateRegistryResponse{}),
		route(http.MethodPost, "/api/v1/registry/delete", h.DeleteRegistry, admin,
			"Log the agent out of a registry", command.DeleteRegistryRequest{}, command.DeleteRegistryResponse{}),
		route(http.MethodGet, "/api/v1/network/list", h.ListNetworks, nil,
			"List Docker networks", nil, command.ListNetworksResponse{}),
		route(http.MethodPost, "/api/v1/network/create", h.CreateNetwork, admin,
			"Create a Docker network", command.CreateNetworkRequest{}, command.CreateNetworkResponse{}),
		route(http.MethodPost, "/api/v1/network/delete", h.DeleteNetwork, admin,
			"Delete a Docker network", command.DeleteNetworkRequest{}, command.DeleteNetworkResponse{}),
		route(http.MethodGet, "/api/v1/volume/list", h.ListVolumes, nil,
			"List Docker volumes", nil, command.ListVolumesResponse{}),
		route(http.MethodPost, "/api/v1/volume/create", h.CreateVolume, admin,
			"Create a Docker volume", command.CreateVolumeRequest{}, command.CreateVolumeResponse{}),
		route(http.MethodPost, "/api/v1/volume/delete", h.DeleteVolume, admin,
			"Delete a Docker volume", command.DeleteVolumeRequest{}, command.DeleteVolumeResponse{}),
		route(http.MethodPost, "/api/v1/agent/update", h.UpdateAgent, admin,
			"Self-update the agent binary", command.UpdateAgentRequest{}, command.UpdateAgentResponse{}),
	}
}
