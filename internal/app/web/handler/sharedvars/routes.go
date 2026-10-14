package sharedvars

import (
	"net/http"
	"slices"

	"winterflow/internal/app/web/api"
	svuc "winterflow/internal/domain/usecase/sharedvars"
)

// SaveVariableRequest is the body of POST /api/v1/org/save-shared-variable.
type SaveVariableRequest struct {
	Name        string            `json:"name" required:"true" description:"Env-var name; COMPOSE_PROJECT_NAME and WF_* are reserved."`
	IsSecret    bool              `json:"is_secret"`
	Value       *string           `json:"value,omitempty" description:"Plain variables only."`
	Ciphertexts map[string]string `json:"ciphertexts,omitempty" description:"Secrets only: server_id -> ECIES ciphertext for that server's key (GET /api/v1/org/list-encryption-keys). A new value covers every keyed server; a gap fill only servers missing it; none keeps every stored copy."`
	Description string            `json:"description"`
	Reload      bool              `json:"reload,omitempty" description:"Reload the affected running apps, one at a time, once each server stored the change (the org's vars_reload rollout)."`
}

func (r SaveVariableRequest) input() svuc.SaveInput {
	return svuc.SaveInput{Name: r.Name, IsSecret: r.IsSecret, Value: r.Value, Ciphertexts: r.Ciphertexts, Description: r.Description, Reload: r.Reload}
}

// SaveServerVariableRequest is the body of POST /api/v1/server/save-shared-variable.
type SaveServerVariableRequest struct {
	ServerID    string            `json:"server_id" required:"true"`
	Name        string            `json:"name" required:"true" description:"Env-var name; COMPOSE_PROJECT_NAME and WF_* are reserved."`
	IsSecret    bool              `json:"is_secret"`
	Value       *string           `json:"value,omitempty" description:"Plain variables only."`
	Ciphertexts map[string]string `json:"ciphertexts,omitempty" description:"Secrets only: exactly one entry, for server_id."`
	Description string            `json:"description"`
	Reload      bool              `json:"reload,omitempty"`
}

func (r SaveServerVariableRequest) input() svuc.SaveInput {
	return svuc.SaveInput{Name: r.Name, IsSecret: r.IsSecret, Value: r.Value, Ciphertexts: r.Ciphertexts, Description: r.Description, Reload: r.Reload}
}

// DeleteVariableRequest is the body of POST /api/v1/org/delete-shared-variable.
type DeleteVariableRequest struct {
	ID     string `json:"id" required:"true"`
	Reload bool   `json:"reload,omitempty"`
}

// DeleteServerVariableRequest is the body of POST /api/v1/server/delete-shared-variable.
type DeleteServerVariableRequest struct {
	ServerID string `json:"server_id" required:"true"`
	ID       string `json:"id" required:"true"`
	Reload   bool   `json:"reload,omitempty"`
}

// ResyncRequest addresses one server, the servers one variable reaches, or
// (neither) every org server.
type ResyncRequest struct {
	ServerID   string `json:"server_id,omitempty"`
	VariableID string `json:"variable_id,omitempty"`
}

// ReloadRequest addresses one server (optionally filtered by names or app
// ids) or, by variable, every server it reaches.
type ReloadRequest struct {
	ServerID   string   `json:"server_id,omitempty"`
	VariableID string   `json:"variable_id,omitempty"`
	Names      []string `json:"names,omitempty"`
	AppIDs     []string `json:"app_ids,omitempty"`
}

// Routes is the shared variables slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	bad := []int{http.StatusBadRequest}
	both := []int{http.StatusBadRequest, http.StatusForbidden}
	const pushed = "Answers once the change is stored; each affected server's agent then gets the new snapshot " +
		"(vars.synced / vars.sync_failed over SSE)."
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/org/list-shared-variables", Handler: h.ListOrg, Auth: api.User,
			Summary: "The org's shared variables (secrets masked) with coverage and per-server sync state", Tag: "variables",
			Response: svuc.OrgVariables{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/save-shared-variable", Handler: h.SaveOrg, Auth: api.User,
			Summary: "Create or update an org-level shared variable", Tag: "variables", Description: pushed,
			Request: SaveVariableRequest{}, Response: svuc.VariableView{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/delete-shared-variable", Handler: h.DeleteOrg, Auth: api.User,
			Summary: "Delete an org-level shared variable", Tag: "variables", Description: pushed,
			Request: DeleteVariableRequest{}, Errors: both,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/list-encryption-keys", Handler: h.EncryptionKeys, Auth: api.User,
			Summary: "Public keys of the org's servers for encrypting secrets in the browser", Tag: "variables",
			Response: svuc.EncryptionKeys{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/server/list-shared-variables", Handler: h.ListServer, Auth: api.User,
			Summary: "A server's shared variables, the org's as inherited, built-ins and missing org secrets", Tag: "variables",
			Query: api.ServerQuery{}, Response: svuc.ServerVariables{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/server/save-shared-variable", Handler: h.SaveServer, Auth: api.User,
			Summary: "Create or update a server-level shared variable", Tag: "variables", Description: pushed,
			Request: SaveServerVariableRequest{}, Response: svuc.VariableView{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/server/delete-shared-variable", Handler: h.DeleteServer, Auth: api.User,
			Summary: "Delete a server-level shared variable", Tag: "variables", Description: pushed,
			Request: DeleteServerVariableRequest{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/shared-variables/resync", Handler: h.Resync, Auth: api.User,
			Summary: "Push the full shared-variables snapshot again", Tag: "variables",
			Description: "Answers with the servers dispatched to; outcomes arrive as vars.synced / vars.sync_failed over SSE.",
			Request:     ResyncRequest{}, Response: svuc.DispatchedServers{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/shared-variables/reload", Handler: h.Reload, Auth: api.User,
			Summary: "Reload running apps whose shared variables changed", Tag: "variables",
			Description: "Queues, or extends, the org's vars_reload rollout: the stale running apps in scope reload one " +
				"at a time and the first failure stops it; 409 while the org's reload rollout is being stopped. Answers with the " +
				"servers in scope; progress arrives as rollout events, each app's outcome as vars.reloaded over SSE.",
			Request: ReloadRequest{}, Response: svuc.DispatchedServers{}, Errors: append(slices.Clone(both), http.StatusConflict),
		},
	}
}
