package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
)

// acceptedRequestID is the standard 202 body for fire-and-forward operations.
type acceptedRequestID struct {
	RequestID string `json:"request_id"`
}

func accepted(w http.ResponseWriter, msg, requestID string) {
	webutil.Accepted(w, msg, acceptedRequestID{RequestID: requestID})
}

type controlAppRequest struct {
	ServerID string                `json:"server_id"`
	AppID    string                `json:"app_id"`
	Action   command.ControlAction `json:"action"`
}

// ControlApp dispatches a start/stop/restart/update action to the app's agent
// (fire-and-forward). Returns 202 + request_id; the result arrives over SSE.
func (h *Handler) ControlApp(w http.ResponseWriter, r *http.Request) {
	userID, err := webutil.GetUserID(r)
	if err != nil || userID == "" {
		webutil.Error(w, "unauthorized", nil)
		return
	}
	var req controlAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webutil.Error(w, "invalid request body", nil)
		return
	}
	if req.ServerID == "" || req.AppID == "" {
		webutil.Error(w, "server_id and app_id are required", nil)
		return
	}
	switch req.Action {
	case command.ControlStart, command.ControlStop, command.ControlRestart, command.ControlUpdate:
	default:
		webutil.Error(w, "invalid action", nil)
		return
	}

	requestID, err := h.usecase.ControlApp(r.Context(), userID, req.ServerID, req.AppID, req.Action)
	if err != nil {
		webutil.Error(w, "failed to control app", nil)
		return
	}
	accepted(w, "control action dispatched", requestID)
}

type deleteAppRequest struct {
	ServerID string `json:"server_id"`
	AppID    string `json:"app_id"`
}

// DeleteApp dispatches an app.delete to the agent. Returns 202 + request_id.
func (h *Handler) DeleteApp(w http.ResponseWriter, r *http.Request) {
	userID, err := webutil.GetUserID(r)
	if err != nil || userID == "" {
		webutil.Error(w, "unauthorized", nil)
		return
	}
	var req deleteAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webutil.Error(w, "invalid request body", nil)
		return
	}
	if req.ServerID == "" || req.AppID == "" {
		webutil.Error(w, "server_id and app_id are required", nil)
		return
	}
	requestID, err := h.usecase.DeleteApp(r.Context(), userID, req.ServerID, req.AppID)
	if err != nil {
		webutil.Error(w, "failed to delete app", nil)
		return
	}
	accepted(w, "delete dispatched", requestID)
}

type renameAppRequest struct {
	ServerID string `json:"server_id"`
	AppID    string `json:"app_id"`
	Name     string `json:"name"`
}

// RenameApp dispatches an app.rename to the agent. Returns 202 + request_id.
func (h *Handler) RenameApp(w http.ResponseWriter, r *http.Request) {
	userID, err := webutil.GetUserID(r)
	if err != nil || userID == "" {
		webutil.Error(w, "unauthorized", nil)
		return
	}
	var req renameAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webutil.Error(w, "invalid request body", nil)
		return
	}
	if req.ServerID == "" || req.AppID == "" || req.Name == "" {
		webutil.Error(w, "server_id, app_id and name are required", nil)
		return
	}
	requestID, err := h.usecase.RenameApp(r.Context(), userID, req.ServerID, req.AppID, req.Name)
	if err != nil {
		webutil.Error(w, "failed to rename app", nil)
		return
	}
	accepted(w, "rename dispatched", requestID)
}

// GetApp dispatches an app.get to the agent (config + revisions). Returns
// 202 + request_id; the result arrives over SSE. Query: server_id, app_id,
// optional revision.
func (h *Handler) GetApp(w http.ResponseWriter, r *http.Request) {
	userID, err := webutil.GetUserID(r)
	if err != nil || userID == "" {
		webutil.Error(w, "unauthorized", nil)
		return
	}
	serverID := r.URL.Query().Get("server_id")
	appID := r.URL.Query().Get("app_id")
	if serverID == "" || appID == "" {
		webutil.Error(w, "server_id and app_id are required", nil)
		return
	}
	requestID, err := h.usecase.GetApp(r.Context(), userID, serverID, appID)
	if err != nil {
		webutil.Error(w, "failed to get app", nil)
		return
	}
	accepted(w, "app fetch dispatched", requestID)
}

// GetLogs dispatches an app.logs to the agent. Returns 202 + request_id; the
// log entries arrive over SSE. Query: server_id, app_id, optional since, tail.
func (h *Handler) GetLogs(w http.ResponseWriter, r *http.Request) {
	userID, err := webutil.GetUserID(r)
	if err != nil || userID == "" {
		webutil.Error(w, "unauthorized", nil)
		return
	}
	serverID := r.URL.Query().Get("server_id")
	appID := r.URL.Query().Get("app_id")
	if serverID == "" || appID == "" {
		webutil.Error(w, "server_id and app_id are required", nil)
		return
	}
	var since int64
	if s := r.URL.Query().Get("since"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			since = v
		}
	}
	var tail int32 = 200
	if t := r.URL.Query().Get("tail"); t != "" {
		if v, err := strconv.ParseInt(t, 10, 32); err == nil {
			tail = int32(v)
		}
	}
	requestID, err := h.usecase.GetLogs(r.Context(), userID, serverID, appID, since, tail)
	if err != nil {
		webutil.Error(w, "failed to get logs", nil)
		return
	}
	accepted(w, "logs fetch dispatched", requestID)
}
