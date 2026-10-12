package alerts

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
)

// SaveRuleRequest is the body of POST /api/v1/alerts/save-rule. An empty
// id creates; on update target_kind and metric must match the stored
// rule. channel_ids: null = the org's default channels, [] = none
// (History only), else those channels.
type SaveRuleRequest struct {
	ID              string           `json:"id"`
	TargetKind      alert.TargetKind `json:"target_kind" enum:"server,app"`
	Metric          alert.Metric     `json:"metric" enum:"down,cpu,memory,disk,load1,load5,load15,bandwidth,temperature"`
	Threshold       float64          `json:"threshold"`
	DurationMinutes int              `json:"duration_minutes"`
	ChannelIDs      []string         `json:"channel_ids"`
	Enabled         bool             `json:"enabled"`
}

// GetRules serves GET /api/v1/alerts/get-rules. It seeds the default
// rules of an org that has not had them yet first, so a new org sees them
// at once.
func (h *Handler) GetRules(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	if _, err := h.store.SeedDefaults(r.Context()); err != nil {
		h.log.Warn("alerts: seed default rules", "error", err)
	}
	rules, err := h.store.ListRules(r.Context(), orgID)
	if err != nil {
		h.fail(w, "load rules", err)
		return
	}
	webutil.Success(w, "ok", rules)
}

// SaveRule serves POST /api/v1/alerts/save-rule.
func (h *Handler) SaveRule(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[SaveRuleRequest](w, r)
	if !ok {
		return
	}
	rule := alert.Rule{
		ID: req.ID, OrgID: orgID, TargetKind: req.TargetKind, Metric: req.Metric, Threshold: req.Threshold,
		DurationMinutes: req.DurationMinutes, ChannelIDs: req.ChannelIDs, Enabled: req.Enabled,
	}
	rule.Normalize()
	if err := rule.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	if !h.requireChannels(w, r, orgID, rule.ChannelIDs) {
		return
	}
	if rule.ID == "" {
		created, err := h.store.CreateRule(r.Context(), rule)
		if err != nil {
			h.fail(w, "save rule", err)
			return
		}
		webutil.Success(w, "ok", created)
		return
	}
	stored, err := h.store.GetRule(r.Context(), orgID, rule.ID)
	if err != nil {
		h.fail(w, "load rule", err)
		return
	}
	if stored.TargetKind != rule.TargetKind || stored.Metric != rule.Metric {
		webutil.Error(w, "a rule's target kind and metric cannot change; create a new rule", nil)
		return
	}
	if err := h.store.UpdateRule(r.Context(), rule); err != nil {
		h.fail(w, "save rule", err)
		return
	}
	rule.CreatedAt = stored.CreatedAt
	webutil.Success(w, "ok", rule)
}

// DeleteRule serves POST /api/v1/alerts/delete-rule. Its overrides and
// state go with it; its history stays.
func (h *Handler) DeleteRule(w http.ResponseWriter, r *http.Request) {
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
	if err := h.store.DeleteRule(r.Context(), orgID, req.ID); err != nil {
		h.fail(w, "delete rule", err)
		return
	}
	webutil.Success(w, "ok", nil)
}
