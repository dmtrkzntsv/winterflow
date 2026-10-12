package alerts

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
)

// TargetQuery addresses a server (app_id empty) or one of its apps.
type TargetQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Target server id."`
	AppID    string `query:"app_id" description:"Target app id; empty for the server itself."`
}

// DeleteOverrideRequest is the body of POST /api/v1/alerts/delete-override.
type DeleteOverrideRequest struct {
	RuleID   string `json:"rule_id"`
	ServerID string `json:"server_id"`
	AppID    string `json:"app_id"`
}

// GetOverrides serves GET /api/v1/alerts/get-overrides: one target's
// overrides, by rule.
func (h *Handler) GetOverrides(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	t := alert.Target{ServerID: q.Get("server_id"), AppID: q.Get("app_id")}
	if !h.requireTarget(w, r, userID, orgID, t) {
		return
	}
	list, err := h.store.TargetOverrides(r.Context(), t)
	if err != nil {
		h.fail(w, "load overrides", err)
		return
	}
	webutil.Success(w, "ok", list)
}

// SaveOverride serves POST /api/v1/alerts/save-override: create or replace
// the override of one rule for one target.
func (h *Handler) SaveOverride(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	o, ok := webutil.DecodeBody[alert.Override](w, r)
	if !ok {
		return
	}
	if !h.requireTarget(w, r, userID, orgID, alert.Target{ServerID: o.ServerID, AppID: o.AppID}) {
		return
	}
	rule, err := h.store.GetRule(r.Context(), orgID, o.RuleID)
	if err != nil {
		h.fail(w, "load rule", err)
		return
	}
	if err := o.Validate(rule); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	if !h.requireChannels(w, r, orgID, o.ChannelIDs) {
		return
	}
	if err := h.store.SaveOverride(r.Context(), o); err != nil {
		h.fail(w, "save override", err)
		return
	}
	webutil.Success(w, "ok", o)
}

// DeleteOverride serves POST /api/v1/alerts/delete-override: the target
// goes back to the rule's settings.
func (h *Handler) DeleteOverride(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[DeleteOverrideRequest](w, r)
	if !ok {
		return
	}
	t := alert.Target{ServerID: req.ServerID, AppID: req.AppID}
	if !h.requireTarget(w, r, userID, orgID, t) {
		return
	}
	if req.RuleID == "" {
		webutil.Error(w, "rule_id is required", nil)
		return
	}
	if err := h.store.DeleteOverride(r.Context(), alert.Key{RuleID: req.RuleID, Target: t}); err != nil {
		h.fail(w, "delete override", err)
		return
	}
	webutil.Success(w, "ok", nil)
}
