package templates

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
)

// CreateInstanceRequest is the body of POST create-instance.
type CreateInstanceRequest struct {
	ServerID   string                           `json:"server_id" required:"true"`
	Key        string                           `json:"key" required:"true" description:"The caller's id for the instance, unique in the org among instances not unlinked; a retried create with the same key and values makes no second app."`
	TemplateID string                           `json:"template_id" required:"true"`
	Revision   int64                            `json:"revision,omitempty" description:"Absent or 0: the latest."`
	Name       string                           `json:"name,omitempty" description:"The app's name; absent: the template's name."`
	Values     map[string]string                `json:"values,omitempty" description:"Plain values of the variables the compose file declares, by name; a value may not contain $. A variable left out keeps the compose file's own value."`
	Secrets    map[string]string                `json:"secrets,omitempty" description:"Values of the encrypted variables by name, ECIES-encrypted with the server's public key."`
	Hosts      []model.InstanceHost             `json:"hosts,omitempty" description:"Hostnames, each routed to an http expose entry (<service>:<port>) of the template; each must be covered by a domain registered on the server."`
	Backups    map[string]template.BackupChoice `json:"backups,omitempty" description:"Settings of the backups the compose file declares, by name; a backup left out starts off."`
	Wait       bool                             `json:"wait,omitempty" description:"Hold the request for the outcome (at most 10 minutes)."`
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

func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[CreateInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.instanceCaller(w, r, req.ServerID, req.Key)
	if !ok {
		return
	}
	if !requireID(w, "template_id", req.TemplateID) {
		return
	}
	out, err := h.uc.CreateInstance(r.Context(), userID, orgID, tuc.CreateInput{
		ServerID: req.ServerID, Key: req.Key, TemplateID: req.TemplateID, Revision: req.Revision,
		Name: req.Name, Values: req.Values, Secrets: req.Secrets, Hosts: req.Hosts, Backups: req.Backups,
	})
	if err != nil {
		h.fail(w, err, "create the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "instance save dispatched")
}

func (h *Handler) GetInstance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	_, orgID, ok := h.instanceCaller(w, r, q.Get("server_id"), q.Get("key"))
	if !ok {
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
	var (
		orgID string
		ok    bool
	)
	if serverID != "" {
		_, orgID, ok = h.serverCaller(w, r, serverID)
	} else {
		_, orgID, ok = h.callerOrg(w, r)
	}
	if !ok {
		return
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
