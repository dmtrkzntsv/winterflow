package alerts

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/alert"
)

// Routes is the alerts slice of the route table. Everything acts on the
// caller's org; target-addressed routes also check the server is the
// caller's (403). test-channel sends outbound traffic on demand, so it
// sits behind the per-IP limiter (429).
func (h *Handler) Routes(g api.Guards) []api.Route {
	bad := []int{http.StatusBadRequest}
	target := []int{http.StatusBadRequest, http.StatusForbidden}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-channels", Handler: h.GetChannels, Auth: api.User,
			Summary: "List the org's notification channels", Tag: "alerts",
			Description: "Channel URLs carry secrets and are never returned: `service` is the URL scheme and " +
				"`fields` the non-secret form values the client stored with it.",
			Response: []alert.Channel{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/save-channel", Handler: h.SaveChannel, Auth: api.User,
			Summary: "Create or update a notification channel", Tag: "alerts",
			Description: "`url` is a Shoutrrr URL (telegram://, discord://, ntfy://, smtp://, generic://, …), " +
				"stored encrypted. An empty `id` creates. On update an empty `url` keeps the stored destination " +
				"(url, service and fields); only name, default and enabled change.",
			Request: SaveChannelRequest{}, Response: alert.Channel{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/delete-channel", Handler: h.DeleteChannel, Auth: api.User,
			Summary: "Delete a notification channel", Tag: "alerts",
			Request: IDRequest{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/test-channel", Handler: h.TestChannel, Auth: api.User,
			Middleware: []api.Middleware{g.AuthLimit},
			Summary:    "Send a test notification", Tag: "alerts",
			Description: "Send exactly one of `id` (a saved channel; the outcome is recorded on it) or `url` " +
				"(a draft). Answers 200 with `ok` false and the error when the service refuses.",
			Request: TestChannelRequest{}, Response: TestChannelResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-rules", Handler: h.GetRules, Auth: api.User,
			Summary: "List the org's alert rules", Tag: "alerts",
			Response: []alert.Rule{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/save-rule", Handler: h.SaveRule, Auth: api.User,
			Summary: "Create or update an alert rule", Tag: "alerts",
			Description: "Fires when the metric's average over `duration_minutes` (1-60) is above `threshold` " +
				"(down rules: when the server or app is down that long). `channel_ids`: null = the org's default " +
				"channels, [] = none (History only). `target_kind` and `metric` cannot change on update.",
			Request: SaveRuleRequest{}, Response: alert.Rule{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/delete-rule", Handler: h.DeleteRule, Auth: api.User,
			Summary: "Delete an alert rule", Tag: "alerts",
			Request: IDRequest{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-overrides", Handler: h.GetOverrides, Auth: api.User,
			Summary: "A server's or app's rule overrides", Tag: "alerts",
			Query: TargetQuery{}, Response: []alert.Override{}, Errors: target,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/save-override", Handler: h.SaveOverride, Auth: api.User,
			Summary: "Override a rule for one server or app", Tag: "alerts",
			Description: "Null fields inherit the rule's; `channel_ids` null inherits, [] silences.",
			Request:     alert.Override{}, Response: alert.Override{}, Errors: target,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/alerts/delete-override", Handler: h.DeleteOverride, Auth: api.User,
			Summary: "Remove a rule override", Tag: "alerts",
			Request: DeleteOverrideRequest{}, Errors: target,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-events", Handler: h.GetEvents, Auth: api.User,
			Summary: "Alert history, newest first", Tag: "alerts",
			Query: EventsQuery{}, Response: EventsResponse{}, Errors: target,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/alerts/get-active", Handler: h.GetActive, Auth: api.User,
			Summary: "Firing alerts", Tag: "alerts",
			Query: ActiveQuery{}, Response: []ActiveAlert{}, Errors: target,
		},
	}
}
