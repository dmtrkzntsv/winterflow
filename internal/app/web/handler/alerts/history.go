package alerts

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
)

const (
	defaultEventsPage = 50
	maxEventsPage     = 200
)

// EventsQuery filters and pages the history.
type EventsQuery struct {
	ServerID string `query:"server_id" description:"Only this server's events (and its apps')."`
	AppID    string `query:"app_id" description:"Only this app's events; needs server_id."`
	RuleID   string `query:"rule_id" description:"Only this rule's events."`
	Kind     string `query:"kind" enum:"fired,resolved" description:"Only fired or only resolved events."`
	Cursor   string `query:"cursor" description:"next_cursor of the previous page."`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" description:"Page size (default 50)."`
}

// EventsResponse is one history page, newest first. next_cursor is empty
// on the last page.
type EventsResponse struct {
	Events     []alert.Event `json:"events"`
	NextCursor string        `json:"next_cursor"`
}

// ActiveQuery narrows the firing alerts to a server or one of its apps.
type ActiveQuery struct {
	ServerID string `query:"server_id" description:"Only this server's alerts (and its apps')."`
	AppID    string `query:"app_id" description:"Only this app's alerts; needs server_id."`
}

// ActiveAlert is a firing alert with its target's names.
type ActiveAlert struct {
	alert.Active
	ServerName string `json:"server_name"`
	AppName    string `json:"app_name"`
}

// scope reads the optional server_id/app_id filter and authorizes it.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request, userID, orgID string) (alert.Target, bool) {
	q := r.URL.Query()
	t := alert.Target{ServerID: q.Get("server_id"), AppID: q.Get("app_id")}
	if t.ServerID == "" {
		if t.AppID != "" {
			webutil.Error(w, "app_id needs server_id", nil)
			return t, false
		}
		return t, true
	}
	return t, h.requireTarget(w, r, userID, orgID, t)
}

// GetEvents serves GET /api/v1/alerts/get-events.
func (h *Handler) GetEvents(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	t, ok := h.scope(w, r, userID, orgID)
	if !ok {
		return
	}
	q := r.URL.Query()
	query := alert.EventQuery{OrgID: orgID, ServerID: t.ServerID, AppID: t.AppID, RuleID: q.Get("rule_id"), Limit: defaultEventsPage}
	switch kind := alert.EventKind(q.Get("kind")); kind {
	case "", alert.EventFired, alert.EventResolved:
		query.Kind = kind
	default:
		webutil.Error(w, "kind must be fired or resolved", nil)
		return
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxEventsPage {
			webutil.Error(w, "limit must be 1 to 200", nil)
			return
		}
		query.Limit = n
	}
	if c := q.Get("cursor"); c != "" {
		at, id, err := parseCursor(c)
		if err != nil {
			webutil.Error(w, "invalid cursor", nil)
			return
		}
		query.BeforeAt, query.BeforeID = at, id
	}
	events, err := h.store.ListEvents(r.Context(), query)
	if err != nil {
		h.fail(w, "load history", err)
		return
	}
	resp := EventsResponse{Events: events}
	if n := len(events); n == query.Limit {
		resp.NextCursor = formatCursor(events[n-1])
	}
	webutil.Success(w, "ok", resp)
}

// GetActive serves GET /api/v1/alerts/get-active: the org's firing
// alerts, newest first, optionally for one server or app.
func (h *Handler) GetActive(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	t, ok := h.scope(w, r, userID, orgID)
	if !ok {
		return
	}
	active, err := h.store.ListActive(r.Context(), orgID)
	if err != nil {
		h.fail(w, "load firing alerts", err)
		return
	}
	dir, err := h.store.Directory(r.Context())
	if err != nil {
		h.fail(w, "load names", err)
		return
	}
	out := []ActiveAlert{}
	for _, a := range active {
		if (t.ServerID != "" && a.ServerID != t.ServerID) || (t.AppID != "" && a.AppID != t.AppID) {
			continue
		}
		item := ActiveAlert{Active: a, ServerName: dir.Servers[a.ServerID].Name}
		if a.AppID != "" {
			item.AppName = dir.Apps[a.AppID].Name
		}
		out = append(out, item)
	}
	webutil.Success(w, "ok", out)
}

// A cursor is "<unix seconds>_<event id>" of the last event of a page.
func formatCursor(ev alert.Event) string {
	return fmt.Sprintf("%d_%s", ev.CreatedAt.Unix(), ev.ID)
}

func parseCursor(s string) (time.Time, string, error) {
	secs, id, found := strings.Cut(s, "_")
	n, err := strconv.ParseInt(secs, 10, 64)
	if !found || err != nil || id == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor %q", s)
	}
	return time.Unix(n, 0).UTC(), id, nil
}
