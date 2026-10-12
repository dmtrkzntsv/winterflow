package templates

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	tuc "winterflow/internal/domain/usecase/templates"
)

// UpdateInstanceRequest is the body of POST update-instance.
type UpdateInstanceRequest struct {
	ServerID   string            `json:"server_id" required:"true"`
	Key        string            `json:"key" required:"true"`
	Parameters map[string]string `json:"parameters,omitempty" description:"New plain values, merged over the stored ones; a value may not contain $."`
	Secrets    map[string]string `json:"secrets,omitempty" description:"New secret values; a secret left out keeps its value (a pending instance holds none: send every required secret again)."`
	Backups    map[string]string `json:"backups,omitempty" description:"Backup name -> storage name, replacing the template default's storage. Accepted only while the instance is pending; afterwards change backups in the app's Backups tab. A pending instance keeps none of the create's overrides: send them again."`
	Wait       bool              `json:"wait,omitempty"`
}

// UpgradeInstanceRequest is the body of POST upgrade-instance.
type UpgradeInstanceRequest struct {
	ServerID   string            `json:"server_id" required:"true"`
	Key        string            `json:"key" required:"true"`
	Revision   int64             `json:"revision" required:"true" minimum:"1"`
	Parameters map[string]string `json:"parameters,omitempty" description:"Values for parameters the revision adds or changes; a value may not contain $."`
	Secrets    map[string]string `json:"secrets,omitempty" description:"Secret values for the revision; a secret left out keeps its value (a pending instance holds none: send every required secret again)."`
	Backups    map[string]string `json:"backups,omitempty" description:"Backup name -> storage name, replacing the template default's storage. Accepted only while the instance is pending; afterwards change backups in the app's Backups tab. A pending instance keeps none of the create's overrides: send them again."`
	Wait       bool              `json:"wait,omitempty"`
}

func (h *Handler) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[UpdateInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	out, err := h.uc.UpdateInstance(r.Context(), userID, orgID, tuc.UpdateInput{
		ServerID: req.ServerID, Key: req.Key, Params: req.Parameters, Secrets: req.Secrets, Backups: req.Backups,
	})
	if err != nil {
		h.fail(w, err, "update the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "instance save dispatched")
}

func (h *Handler) UpgradeInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[UpgradeInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	if req.Revision < 1 {
		webutil.Error(w, "revision is required", nil)
		return
	}
	out, err := h.uc.UpdateInstance(r.Context(), userID, orgID, tuc.UpdateInput{
		ServerID: req.ServerID, Key: req.Key, Revision: req.Revision, Params: req.Parameters, Secrets: req.Secrets, Backups: req.Backups,
	})
	if err != nil {
		h.fail(w, err, "upgrade the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "instance save dispatched")
}
