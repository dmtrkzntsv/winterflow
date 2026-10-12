package templates

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	tuc "winterflow/internal/domain/usecase/templates"
)

// CreateInstanceRequest is the body of POST create-instance.
type CreateInstanceRequest struct {
	ServerID   string            `json:"server_id" required:"true"`
	Key        string            `json:"key" required:"true" description:"The caller's id for the instance, unique in the org among instances not unlinked; a retried create with the same key and values makes no second app."`
	TemplateID string            `json:"template_id" required:"true"`
	Revision   int64             `json:"revision,omitempty" description:"Absent or 0: the latest."`
	Parameters map[string]string `json:"parameters,omitempty" description:"Plain parameter values by name."`
	Secrets    map[string]string `json:"secrets,omitempty" description:"Secret parameter values by name, ECIES-encrypted with the server's public key."`
	Backups    map[string]string `json:"backups,omitempty" description:"Backup name -> storage name, replacing the template default's storage."`
	Wait       bool              `json:"wait,omitempty" description:"Hold the request for the outcome (at most 10 minutes)."`
}

// InstanceQuery is the query of GET get-instance.
type InstanceQuery struct {
	ServerID string `query:"server_id" required:"true"`
	Key      string `query:"key" required:"true"`
}

// InstancesQuery is the query of GET get-instances.
type InstancesQuery struct {
	ServerID   string `query:"server_id" description:"Only this server's instances."`
	TemplateID string `query:"template_id" description:"Only this template's instances."`
}

// requireKey answers 400 without a key.
func requireKey(w http.ResponseWriter, key string) bool {
	if key == "" {
		webutil.Error(w, "key is required", nil)
		return false
	}
	return true
}

func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[CreateInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	if !requireID(w, "template_id", req.TemplateID) {
		return
	}
	out, err := h.uc.CreateInstance(r.Context(), userID, orgID, tuc.CreateInput{
		ServerID: req.ServerID, Key: req.Key, TemplateID: req.TemplateID, Revision: req.Revision,
		Params: req.Parameters, Secrets: req.Secrets, Backups: req.Backups,
	})
	if err != nil {
		h.fail(w, err, "create the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "instance save dispatched")
}

func (h *Handler) GetInstance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	_, orgID, ok := h.serverCaller(w, r, q.Get("server_id"))
	if !ok || !requireKey(w, q.Get("key")) {
		return
	}
	out, err := h.uc.GetInstance(r.Context(), orgID, q.Get("server_id"), q.Get("key"))
	if err != nil {
		h.fail(w, err, "read the instance")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) GetInstances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	serverID := q.Get("server_id")
	var orgID string
	if serverID != "" {
		_, org, ok := h.serverCaller(w, r, serverID)
		if !ok {
			return
		}
		orgID = org
	} else {
		_, org, ok := h.callerOrg(w, r)
		if !ok {
			return
		}
		orgID = org
	}
	templateID := q.Get("template_id")
	if templateID != "" && !requireID(w, "template_id", templateID) {
		return
	}
	out, err := h.uc.ListInstances(r.Context(), orgID, serverID, templateID)
	if err != nil {
		h.fail(w, err, "list instances")
		return
	}
	webutil.Success(w, "", out)
}
