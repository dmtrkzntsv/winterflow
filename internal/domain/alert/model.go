package alert

import (
	"errors"
	"time"
)

// ErrNotFound is returned for a channel, rule or server that does not exist
// in the caller's org.
var ErrNotFound = errors.New("alert: not found")

// State is a (rule, target)'s alert state.
type State string

const (
	StateOK     State = "ok"
	StateFiring State = "firing"
)

// EventKind is a transition recorded in the history.
type EventKind string

const (
	EventFired    EventKind = "fired"
	EventResolved EventKind = "resolved"
)

// Delivery is one channel's outcome for an event.
type Delivery struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
}

// Event is one history row. Names are captured at event time.
type Event struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"-"`
	RuleID     string     `json:"rule_id"`
	ServerID   string     `json:"server_id"`
	AppID      string     `json:"app_id"`
	ServerName string     `json:"server_name"`
	AppName    string     `json:"app_name"`
	TargetKind TargetKind `json:"target_kind"`
	Metric     Metric     `json:"metric"`
	Kind       EventKind  `json:"kind"`
	Value      float64    `json:"value"`
	Threshold  float64    `json:"threshold"`
	Title      string     `json:"title"`
	Message    string     `json:"message"`
	Deliveries []Delivery `json:"deliveries"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Channel is an org's notification channel. The Shoutrrr URL is stored
// encrypted (URLEncrypted) and never leaves the API; Service is its scheme
// and Fields are the non-secret form values the browser chose to keep.
type Channel struct {
	ID           string            `json:"id"`
	OrgID        string            `json:"-"`
	Name         string            `json:"name"`
	Service      string            `json:"service"`
	Fields       map[string]string `json:"fields"`
	IsDefault    bool              `json:"is_default"`
	Enabled      bool              `json:"enabled"`
	LastError    string            `json:"last_error"`
	LastSentAt   *time.Time        `json:"last_sent_at"`
	CreatedAt    time.Time         `json:"created_at"`
	URLEncrypted string            `json:"-"`
}

// RouteChannels picks the enabled channels a notification goes to: the
// defaults when ids is nil, none when it is empty, else the listed ones
// (unknown ids are ignored). Order follows all.
func RouteChannels(all []Channel, ids []string) []Channel {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []Channel
	for _, c := range all {
		if !c.Enabled {
			continue
		}
		if (ids == nil && c.IsDefault) || want[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// StateRow is a stored state with its rule's kind and metric.
type StateRow struct {
	Key
	TargetKind TargetKind
	Metric     Metric
	State      State
	Since      time.Time
	Value      float64
	Threshold  float64
}

// Active is a firing alert for the badges, strip and cards.
type Active struct {
	RuleID     string     `json:"rule_id"`
	TargetKind TargetKind `json:"target_kind"`
	Metric     Metric     `json:"metric"`
	ServerID   string     `json:"server_id"`
	AppID      string     `json:"app_id"`
	Since      time.Time  `json:"since"`
	Value      float64    `json:"value"`
	Threshold  float64    `json:"threshold"`
}

// Notice is the payload of the SSE "alert" notification.
type Notice struct {
	Kind       EventKind  `json:"kind"`
	RuleID     string     `json:"rule_id"`
	TargetKind TargetKind `json:"target_kind"`
	Metric     Metric     `json:"metric"`
	ServerID   string     `json:"server_id"`
	AppID      string     `json:"app_id"`
	Value      float64    `json:"value"`
	Threshold  float64    `json:"threshold"`
	Title      string     `json:"title"`
}

// EventQuery filters the history (empty fields match all) and pages it
// newest first: rows strictly before (BeforeAt, BeforeID).
type EventQuery struct {
	OrgID    string
	ServerID string
	AppID    string
	RuleID   string
	Kind     EventKind
	BeforeAt time.Time
	BeforeID string
	Limit    int
}

// ServerRef and AppRef are the evaluator's directory of targets.
type ServerRef struct{ ID, OrgID, Name string }

type AppRef struct{ ID, ServerID, Name string }

// Directory is every server and app the API knows, keyed by id.
type Directory struct {
	Servers map[string]ServerRef
	Apps    map[string]AppRef
}
