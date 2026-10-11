package server

import (
	"context"
	"errors"
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// UpdateBackupsRequest is the body of POST /api/v1/app/update-backups: the
// app's backups list, replaced as a whole.
type UpdateBackupsRequest struct {
	ServerID string `json:"server_id"`
	command.UpdateBackupsRequest
}

// Validate refuses a body without a backups list (absent or null: a
// misspelt key must not wipe every backup setting); an explicit [] means
// "no overrides".
func (r UpdateBackupsRequest) Validate() error {
	if err := r.UpdateBackupsRequest.Validate(); err != nil {
		return err
	}
	if r.Backups == nil {
		return errors.New("backups is required (use [] for no overrides)")
	}
	return nil
}

// RunBackupRequest is the body of POST /api/v1/app/run-backup.
type RunBackupRequest struct {
	ServerID string `json:"server_id"`
	command.RunBackupRequest
}

// DeleteRestorePointRequest is the body of POST
// /api/v1/app/delete-restore-point.
type DeleteRestorePointRequest struct {
	ServerID string `json:"server_id"`
	command.DeleteRestorePointRequest
}

// RestorePointsQuery is the query of GET /api/v1/app/list-restore-points.
type RestorePointsQuery struct {
	ServerID string `query:"server_id" required:"true"`
	AppID    string `query:"app_id" required:"true"`
	Backup   string `query:"backup" required:"true" description:"Backup name."`
}

// RestoreBackupRequest is the body of POST /api/v1/app/restore-backup.
type RestoreBackupRequest struct {
	ServerID string `json:"server_id"`
	command.RestoreBackupRequest
}

// DeleteOrphanRequest is the body of POST /api/v1/storage/delete-orphan.
type DeleteOrphanRequest struct {
	ServerID string `json:"server_id"`
	command.DeleteOrphanRequest
}

// OrphansQuery is the query of GET /api/v1/storage/list-orphans.
type OrphansQuery struct {
	ServerID string `query:"server_id" required:"true"`
	Name     string `query:"name" required:"true" description:"Storage name."`
}

func (r UpdateBackupsRequest) server() string      { return r.ServerID }
func (r RunBackupRequest) server() string          { return r.ServerID }
func (r DeleteRestorePointRequest) server() string { return r.ServerID }
func (r RestoreBackupRequest) server() string      { return r.ServerID }
func (r DeleteOrphanRequest) server() string       { return r.ServerID }

// serverBody is a JSON body naming its server: server_id plus the agent
// request.
type serverBody interface {
	Validate() error
	server() string
}

// dispatchServerBody serves an app-scoped command whose body carries
// server_id: it decodes and validates the body (400 with the reason),
// authorizes the caller for the server (401/403), dispatches, and answers
// 202 with the request id. A rejection by the usecase (model.ErrRejected)
// is a 400 with its message.
func dispatchServerBody[T serverBody](h *Handler, w http.ResponseWriter, r *http.Request, what string,
	dispatch func(ctx context.Context, userID, serverID string, req T) (string, error)) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[T](w, r)
	if !ok {
		return
	}
	if req.server() == "" {
		webutil.Error(w, "server_id is required", nil)
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, req.server()) {
		return
	}
	reqID, err := dispatch(r.Context(), userID, req.server(), req)
	if err != nil {
		if errors.Is(err, model.ErrRejected) {
			webutil.Error(w, err.Error(), nil)
			return
		}
		webutil.Error(w, "failed to "+what, nil)
		return
	}
	accepted(w, what+" dispatched", reqID)
}

// UpdateBackups replaces an app's backups list; the agent checks it against
// the compose file, commits it and redeploys only when the compose profiles
// or storage variables change.
func (h *Handler) UpdateBackups(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "update backups", func(ctx context.Context, userID, serverID string, req UpdateBackupsRequest) (string, error) {
		return h.usecase.UpdateBackups(ctx, userID, serverID, req.UpdateBackupsRequest)
	})
}

// RunBackup runs a backup now; the outcome arrives over SSE when the run
// ends.
func (h *Handler) RunBackup(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "run backup", func(ctx context.Context, userID, serverID string, req RunBackupRequest) (string, error) {
		return h.usecase.RunBackup(ctx, userID, serverID, req.RunBackupRequest)
	})
}

// DeleteRestorePoint deletes one restore point.
func (h *Handler) DeleteRestorePoint(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "delete restore point", func(ctx context.Context, userID, serverID string, req DeleteRestorePointRequest) (string, error) {
		return h.usecase.DeleteRestorePoint(ctx, userID, serverID, req.DeleteRestorePointRequest)
	})
}

// ListRestorePoints lists a backup's restore points; they arrive over SSE.
func (h *Handler) ListRestorePoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := command.ListRestorePointsRequest{AppID: q.Get("app_id"), Backup: q.Get("backup")}
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.ListRestorePoints(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to list restore points", nil)
		return
	}
	accepted(w, "list restore points dispatched", reqID)
}

// RestoreBackup restores a restore point in place; the outcome arrives over
// SSE when the restore ends.
func (h *Handler) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "restore backup", func(ctx context.Context, userID, serverID string, req RestoreBackupRequest) (string, error) {
		return h.usecase.RestoreBackup(ctx, userID, serverID, req.RestoreBackupRequest)
	})
}

// ListOrphans lists the backups in a storage whose app is not on the
// server; they arrive over SSE.
func (h *Handler) ListOrphans(w http.ResponseWriter, r *http.Request) {
	req := command.ListOrphansRequest{Name: r.URL.Query().Get("name")}
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.ListOrphans(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to list orphaned backups", nil)
		return
	}
	accepted(w, "list orphaned backups dispatched", reqID)
}

// DeleteOrphan deletes an orphan's backups from a storage.
func (h *Handler) DeleteOrphan(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "delete orphaned backups", func(ctx context.Context, userID, serverID string, req DeleteOrphanRequest) (string, error) {
		return h.usecase.DeleteOrphan(ctx, userID, serverID, req.DeleteOrphanRequest)
	})
}
