package templates

import (
	"encoding/json"
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/port"
	tuc "winterflow/internal/domain/usecase/templates"
)

// InstanceResult is the data of an instance call that waited for its
// outcome (or had nothing to wait for).
type InstanceResult struct {
	Instance tuc.InstanceView `json:"instance"`
	// RequestID names the command that ran ("" when none was needed).
	RequestID string `json:"request_id"`
	// Warnings are the agent's non-fatal warnings (backup settings
	// dropped, an ingress problem).
	Warnings []string `json:"warnings"`
}

// acceptedData is the 202 body.
type acceptedData struct {
	RequestID string `json:"request_id"`
}

// respond answers an instance call: 200 with the instance when nothing was
// dispatched; without wait 202; with wait the outcome (200 with the
// instance re-read after the result was recorded, 502 with the agent's
// error), or 202 once the bound passes.
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, out tuc.Outcome, wait bool, msg string) {
	if out.Instance != nil {
		webutil.Success(w, msg, InstanceResult{Instance: *out.Instance, Warnings: []string{}})
		return
	}
	if !wait {
		webutil.Accepted(w, msg, acceptedData{RequestID: out.RequestID})
		return
	}
	res, ok := port.AwaitResult(r.Context(), out.Done, h.waitBound)
	switch {
	case !ok && r.Context().Err() != nil:
		return // the client is gone: there is no one to answer
	case !ok:
		webutil.Accepted(w, msg, acceptedData{RequestID: out.RequestID})
		return
	case !res.Success:
		webutil.ErrorStatus(w, http.StatusBadGateway, res.Err().Error())
		return
	}
	view, err := out.Final(r.Context())
	if err != nil {
		h.fail(w, err, "read the instance")
		return
	}
	var agent struct {
		Warnings []string `json:"warnings"`
	}
	_ = json.Unmarshal(res.Payload, &agent)
	if agent.Warnings == nil {
		agent.Warnings = []string{}
	}
	webutil.Success(w, msg, InstanceResult{Instance: view, RequestID: out.RequestID, Warnings: agent.Warnings})
}
