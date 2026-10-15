package templates

import (
	"net/http"
	"strconv"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
)

// maxSaveBytes caps a save-template request: the body's own cap plus room
// for the envelope fields.
const maxSaveBytes = template.MaxBodyBytes + 64<<10

// TemplateQuery is the query of GET get-template.
type TemplateQuery struct {
	ID       string `query:"id" required:"true"`
	Revision int64  `query:"revision" description:"Revision to read; absent or 0 is the latest."`
}

// SaveTemplateRequest is the body of POST save-template.
type SaveTemplateRequest struct {
	ID           string        `json:"id,omitempty" description:"Absent to create the template."`
	Name         string        `json:"name" required:"true" description:"^[a-z][a-z0-9-]{0,62}$, unique in the org."`
	Body         template.Body `json:"body" required:"true"`
	Note         string        `json:"note,omitempty"`
	BaseRevision int64         `json:"base_revision" description:"The latest revision the editor read (0 for a new template); another one is a 409."`
}

// InspectTemplateRequest is the body of POST inspect-template.
type InspectTemplateRequest struct {
	Body template.Body `json:"body" required:"true"`
}

// TemplateIDRequest is the body of POST delete-template.
type TemplateIDRequest struct {
	ID string `json:"id" required:"true"`
}

// UpgradeInstancesRequest is the body of POST upgrade-instances.
type UpgradeInstancesRequest struct {
	ID       string `json:"id" required:"true"`
	Revision int64  `json:"revision" description:"The revision to move the instances to; absent or 0 is the latest."`
}

func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	out, err := h.uc.ListTemplates(r.Context(), orgID)
	if err != nil {
		h.fail(w, err, "list templates")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if !requireID(w, "id", id) {
		return
	}
	var revision int64
	if s := q.Get("revision"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			webutil.Error(w, "revision must be a revision number", nil)
			return
		}
		revision = n
	}
	out, err := h.uc.GetTemplate(r.Context(), orgID, id, revision)
	if err != nil {
		h.fail(w, err, "read the template")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) SaveTemplate(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSaveBytes)
	req, ok := webutil.DecodeBody[SaveTemplateRequest](w, r)
	if !ok || (req.ID != "" && !requireID(w, "id", req.ID)) {
		return
	}
	out, err := h.uc.SaveTemplate(r.Context(), userID, orgID, tuc.SaveInput{
		ID: req.ID, Name: req.Name, Body: req.Body, Note: req.Note, BaseRevision: req.BaseRevision,
	})
	if err != nil {
		h.fail(w, err, "save the template")
		return
	}
	webutil.Success(w, "template saved", out)
}

func (h *Handler) InspectTemplate(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSaveBytes)
	req, ok := webutil.DecodeBody[InspectTemplateRequest](w, r)
	if !ok {
		return
	}
	out, err := h.uc.InspectTemplate(r.Context(), orgID, req.Body)
	if err != nil {
		h.fail(w, err, "inspect the template")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[TemplateIDRequest](w, r)
	if !ok {
		return
	}
	if !requireID(w, "id", req.ID) {
		return
	}
	if err := h.uc.DeleteTemplate(r.Context(), orgID, req.ID); err != nil {
		h.fail(w, err, "delete the template")
		return
	}
	webutil.Success(w, "template deleted", nil)
}

func (h *Handler) UpgradeInstances(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[UpgradeInstancesRequest](w, r)
	if !ok || !requireID(w, "id", req.ID) {
		return
	}
	if req.Revision < 0 {
		webutil.Error(w, "revision must be a revision number", nil)
		return
	}
	out, err := h.rollouts.QueueUpgrade(r.Context(), userID, orgID, req.ID, req.Revision)
	if err != nil {
		h.fail(w, err, "queue the upgrade")
		return
	}
	webutil.Success(w, "upgrade queued", out)
}
