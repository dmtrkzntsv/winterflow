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
			Summary: "Delete an app (optionally its volumes and its backups)", Tag: "apps",
			Description: "Without `delete_backups` the app's backups are kept, marked as a deleted app's: the agent's daily sweep " +
				"deletes them once older than its BACKUP_RETENTION_DAYS.",
			Request: DeleteAppRequest{}, Accepted: true, Result: command.DeleteAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/rename-app", Handler: h.RenameApp, Auth: api.User,
			Summary: "Rename an app", Tag: "apps",
			Request: RenameAppRequest{}, Accepted: true, Result: command.RenameAppResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/update-backups", Handler: h.UpdateBackups, Auth: api.User,
			Summary: "Replace an app's backups list (new revision; redeploys only when profiles or storage variables change)", Tag: "backups",
			Description: "`backups` is the whole list of the app's backup settings: entries named like a backup of the compose file's " +
				"`x-winterflow.backups` override it (only the fields that differ, never `type`); other entries are backups added here " +
				"and carry `type` and every field it needs. `enabled: true` needs `storage`, a storage on the server. " +
				"`backups` is required: `[]` means no overrides, an absent or null list is a 400.",
			Request: UpdateBackupsRequest{}, Accepted: true, Result: command.UpdateBackupsResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/list-restore-points", Handler: h.ListRestorePoints, Auth: api.User,
			Summary: "List a backup's restore points, newest first (30s deadline)", Tag: "backups",
			Query: RestorePointsQuery{}, Accepted: true, Result: command.ListRestorePointsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/run-backup", Handler: h.RunBackup, Auth: api.User,
			Summary: "Back up now (refused while the same backup is queued or running)", Tag: "backups",
			Description: "The result arrives when the run ends, which can take up to 30 minutes after it starts. A second request while the same backup is queued or running fails at once (its result is an error).",
			Request:     RunBackupRequest{}, Accepted: true, Result: command.RunBackupResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/restore-backup", Handler: h.RestoreBackup, Auth: api.User,
			Summary: "Restore a restore point in place (the app is stopped, its data saved as a pre-restore point first)", Tag: "backups",
			Description: "The result arrives when the restore ends. On a failure that leaves the data as it was, the app is " +
				"started again; a files restore that fails half-way leaves the app stopped and names the pre-restore point to restore.",
			Request: RestoreBackupRequest{}, Accepted: true, Result: command.RestoreBackupResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/delete-restore-point", Handler: h.DeleteRestorePoint, Auth: api.User,
			Summary: "Delete one restore point (any kind)", Tag: "backups",
			Request: DeleteRestorePointRequest{}, Accepted: true, Result: command.DeleteRestorePointResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domain/get-domains", Handler: h.GetDomains, Auth: api.User,
			Summary: "A server's domains from the API's index (no agent round-trip)", Tag: "domains",
			Query: api.ServerQuery{}, Response: GetDomainsResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domain/list", Handler: h.ListRegisteredDomains, Auth: api.User,
			Summary: "List the domains on a server, with the apps using each", Tag: "domains",
			Query: api.ServerQuery{}, Accepted: true, Result: command.ListDomainsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/domain/create", Handler: h.CreateDomain, Auth: api.User,
			Summary: "Add a domain, or replace its TLS mode or certificate", Tag: "domains",
			Description: "In certificate mode `certificate` is the PEM chain and `key` the PEM private key " +
				"ECIES-encrypted with the server's public key (GET /api/v1/server/get-public-key).",
			Query: api.ServerQuery{}, Request: command.CreateDomainRequest{},
			Accepted: true, Result: command.CreateDomainResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/domain/delete", Handler: h.DeleteDomain, Auth: api.User,
			Summary: "Remove a domain no app uses", Tag: "domains",
			Query: api.ServerQuery{}, Request: command.DeleteDomainRequest{},
			Accepted: true, Result: command.DeleteDomainResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/storage/get-storages", Handler: h.GetStorages, Auth: api.User,
			Summary: "A server's storages from the API's index (no agent round-trip, no secrets)", Tag: "storages",
			Query: api.ServerQuery{}, Response: GetStoragesResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/storage/list", Handler: h.ListStorages, Auth: api.User,
			Summary: "List the storages on a server, with the apps using each", Tag: "storages",
			Query: api.ServerQuery{}, Accepted: true, Result: command.ListStoragesResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/storage/test", Handler: h.TestStorage, Auth: api.User,
			Summary: "Test a storage by writing and deleting a probe object (30s deadline)", Tag: "storages",
			Description: "`s3.secret_access_key` is ECIES-encrypted with the server's public key " +
				"(GET /api/v1/server/get-public-key), or `<encrypted>` to reuse the secret stored under the same name. " +
				"A failed probe is a successful result with `ok: false`.",
			Query: api.ServerQuery{}, Request: command.TestStorageRequest{},
			Accepted: true, Result: command.TestStorageResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/storage/save", Handler: h.SaveStorage, Auth: api.User,
			Summary: "Add or edit a storage (tested first; redeploys the running apps that use it)", Tag: "storages",
			Description: "`create: true` adds a storage and fails when the name exists; otherwise the storage must exist " +
				"and keep its type. `s3.secret_access_key` is ECIES-encrypted with the server's public key, or `<encrypted>` " +
				"to keep the stored one. Nothing is saved when the connection test fails.",
			Query: api.ServerQuery{}, Request: command.SaveStorageRequest{},
			Accepted: true, Result: command.SaveStorageResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/storage/delete", Handler: h.DeleteStorage, Auth: api.User,
			Summary: "Remove a storage no app uses", Tag: "storages",
			Query: api.ServerQuery{}, Request: command.DeleteStorageRequest{},
			Accepted: true, Result: command.DeleteStorageResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/storage/list-orphans", Handler: h.ListOrphans, Auth: api.User,
			Summary: "List the backups in a storage whose app is not on this server", Tag: "storages",
			Query: OrphansQuery{}, Accepted: true, Result: command.ListOrphansResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/storage/delete-orphan", Handler: h.DeleteOrphan, Auth: api.User,
			Summary: "Delete the backups of an app that is not on this server from a storage", Tag: "storages",
			Request: DeleteOrphanRequest{}, Accepted: true, Result: command.DeleteOrphanResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domains/check", Handler: h.CheckDomain, Auth: api.User,
			Summary: "Check whether a hostname is free on a server", Tag: "apps",
			Query: CheckDomainQuery{}, Response: CheckDomainResponse{}, Errors: server,
		},
	}
}
