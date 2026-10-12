package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/alerts"
	"winterflow/internal/app/web/handler/monitoring"
	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
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

// TestAlertOverrides: per-target overrides for the server and one app.
func TestAlertOverrides(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	appID := "00000000-0000-0000-0000-0000000000a1"
	if err := e.deps.AppRepository.SaveApp(e.dbCtx(t), model.App{ID: appID, ServerID: sid, Name: "nextcloud"}); err != nil {
		t.Fatal(err)
	}
	var rules []alert.Rule
	owner.get(t, "/api/v1/alerts/get-rules", nil).expect(t, http.StatusOK).data(t, &rules)
	var cpu, appDown alert.Rule
	for _, r := range rules {
		switch {
		case r.TargetKind == alert.TargetServer && r.Metric == alert.MetricCPU:
			cpu = r
		case r.TargetKind == alert.TargetApp && r.Metric == alert.MetricDown:
			appDown = r
		}
	}

	var saved alert.Override
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{
		"rule_id": cpu.ID, "server_id": sid, "app_id": "", "disabled": false, "threshold": 95,
		"duration_minutes": nil, "channel_ids": nil,
	}).expect(t, http.StatusOK).data(t, &saved)
	if saved.Threshold == nil || *saved.Threshold != 95 {
		t.Fatalf("saved = %+v", saved)
	}
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{
		"rule_id": appDown.ID, "server_id": sid, "app_id": appID, "disabled": true,
	}).expect(t, http.StatusOK)
	// The app rule needs an app target; the down rule takes no threshold;
	// an app of no server is unknown.
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{"rule_id": appDown.ID, "server_id": sid, "app_id": ""}).
		expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{"rule_id": appDown.ID, "server_id": sid, "app_id": appID, "threshold": 3}).
		expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/alerts/save-override", map[string]any{"rule_id": appDown.ID, "server_id": sid, "app_id": anApp}).
		expect(t, http.StatusBadRequest)

	var list []alert.Override
	owner.get(t, "/api/v1/alerts/get-overrides", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &list)
	if len(list) != 1 || list[0].RuleID != cpu.ID {
		t.Fatalf("server overrides = %+v", list)
	}
	owner.get(t, "/api/v1/alerts/get-overrides", url.Values{"server_id": {sid}, "app_id": {appID}}).
		expect(t, http.StatusOK).data(t, &list)
	if len(list) != 1 || !list[0].Disabled {
		t.Fatalf("app overrides = %+v", list)
	}
	owner.post(t, "/api/v1/alerts/delete-override", map[string]any{"rule_id": cpu.ID, "server_id": sid, "app_id": ""}).
		expect(t, http.StatusOK)
	owner.get(t, "/api/v1/alerts/get-overrides", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &list)
	if len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
}

// TestAlertFiresFromMetrics: a sample above a 1-minute rule fires; the
// alert shows in the history, the firing list and the overview badge.
func TestAlertFiresFromMetrics(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	var rule alert.Rule
	owner.post(t, "/api/v1/alerts/save-rule", map[string]any{
		"target_kind": "server", "metric": "cpu", "threshold": 50, "duration_minutes": 1,
		"channel_ids": []string{}, "enabled": true,
	}).expect(t, http.StatusOK).data(t, &rule)

	cpu := 99.0
	body, err := json.Marshal(metrics.Sample{TS: time.Now().Unix(), Server: metrics.ServerSample{CPU: &cpu}})
	if err != nil {
		t.Fatal(err)
	}
	e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventServerMetrics, Payload: body})

	var active []alerts.ActiveAlert
	eventually(t, func() bool {
		owner.get(t, "/api/v1/alerts/get-active", nil).expect(t, http.StatusOK).data(t, &active)
		return len(active) == 1
	})
	if a := active[0]; a.RuleID != rule.ID || a.Value != 99 || a.Threshold != 50 || a.ServerName == "" {
		t.Fatalf("active = %+v", a)
	}
	owner.get(t, "/api/v1/alerts/get-active", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &active)
	if len(active) != 1 {
		t.Fatalf("server filter = %+v", active)
	}
	owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/alerts/get-active", Query: url.Values{"app_id": {anApp}}, Invalid: true}).
		expect(t, http.StatusBadRequest)

	var page alerts.EventsResponse
	owner.get(t, "/api/v1/alerts/get-events", url.Values{"server_id": {sid}, "limit": {"1"}}).
		expect(t, http.StatusOK).data(t, &page)
	if len(page.Events) != 1 || page.Events[0].Kind != alert.EventFired || !strings.HasPrefix(page.Events[0].Title, "🔥") ||
		page.NextCursor == "" || page.Events[0].Deliveries == nil {
		t.Fatalf("history = %+v", page)
	}
	owner.get(t, "/api/v1/alerts/get-events", url.Values{"cursor": {page.NextCursor}}).expect(t, http.StatusOK).data(t, &page)
	if len(page.Events) != 0 || page.NextCursor != "" {
		t.Fatalf("second page = %+v", page)
	}
	owner.get(t, "/api/v1/alerts/get-events", url.Values{"kind": {"resolved"}}).expect(t, http.StatusOK).data(t, &page)
	if len(page.Events) != 0 {
		t.Fatalf("resolved filter = %+v", page)
	}
	owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/alerts/get-events", Query: url.Values{"cursor": {"nope"}}, Invalid: true}).
		expect(t, http.StatusBadRequest)

	var ov monitoring.OverviewResponse
	owner.get(t, "/api/v1/metrics/overview", nil).expect(t, http.StatusOK).data(t, &ov)
	if len(ov.Servers) != 1 || ov.Servers[0].AlertsFiring != 1 {
		t.Fatalf("overview = %+v", ov)
	}
}
