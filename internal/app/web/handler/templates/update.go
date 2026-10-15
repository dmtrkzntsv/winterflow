package templates

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
)

// UpdateInstanceRequest is the body of POST update-instance.
type UpdateInstanceRequest struct {
	ServerID string                           `json:"server_id" required:"true"`
	Key      string                           `json:"key" required:"true"`
	Values   map[string]string                `json:"values,omitempty" description:"New plain values, merged over the stored ones; an empty value clears one (the compose file's own value stands). A value may not contain $."`
	Secrets  map[string]string                `json:"secrets,omitempty" description:"New secret values; a secret left out keeps its value (a pending instance holds none: send every required secret again)."`
	Hosts    *[]model.InstanceHost            `json:"hosts,omitempty" description:"Present: the instance's hostnames from now on (an empty list removes them); absent: kept."`
	Backups  map[string]template.BackupChoice `json:"backups,omitempty" description:"Backup settings by name. Accepted only while the instance is pending: a sent list replaces the stored settings (send them all), none sends the stored ones (the create's) again. Afterwards change backups in the app's Backups tab."`
	Wait     bool                             `json:"wait,omitempty"`
}

// UpgradeInstanceRequest is the body of POST upgrade-instance.
type UpgradeInstanceRequest struct {
	ServerID string                           `json:"server_id" required:"true"`
	Key      string                           `json:"key" required:"true"`
	Revision int64                            `json:"revision" required:"true" minimum:"1"`
	Values   map[string]string                `json:"values,omitempty" description:"Values for variables the revision adds or changes; a value may not contain $."`
	Secrets  map[string]string                `json:"secrets,omitempty" description:"Secret values for the revision; a secret left out keeps its value (a pending instance holds none: send every required secret again)."`
	Hosts    *[]model.InstanceHost            `json:"hosts,omitempty" description:"Present: the instance's hostnames from now on; absent: kept (each must still be an http expose entry of the revision)."`
	Backups  map[string]template.BackupChoice `json:"backups,omitempty" description:"Backup settings by name. Accepted only while the instance is pending: a sent list replaces the stored settings, none sends the stored ones (the create's) again; backups the revision adds start off."`
	Wait     bool                             `json:"wait,omitempty"`
}

func (h *Handler) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[UpdateInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.instanceCaller(w, r, req.ServerID, req.Key)
	if !ok {
		return
	}
	h.update(w, r, userID, orgID, tuc.UpdateInput{
		ServerID: req.ServerID, Key: req.Key, Values: req.Values, Secrets: req.Secrets, Hosts: req.Hosts, Backups: req.Backups,
	}, req.Wait, "update the instance")
}

func (h *Handler) UpgradeInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[UpgradeInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.instanceCaller(w, r, req.ServerID, req.Key)
	if !ok {
		return
	}
	if req.Revision < 1 {
		webutil.Error(w, "revision is required", nil)
		return
	}
	h.update(w, r, userID, orgID, tuc.UpdateInput{
		ServerID: req.ServerID, Key: req.Key, Revision: req.Revision, Values: req.Values, Secrets: req.Secrets, Hosts: req.Hosts, Backups: req.Backups,
	}, req.Wait, "upgrade the instance")
}

// update runs an update or upgrade and answers it; what names it in a
// failure.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, userID, orgID string, in tuc.UpdateInput, wait bool, what string) {
	out, err := h.uc.UpdateInstance(r.Context(), userID, orgID, in)
	if err != nil {
		h.fail(w, err, what)
		return
	}
	h.respond(w, r, out, wait, "instance save dispatched")
}
