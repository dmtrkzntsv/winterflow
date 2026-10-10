package server

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the server handler's slice of the route table. Registering a
// server is admin-only via g.Admin.
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/server/get-servers", Handler: h.GetServers, Auth: api.User,
			Summary: "List the caller's servers", Tag: "server",
			Response: GetServersResponse{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/server/get-servers-status", Handler: h.GetServersStatus, Auth: api.User,
			Summary: "Live liveness of the caller's servers", Tag: "server",
			Response: GetServersStatusResponse{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/server/get-public-key", Handler: h.GetPublicKey, Auth: api.User,
			Summary: "The agent's public key for encrypting secrets", Tag: "server",
			Query: api.ServerQuery{}, Response: PublicKeyResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/server/register", Handler: h.Register, Auth: api.User,
			Middleware: []api.Middleware{g.Admin},
			Summary:    "Claim a pending server by its pairing code", Tag: "server",
			Request: RegisterServerRequest{}, Response: RegisterServerResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden},
		},
	}
}
