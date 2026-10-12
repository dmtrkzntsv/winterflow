package alerts

import (
	"net/http"
	"strings"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
	"winterflow/internal/infra/notify"
)

// Limits of a channel's name and of its non-secret form fields.
const (
	maxNameLen    = 64
	maxFields     = 16
	maxFieldKey   = 32
	maxFieldValue = 256
)

// SaveChannelRequest is the body of POST /api/v1/alerts/save-channel. An
// empty id creates. On update an empty url keeps the stored destination
// (url, service and fields); only name, default and enabled change.
type SaveChannelRequest struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Fields    map[string]string `json:"fields"`
	IsDefault bool              `json:"is_default"`
	Enabled   bool              `json:"enabled"`
}

// TestChannelRequest is the body of POST /api/v1/alerts/test-channel:
// exactly one of a saved channel id or a draft url.
type TestChannelRequest struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// TestChannelResponse is the outcome of a test send (200 either way).
type TestChannelResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

const (
	testTitle = "🔔 Winterflow test"
	testBody  = "This is a test notification from Winterflow. If you can read it, the channel works."
)

// GetChannels serves GET /api/v1/alerts/get-channels.
func (h *Handler) GetChannels(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	chans, err := h.store.ListChannels(r.Context(), orgID)
	if err != nil {
		h.fail(w, "load channels", err)
		return
	}
	webutil.Success(w, "ok", chans)
}

// SaveChannel serves POST /api/v1/alerts/save-channel.
func (h *Handler) SaveChannel(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[SaveChannelRequest](w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxNameLen {
		webutil.Error(w, "name is required (max 64 chars)", nil)
		return
	}
	if msg := checkFields(req.Fields); msg != "" {
		webutil.Error(w, msg, nil)
		return
	}
	if h.box == nil {
		webutil.Error(w, errNoBox, nil)
		return
	}
	ch := alert.Channel{OrgID: orgID, Name: name, IsDefault: req.IsDefault, Enabled: req.Enabled}
	if req.ID != "" {
		stored, err := h.store.GetChannel(r.Context(), orgID, req.ID)
		if err != nil {
			h.fail(w, "load channel", err)
			return
		}
		ch.ID, ch.CreatedAt, ch.LastSentAt = stored.ID, stored.CreatedAt, stored.LastSentAt
		ch.Service, ch.Fields, ch.URLEncrypted, ch.LastError = stored.Service, stored.Fields, stored.URLEncrypted, stored.LastError
	} else if req.URL == "" {
		webutil.Error(w, "url is required", nil)
		return
	}
	if req.URL != "" {
		service, err := notify.Service(req.URL)
		if err != nil {
			webutil.Error(w, err.Error(), nil)
			return
		}
		sealed, err := h.box.Seal(req.URL)
		if err != nil {
			h.fail(w, "encrypt channel url", err)
			return
		}
		ch.Service, ch.URLEncrypted, ch.Fields, ch.LastError = service, sealed, req.Fields, ""
	}
	if ch.Fields == nil {
		ch.Fields = map[string]string{}
	}
	if ch.ID == "" {
		created, err := h.store.CreateChannel(r.Context(), ch)
		if err != nil {
			h.fail(w, "save channel", err)
			return
		}
		webutil.Success(w, "ok", created)
		return
	}
	if err := h.store.UpdateChannel(r.Context(), ch); err != nil {
		h.fail(w, "save channel", err)
		return
	}
	webutil.Success(w, "ok", ch)
}

// DeleteChannel serves POST /api/v1/alerts/delete-channel.
func (h *Handler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[IDRequest](w, r)
	if !ok {
		return
	}
	if req.ID == "" {
		webutil.Error(w, "id is required", nil)
		return
	}
	if err := h.store.DeleteChannel(r.Context(), orgID, req.ID); err != nil {
		h.fail(w, "delete channel", err)
		return
	}
	webutil.Success(w, "ok", nil)
}

// TestChannel serves POST /api/v1/alerts/test-channel: sends a test
// notification now (10 s deadline) and reports the outcome.
func (h *Handler) TestChannel(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[TestChannelRequest](w, r)
	if !ok {
		return
	}
	if (req.ID == "") == (req.URL == "") {
		webutil.Error(w, "send exactly one of id or url", nil)
		return
	}
	var errText string
	if req.URL != "" {
		if _, err := notify.Service(req.URL); err != nil {
			webutil.Error(w, err.Error(), nil)
			return
		}
		errText = h.tester.SendURL(r.Context(), req.URL, testTitle, testBody)
	} else {
		if h.box == nil {
			webutil.Error(w, errNoBox, nil)
			return
		}
		ch, err := h.store.GetChannel(r.Context(), orgID, req.ID)
		if err != nil {
			h.fail(w, "load channel", err)
			return
		}
		errText = h.tester.SendTo(r.Context(), ch, testTitle, testBody)
	}
	webutil.Success(w, "ok", TestChannelResponse{OK: errText == "", Error: errText})
}

// checkFields bounds the non-secret form fields; "" when they are fine.
func checkFields(f map[string]string) string {
	if len(f) > maxFields {
		return "too many fields (max 16)"
	}
	for k, v := range f {
		if k == "" || len(k) > maxFieldKey || len(v) > maxFieldValue {
			return "field names are 1-32 chars and values at most 256"
		}
	}
	return ""
}
