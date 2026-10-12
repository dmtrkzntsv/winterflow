package contract_test

import (
	"net/http"
	"strings"
	"testing"

	"winterflow/internal/app/web/handler/alerts"
	"winterflow/internal/domain/alert"
)

// deadHook is a generic webhook on a closed local port: saving works, a
// send fails fast. Its path doubles as a secret the API must never echo.
const deadHook = "generic://127.0.0.1:9/hook-SECRETPATH?disabletls=yes"

// TestAlertChannelsNeverReturnURL: a channel's URL is stored encrypted and
// never comes back, not even inside a failed test's error.
func TestAlertChannelsNeverReturnURL(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	var ch alert.Channel
	owner.post(t, "/api/v1/alerts/save-channel", map[string]any{
		"name": "Ops", "url": deadHook, "fields": map[string]string{"ui_service": "webhook"},
		"is_default": true, "enabled": true,
	}).expect(t, http.StatusOK).data(t, &ch)
	if ch.ID == "" || ch.Service != "generic" || ch.Fields["ui_service"] != "webhook" || !ch.IsDefault {
		t.Fatalf("channel = %+v", ch)
	}
	list := owner.get(t, "/api/v1/alerts/get-channels", nil).expect(t, http.StatusOK)
	if strings.Contains(string(list.Body), "SECRETPATH") || strings.Contains(string(list.Body), "127.0.0.1") {
		t.Fatalf("URL leaked: %s", list.Body)
	}

	var res alerts.TestChannelResponse
	tested := owner.post(t, "/api/v1/alerts/test-channel", map[string]any{"id": ch.ID}).expect(t, http.StatusOK)
	tested.data(t, &res)
	if res.OK || res.Error == "" || strings.Contains(string(tested.Body), "SECRETPATH") {
		t.Fatalf("test result = %s", tested.Body)
	}
	var stored []alert.Channel
	owner.get(t, "/api/v1/alerts/get-channels", nil).expect(t, http.StatusOK).data(t, &stored)
	if len(stored) != 1 || stored[0].LastError == "" || strings.Contains(stored[0].LastError, "SECRETPATH") {
		t.Fatalf("last error = %+v", stored)
	}
	owner.post(t, "/api/v1/alerts/test-channel", map[string]any{"url": deadHook}).expect(t, http.StatusOK)
	owner.post(t, "/api/v1/alerts/test-channel", map[string]any{"url": "bogus://x"}).expect(t, http.StatusBadRequest)

	// An update without a url keeps the destination.
	var renamed alert.Channel
	owner.post(t, "/api/v1/alerts/save-channel", map[string]any{
		"id": ch.ID, "name": "Pager", "url": "", "fields": map[string]string{"ui_service": "ignored"},
		"is_default": false, "enabled": true,
	}).expect(t, http.StatusOK).data(t, &renamed)
	if renamed.Name != "Pager" || renamed.Service != "generic" || renamed.Fields["ui_service"] != "webhook" || renamed.IsDefault {
		t.Fatalf("renamed = %+v", renamed)
	}
	owner.post(t, "/api/v1/alerts/save-channel", map[string]any{"name": "No URL", "url": ""}).expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/alerts/delete-channel", map[string]any{"id": ch.ID}).expect(t, http.StatusOK)
	owner.post(t, "/api/v1/alerts/delete-channel", map[string]any{"id": ch.ID}).expect(t, http.StatusBadRequest)
}

// TestAlertRuleRejectsForeignChannel: rules and overrides may only route
// to the org's own channels; kind and metric are fixed after creation.
func TestAlertRuleRejectsForeignChannel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	var defaults []alert.Rule
	owner.get(t, "/api/v1/alerts/get-rules", nil).expect(t, http.StatusOK).data(t, &defaults)
	if len(defaults) != len(alert.DefaultRules()) {
		t.Fatalf("defaults = %+v", defaults)
	}
	unknown := "00000000-0000-0000-0000-0000000000c1"
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"target_kind": "server", "metric": "cpu", "threshold": 80, "duration_minutes": 5,
		"channel_ids": []string{unknown}, "enabled": true,
	}).expect(t, http.StatusBadRequest)

	var rule alert.Rule
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"target_kind": "app", "metric": "disk", "threshold": 50, "duration_minutes": 5,
		"channel_ids": []string{}, "enabled": true,
	}).expect(t, http.StatusOK).data(t, &rule)
	if rule.ID == "" || rule.ChannelIDs == nil || len(rule.ChannelIDs) != 0 {
		t.Fatalf("rule = %+v", rule)
	}
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"id": rule.ID, "target_kind": "app", "metric": "memory", "threshold": 50, "duration_minutes": 5, "enabled": true,
	}).expect(t, http.StatusBadRequest)
	var updated alert.Rule
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"id": rule.ID, "target_kind": "app", "metric": "disk", "threshold": 75, "duration_minutes": 5,
		"channel_ids": nil, "enabled": false,
	}).expect(t, http.StatusOK).data(t, &updated)
	if updated.Threshold != 75 || updated.Enabled || updated.ChannelIDs != nil {
		t.Fatalf("updated = %+v", updated)
	}
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"target_kind": "server", "metric": "cpu", "threshold": 120, "duration_minutes": 5,
	}).expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/alerts/delete-rule", map[string]any{"id": rule.ID}).expect(t, http.StatusOK)
	owner.post(t, "/api/v1/alerts/delete-rule", map[string]any{"id": rule.ID}).expect(t, http.StatusBadRequest)
}
