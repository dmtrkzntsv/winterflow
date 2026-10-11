package server

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
)

// AppQuery addresses one app on one server.
type AppQuery struct {
	ServerID string `query:"server_id" required:"true"`
	AppID    string `query:"app_id" required:"true"`
}

// LogsQuery is the query of GET /api/v1/app/get-logs.
type LogsQuery struct {
	ServerID string `query:"server_id" required:"true"`
	AppID    string `query:"app_id" required:"true"`
	Since    int64  `query:"since" description:"Unix seconds; 0 or absent = no lower bound."`
	Tail     int32  `query:"tail" description:"Last N lines; default 200."`
}

// ImageTagsQuery is the query of GET /api/v1/image/get-tags.
type ImageTagsQuery struct {
	ServerID string `query:"server_id" required:"true"`
	Image    string `query:"image" required:"true" description:"Image reference, e.g. nginx or ghcr.io/org/app."`
}

// CheckDomainQuery is the query of GET /api/v1/domains/check.
type CheckDomainQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Server the hostname would be served on."`
	Domain   string `query:"domain" description:"Hostname to check."`
	AppID    string `query:"app_id" description:"App being edited; its own claims don't count."`
}

// Routes is the app handler's slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	// Every server-addressed route: 400 on bad input or a failed dispatch,
	// 403 when the caller's org doesn't own the server.
	server := []int{http.StatusBadRequest, http.StatusForbidden}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-apps", Handler: h.GetApps,
			Auth: api.User, Middleware: []api.Middleware{GetAppsValidationMiddleware},
			Summary: "List a server's apps (DB cache) with their domains", Tag: "apps",
			Query: api.ServerQuery{}, Response: GetAppsResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-apps-status", Handler: h.GetAppsStatus,
			Auth: api.User, Middleware: []api.Middleware{GetAppsValidationMiddleware},
			Summary: "Latest cached container status of a server's apps", Tag: "apps",
			Query: api.ServerQuery{}, Response: command.GetAppsStatusResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Handler: h.RefreshApps,
			Auth: api.User, Middleware: []api.Middleware{GetAppsValidationMiddleware},
			Summary: "Re-list apps from the agent and reconcile the DB cache", Tag: "apps",
			Query: api.ServerQuery{}, Accepted: true, Result: command.ListAppsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/save-app", Handler: h.SaveApp, Auth: api.User,
			Summary: "Create or edit an app (new revision, redeploy unless draft)", Tag: "apps",
			Request: SaveAppRequest{}, Accepted: true, Result: command.SaveAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-app", Handler: h.GetApp, Auth: api.User,
			Summary: "Fetch an app's config, variables and files", Tag: "apps",
			Query: AppQuery{}, Accepted: true, Result: command.GetAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-logs", Handler: h.GetLogs, Auth: api.User,
			Summary: "Fetch an app's container logs", Tag: "apps",
			Query: LogsQuery{}, Accepted: true, Result: command.GetLogsResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/get-revisions", Handler: h.GetRevisions, Auth: api.User,
			Summary: "List an app's revision history", Tag: "apps",
			Query: AppQuery{}, Accepted: true, Result: command.GetRevisionsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/rollback-app", Handler: h.RollbackApp, Auth: api.User,
			Summary: "Restore a revision as a new commit and redeploy", Tag: "apps",
			Request: RollbackAppRequest{}, Accepted: true, Result: command.RollbackAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/image/get-tags", Handler: h.GetImageTags, Auth: api.User,
			Summary: "List registry tags for an image (agent credentials)", Tag: "apps",
			Query: ImageTagsQuery{}, Accepted: true, Result: command.ImageTagsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/control-app", Handler: h.ControlApp, Auth: api.User,
			Summary: "Start, stop, restart or update an app", Tag: "apps",
			Request: ControlAppRequest{}, Accepted: true, Result: command.ControlAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/delete-app", Handler: h.DeleteApp, Auth: api.User,
			Summary: "Delete an app (optionally its volumes)", Tag: "apps",
			Request: DeleteAppRequest{}, Accepted: true, Result: command.DeleteAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/rename-app", Handler: h.RenameApp, Auth: api.User,
			Summary: "Rename an app", Tag: "apps",
			Request: RenameAppRequest{}, Accepted: true, Result: command.RenameAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domain/get-domains", Handler: h.GetDomains, Auth: api.User,
			Summary: "A server's registered domains from the API's index (no agent round-trip)", Tag: "domains",
			Query: api.ServerQuery{}, Response: GetDomainsResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domain/list", Handler: h.ListRegisteredDomains, Auth: api.User,
			Summary: "List the domains registered on a server, with the apps using each", Tag: "domains",
			Query: api.ServerQuery{}, Accepted: true, Result: command.ListDomainsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/domain/create", Handler: h.CreateDomain, Auth: api.User,
			Summary: "Register a domain, or replace its TLS mode or certificate", Tag: "domains",
			Description: "In certificate mode `certificate` is the PEM chain and `key` the PEM private key " +
				"ECIES-encrypted with the server's public key (GET /api/v1/server/get-public-key).",
			Query: api.ServerQuery{}, Request: command.CreateDomainRequest{},
			Accepted: true, Result: command.CreateDomainResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/domain/delete", Handler: h.DeleteDomain, Auth: api.User,
			Summary: "Remove a registered domain no app uses", Tag: "domains",
			Query: api.ServerQuery{}, Request: command.DeleteDomainRequest{},
			Accepted: true, Result: command.DeleteDomainResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domains/check", Handler: h.CheckDomain, Auth: api.User,
			Summary: "Check whether a hostname is free on a server", Tag: "apps",
			Query: CheckDomainQuery{}, Response: CheckDomainResponse{}, Errors: server,
		},
	}
}
