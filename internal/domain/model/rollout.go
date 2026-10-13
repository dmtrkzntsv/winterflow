package model

import (
	"slices"
	"time"
)

// Rollout kinds: upgrade a template's instances, reload the apps a
// shared-variable change made stale.
const (
	RolloutTemplateUpgrade = "template_upgrade"
	RolloutVarsReload      = "vars_reload"
)

// RolloutState is where a rollout is in its life.
type RolloutState string

const (
	RolloutQueued  RolloutState = "queued"
	RolloutRunning RolloutState = "running"
	RolloutDone    RolloutState = "done"
	RolloutStopped RolloutState = "stopped"
	RolloutFailed  RolloutState = "failed"
)

// Active reports whether the rollout may still take steps.
func (s RolloutState) Active() bool { return s == RolloutQueued || s == RolloutRunning }

// Rollout errors.
var (
	ErrRolloutNotFound = &classified{"rollout not found", ErrNotFound}
	ErrRolloutActive   = &classified{"an upgrade of this template is already running: wait for it or stop it", ErrConflict}
	ErrRolloutStopping = &classified{"a reload is stopping; reload again once it has stopped", ErrConflict}
)

// Rollout is one queued, resumable, one-app-at-a-time job.
type Rollout struct {
	ID    string
	OrgID string
	Kind  string
	// Scope is the kind's JSON scope (UpgradeScope, ReloadScope); ScopeRev
	// counts its changes (an extension bumps it).
	Scope    []byte
	ScopeKey string
	ScopeRev int64
	State    RolloutState
	// StopRequested ends the rollout (stopped) after the step in flight.
	StopRequested bool
	// CurrentAppID and CurrentServerID name the app in flight; after a
	// failure, the app that failed.
	CurrentAppID    string
	CurrentServerID string
	Done            int64
	Skipped         int64
	// Total is Done + Skipped + the apps still to do, as of the last step.
	Total int64
	// SkipIDs are what this rollout skipped (instance ids for an upgrade,
	// app ids for a reload): never picked again.
	SkipIDs    []string
	Error      string
	LeaseOwner string
	LeaseUntil *time.Time
	CreatedAt  time.Time
	CreatedBy  string
	FinishedAt *time.Time
}

// UpgradeScope is a template_upgrade rollout's scope: the instances of
// TemplateID below Revision move to it.
type UpgradeScope struct {
	TemplateID string `json:"template_id"`
	Revision   int64  `json:"revision"`
}

// ReloadScope is a vars_reload rollout's scope: the running apps to reload
// are the stale ones (stale_vars in the API's status cache) that
//   - are on a server of ServerIDs and stale for one of Names (a variable
//     saved or deleted with reload, a reload by name or by variable), or
//   - are on a server of AllStale (a reload of a server's stale apps), or
//   - are one of AppIDs (an app's own Reload), whatever the name.
//
// Revisions holds, per server, the shared-variables revision its status
// report must have reached before its stale_vars count (the change is
// pushed with vars.sync first); a server that has not reached it by
// SettleBy is left out.
type ReloadScope struct {
	Names     []string         `json:"names"`
	ServerIDs []string         `json:"server_ids"`
	AllStale  []string         `json:"all_stale,omitempty"`
	AppIDs    []string         `json:"app_ids,omitempty"`
	Revisions map[string]int64 `json:"revisions,omitempty"`
	SettleBy  time.Time        `json:"settle_by"`
}

// Merge is the union of two scopes: lists joined (sorted, no duplicates),
// the higher revision per server, the later SettleBy.
func (s ReloadScope) Merge(o ReloadScope) ReloadScope {
	out := ReloadScope{
		Names: joined(s.Names, o.Names), ServerIDs: joined(s.ServerIDs, o.ServerIDs),
		AllStale: joined(s.AllStale, o.AllStale), AppIDs: joined(s.AppIDs, o.AppIDs),
		Revisions: map[string]int64{}, SettleBy: s.SettleBy,
	}
	for _, m := range []map[string]int64{s.Revisions, o.Revisions} {
		for id, rev := range m {
			if cur, ok := out.Revisions[id]; !ok || rev > cur {
				out.Revisions[id] = rev
			}
		}
	}
	if o.SettleBy.After(out.SettleBy) {
		out.SettleBy = o.SettleBy
	}
	return out
}

// Servers are the servers the scope reaches, sorted.
func (s ReloadScope) Servers() []string { return joined(s.ServerIDs, s.AllStale) }

// Wants reports whether a running app on serverID that is stale for the
// names in stale is to be reloaded.
func (s ReloadScope) Wants(serverID, appID string, stale []string) bool {
	switch {
	case len(stale) == 0:
		return false
	case slices.Contains(s.AllStale, serverID), slices.Contains(s.AppIDs, appID):
		return true
	case !slices.Contains(s.ServerIDs, serverID):
		return false
	}
	for _, n := range stale {
		if slices.Contains(s.Names, n) {
			return true
		}
	}
	return false
}

// joined is a and b without duplicates, sorted (never nil).
func joined(a, b []string) []string {
	out := append(append([]string{}, a...), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// RolloutView is a rollout as the API shows it (get-rollouts, the rollout
// SSE event).
type RolloutView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind" description:"template_upgrade or vars_reload."`
	ScopeKey string `json:"scope_key" description:"The template id (template_upgrade) or the org id (vars_reload)."`
	// Revision is a template_upgrade's target revision (0 for vars_reload).
	Revision      int64  `json:"revision" description:"template_upgrade: the target revision; 0 for vars_reload."`
	State         string `json:"state" description:"queued, running, done, stopped or failed."`
	StopRequested bool   `json:"stop_requested" description:"Stop was asked: the rollout stops after the app in flight."`
	Done          int64  `json:"done"`
	Skipped       int64  `json:"skipped" description:"Apps left out: an instance the revision needs new values for, an app that stopped meanwhile."`
	Total         int64  `json:"total" description:"done + skipped + the apps still to do, as of the last step."`
	// CurrentAppID names the app in flight; after a failure, the app that
	// failed.
	CurrentAppID    string     `json:"current_app_id" description:"The app in flight; after a failure, the app that failed."`
	CurrentServerID string     `json:"current_server_id"`
	CurrentAppName  string     `json:"current_app_name"`
	Error           string     `json:"error"`
	CreatedAt       time.Time  `json:"created_at"`
	CreatedBy       string     `json:"created_by"`
	FinishedAt      *time.Time `json:"finished_at"`
}
