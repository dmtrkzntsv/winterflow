package server

import (
	"encoding/json"
	"net/http"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// contentItemRequest is the browser's form of a file/variable. content is a
// plain string for non-secrets, or an ECIES base64 payload (or the
// "<encrypted>" sentinel) when encrypted is true.
type contentItemRequest struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	Encrypted bool   `json:"encrypted"`
}

type createAppRequest struct {
	ServerID  string               `json:"server_id"`
	App       model.App            `json:"app"`
	Config    json.RawMessage      `json:"config"`
	Files     []contentItemRequest `json:"files"`
	Variables []contentItemRequest `json:"variables"`
}

func toContentItems(items []contentItemRequest) []command.ContentItem {
	out := make([]command.ContentItem, 0, len(items))
	for _, it := range items {
		out = append(out, command.ContentItem{
			Name:      it.Name,
			Content:   []byte(it.Content),
			Encrypted: it.Encrypted,
		})
	}
	return out
}

// CreateApp accepts an app definition and dispatches an app.save command to the
// target agent (fire-and-forward). It returns 202 immediately with a
// request_id; the result is delivered to the caller over the SSE notification
// stream, correlated by that request_id.
func (h *Handler) CreateApp(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}

	req, ok := webutil.DecodeBody[createAppRequest](w, r)
	if !ok {
		return
	}
	if req.ServerID == "" {
		webutil.Error(w, "server_id is required", nil)
		return
	}

	// @todo check ownership of server_id by userID

	app := req.App
	app.ServerID = req.ServerID

	payload := command.AppPayload{
		Config:    req.Config,
		Files:     toContentItems(req.Files),
		Variables: toContentItems(req.Variables),
	}

	requestID, err := h.usecase.CreateApp(r.Context(), userID, req.ServerID, app, payload)
	if err != nil {
		webutil.Error(w, "failed to create app", nil)
		return
	}

	webutil.Accepted(w, "app creation dispatched", struct {
		RequestID string `json:"request_id"`
	}{RequestID: requestID})
}
