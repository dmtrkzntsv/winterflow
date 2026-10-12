package templates

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
)

// ControlInstanceRequest is the body of POST control-instance.
type ControlInstanceRequest struct {
	ServerID string                `json:"server_id" required:"true"`
	Key      string                `json:"key" required:"true"`
	Action   command.ControlAction `json:"action" required:"true" enum:"start,stop,restart"`
	Wait     bool                  `json:"wait,omitempty"`
}

// DeleteInstanceRequest is the body of POST delete-instance.
type DeleteInstanceRequest struct {
	ServerID      string `json:"server_id" required:"true"`
	Key           string `json:"key" required:"true"`
	DeleteVolumes bool   `json:"delete_volumes"`
	DeleteBackups bool   `json:"delete_backups"`
	Wait          bool   `json:"wait,omitempty"`
}

// InstanceKeyRequest is the body of POST unlink-instance.
type InstanceKeyRequest struct {
	ServerID string `json:"server_id" required:"true"`
	Key      string `json:"key" required:"true"`
	Wait     bool   `json:"wait,omitempty"`
}

func (h *Handler) ControlInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[ControlInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.instanceCaller(w, r, req.ServerID, req.Key)
	if !ok {
		return
	}
	out, err := h.uc.ControlInstance(r.Context(), userID, orgID, req.ServerID, req.Key, req.Action)
	if err != nil {
		h.fail(w, err, "control the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "control action dispatched")
}

func (h *Handler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[DeleteInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.instanceCaller(w, r, req.ServerID, req.Key)
	if !ok {
		return
	}
	out, err := h.uc.DeleteInstance(r.Context(), userID, orgID, req.ServerID, req.Key, req.DeleteVolumes, req.DeleteBackups)
	if err != nil {
		h.fail(w, err, "delete the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "delete dispatched")
}

func (h *Handler) UnlinkInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[InstanceKeyRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.instanceCaller(w, r, req.ServerID, req.Key)
	if !ok {
		return
	}
	out, err := h.uc.UnlinkInstance(r.Context(), userID, orgID, req.ServerID, req.Key)
	if err != nil {
		h.fail(w, err, "unlink the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "unlink dispatched")
}
