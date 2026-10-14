package command

import (
	"fmt"
	"time"

	"winterflow/internal/domain/model"
)

// SharedVar is one entry of a vars.sync snapshot. A plain entry carries
// Value; a secret carries Ciphertext (ECIES for the target server's key),
// or nothing when the browser has not encrypted it for this server yet —
// the agent then treats the name as missing and warns when an app uses it.
type SharedVar struct {
	Name       string `json:"name"`
	IsSecret   bool   `json:"is_secret"`
	Value      string `json:"value,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
}

// VarRef names one variable of a snapshot by scope ("org" or "server").
type VarRef struct {
	Scope string `json:"scope"`
	Name  string `json:"name"`
}

// SyncVarsRequest is vars.sync: the target server's full effective set of
// org- and server-level variables at Revision. The agent stores it as its
// snapshot (ignoring one older than the stored one) and renders nothing.
type SyncVarsRequest struct {
	Revision   int64       `json:"revision"`
	ServerID   string      `json:"server_id"`
	ServerName string      `json:"server_name"`
	OrgID      string      `json:"org_id"`
	Org        []SharedVar `json:"org"`
	Server     []SharedVar `json:"server"`
}

// Validate refuses names the agent would never render and malformed
// entries, so a bad snapshot is rejected before it replaces a good one.
func (r SyncVarsRequest) Validate() error {
	if r.Revision < 0 {
		return fmt.Errorf("invalid revision %d", r.Revision)
	}
	for _, scope := range []struct {
		name string
		vars []SharedVar
	}{{"org", r.Org}, {"server", r.Server}} {
		seen := map[string]bool{}
		for _, v := range scope.vars {
			if err := model.ValidateSharedVariableName(v.Name); err != nil {
				return fmt.Errorf("%s variable: %w", scope.name, err)
			}
			if seen[v.Name] {
				return fmt.Errorf("duplicate %s variable %s", scope.name, v.Name)
			}
			seen[v.Name] = true
			if v.IsSecret && v.Value != "" {
				return fmt.Errorf("secret %s carries a plain value", v.Name)
			}
			if !v.IsSecret && v.Ciphertext != "" {
				return fmt.Errorf("plain variable %s carries a ciphertext", v.Name)
			}
		}
	}
	return nil
}

// SyncVarsResponse reports the revision the agent now holds and the
// secrets it holds but cannot decrypt; the API drops those ciphertexts, so
// the names show as missing on this server.
type SyncVarsResponse struct {
	Revision      int64    `json:"revision"`
	Undecryptable []VarRef `json:"undecryptable"`
}

// ReloadAppWait bounds a single-app vars.reload on the agent: the app's
// turn on the reload worker plus its reload (whose own deadline is also 10
// minutes, so a reload that starts late answers this bound's timeout).
const ReloadAppWait = 10 * time.Minute

// ReloadVarsRequest is vars.reload: re-render and `compose up -d` running
// apps. AppIDs selects those apps; else Names selects the stale apps whose
// stale names include one of them; neither = every stale running app.
// Exactly one app id is the waitable form: the answer comes when that
// app's reload ends (an error when it failed); any other form answers once
// queued and reports each app as a vars.reloaded event.
type ReloadVarsRequest struct {
	Names  []string `json:"names,omitempty"`
	AppIDs []string `json:"app_ids,omitempty"`
}

// Validate refuses app ids that are not single path elements and names
// that cannot be shared variables.
func (r ReloadVarsRequest) Validate() error {
	for _, id := range r.AppIDs {
		if err := validateAppID(id); err != nil {
			return err
		}
	}
	for _, n := range r.Names {
		if err := model.ValidateSharedVariableName(n); err != nil {
			return err
		}
	}
	return nil
}

// ReloadVarsResponse acknowledges a queued reload; each app's outcome
// arrives later as a vars.reloaded event. The single-app form answers when
// the app's reload ended: AppID, the names that were stale, and Skipped
// when the app was not running (nothing reloaded, nothing failed).
type ReloadVarsResponse struct {
	Queued  bool     `json:"queued"`
	AppID   string   `json:"app_id,omitempty"`
	Names   []string `json:"names,omitempty"`
	Skipped bool     `json:"skipped,omitempty"`
}

// VarsReloadedPayload is one app's reload outcome: the body of the agent's
// vars.reloaded event and, once the API sets ServerID, the payload of the
// vars.reloaded SSE notification.
type VarsReloadedPayload struct {
	ServerID string   `json:"server_id"`
	AppID    string   `json:"app_id"`
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
	Names    []string `json:"names"`
}
