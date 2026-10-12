package alert

import (
	"fmt"
	"math"
	"time"
)

// Target is what a rule is evaluated for: a server (AppID "") or one app on
// it, the same addressing as metric_samples.
type Target struct {
	ServerID string `json:"server_id"`
	AppID    string `json:"app_id"`
}

// Kind is the target's kind.
func (t Target) Kind() TargetKind {
	if t.AppID == "" {
		return TargetServer
	}
	return TargetApp
}

// Key addresses one rule's state for one target.
type Key struct {
	RuleID string
	Target
}

// Rule is an org-wide alert rule. ChannelIDs nil routes to the org's
// default channels, an empty list to none (History only), otherwise to
// exactly those channels. Down rules carry Threshold 0.
type Rule struct {
	ID              string     `json:"id"`
	OrgID           string     `json:"-"`
	TargetKind      TargetKind `json:"target_kind"`
	Metric          Metric     `json:"metric"`
	Threshold       float64    `json:"threshold"`
	DurationMinutes int        `json:"duration_minutes"`
	ChannelIDs      []string   `json:"channel_ids"`
	Enabled         bool       `json:"enabled"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Normalize zeroes the threshold of a metric that has none.
func (r *Rule) Normalize() {
	if info, ok := Lookup(r.TargetKind, r.Metric); ok && !info.HasThreshold() {
		r.Threshold = 0
	}
}

// Validate checks the metric exists for the kind, the duration is 1..60
// minutes and the threshold is within the metric's range.
func (r Rule) Validate() error {
	info, ok := Lookup(r.TargetKind, r.Metric)
	if !ok {
		return fmt.Errorf("unknown metric %q for %q targets", r.Metric, r.TargetKind)
	}
	if err := validDuration(r.DurationMinutes); err != nil {
		return err
	}
	if info.HasThreshold() {
		return validThreshold(info, r.Threshold)
	}
	return nil
}

func validDuration(m int) error {
	if m < MinDuration || m > MaxDuration {
		return fmt.Errorf("duration must be %d to %d minutes", MinDuration, MaxDuration)
	}
	return nil
}

func validThreshold(info MetricInfo, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || (info.Max > 0 && v > info.Max) {
		if info.Max > 0 {
			return fmt.Errorf("%s threshold must be above 0 and at most %s", info.Label, FormatValue(info, info.Max))
		}
		return fmt.Errorf("%s threshold must be above 0", info.Label)
	}
	return nil
}

// DefaultRules are seeded once per org (spec §3 "Defaults"): enabled, routed
// to the org's default channels.
func DefaultRules() []Rule {
	return []Rule{
		{TargetKind: TargetServer, Metric: MetricDown, DurationMinutes: 5, Enabled: true},
		{TargetKind: TargetServer, Metric: MetricCPU, Threshold: 90, DurationMinutes: 10, Enabled: true},
		{TargetKind: TargetServer, Metric: MetricMemory, Threshold: 90, DurationMinutes: 10, Enabled: true},
		{TargetKind: TargetServer, Metric: MetricDisk, Threshold: 90, DurationMinutes: 5, Enabled: true},
		{TargetKind: TargetApp, Metric: MetricDown, DurationMinutes: 5, Enabled: true},
	}
}

// Override adjusts one rule for one target. Nil fields inherit the rule's;
// ChannelIDs nil inherits, an empty list silences, a list reroutes.
type Override struct {
	RuleID          string   `json:"rule_id"`
	ServerID        string   `json:"server_id"`
	AppID           string   `json:"app_id"`
	Disabled        bool     `json:"disabled"`
	Threshold       *float64 `json:"threshold"`
	DurationMinutes *int     `json:"duration_minutes"`
	ChannelIDs      []string `json:"channel_ids"`
}

// Key is the override's (rule, target).
func (o Override) Key() Key {
	return Key{RuleID: o.RuleID, Target: Target{ServerID: o.ServerID, AppID: o.AppID}}
}

// Validate checks the override fits its rule: the target kind matches and
// any threshold/duration is in range (down rules take no threshold).
func (o Override) Validate(r Rule) error {
	if o.ServerID == "" {
		return fmt.Errorf("server_id is required")
	}
	if (o.AppID == "") != (r.TargetKind == TargetServer) {
		return fmt.Errorf("a %s rule needs a %s target", r.TargetKind, r.TargetKind)
	}
	info, ok := Lookup(r.TargetKind, r.Metric)
	if !ok {
		return fmt.Errorf("unknown metric %q", r.Metric)
	}
	if o.DurationMinutes != nil {
		if err := validDuration(*o.DurationMinutes); err != nil {
			return err
		}
	}
	if o.Threshold != nil {
		if !info.HasThreshold() {
			return fmt.Errorf("%s has no threshold", info.Label)
		}
		return validThreshold(info, *o.Threshold)
	}
	return nil
}

// Settings are a rule's effective settings for one target.
type Settings struct {
	Enabled    bool
	Threshold  float64
	Duration   time.Duration
	ChannelIDs []string
}

// Effective merges the rule with the target's override (nil: none).
func Effective(r Rule, o *Override) Settings {
	s := Settings{
		Enabled:    r.Enabled,
		Threshold:  r.Threshold,
		Duration:   time.Duration(r.DurationMinutes) * time.Minute,
		ChannelIDs: r.ChannelIDs,
	}
	if o == nil {
		return s
	}
	if o.Disabled {
		s.Enabled = false
	}
	if o.Threshold != nil {
		s.Threshold = *o.Threshold
	}
	if o.DurationMinutes != nil {
		s.Duration = time.Duration(*o.DurationMinutes) * time.Minute
	}
	if o.ChannelIDs != nil {
		s.ChannelIDs = o.ChannelIDs
	}
	return s
}
