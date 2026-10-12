// Package alerting evaluates the org-wide alert rules against what the API
// hears from agents (metrics samples, app statuses, liveness) and delivers
// the resulting notifications through the org's channels. Spec:
// monitoring-and-alerts §3 "Evaluation" and "Delivery".
package alerting

import (
	"context"
	"sort"
	"sync"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/metrics"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

const (
	// storeTimeout bounds every evaluator and notifier DB call.
	storeTimeout = 5 * time.Second
	// directoryTTL is how long the target directory (servers, apps and
	// their names) is reused before it is reloaded.
	directoryTTL = time.Minute
	// directoryRetry is the least gap between reloads forced by an event
	// from a server the directory does not know yet.
	directoryRetry = 5 * time.Second
	// historyWindow is how far back Load refills the buffers.
	historyWindow = alert.BufferSize * time.Minute
)

// Store is the slice of the alert repository the evaluator needs.
type Store interface {
	ListRules(ctx context.Context, orgID string) ([]alert.Rule, error)
	ListOverrides(ctx context.Context, orgID string) ([]alert.Override, error)
	FiringStates(ctx context.Context) ([]alert.StateRow, error)
	Fire(ctx context.Context, k alert.Key, value, threshold float64, ev alert.Event) (bool, error)
	Resolve(ctx context.Context, k alert.Key, value float64, ev alert.Event) (bool, error)
	ClearState(ctx context.Context, k alert.Key) error
	Directory(ctx context.Context) (alert.Directory, error)
}

// History is the tier-1 sample read that warms the buffers at startup.
type History interface {
	Tier1Range(ctx context.Context, from, to time.Time) ([]metrics.TargetBucket, error)
}

// Job is one notification for the notifier. ChannelIDs follow
// alert.RouteChannels (nil = the org's default channels).
type Job struct {
	EventID    string
	OrgID      string
	ChannelIDs []string
	Title      string
	Body       string
}

// Queue accepts notification jobs without blocking.
type Queue interface {
	Enqueue(Job) bool
}

// Config wires an Evaluator.
type Config struct {
	Store Store
	// History warms the buffers in Load; nil (metrics off) starts empty.
	History History
	// Queue receives the notification of every transition this evaluator
	// recorded; nil sends nothing.
	Queue Queue
	// Publish pushes a transition to the org's browsers (SSE); nil skips.
	Publish func(ctx context.Context, serverID string, n alert.Notice)
	// WebURL builds the links in notifications ("" = no link).
	WebURL string
	// DownGrace is the least silence before a server counts as down: the
	// liveness TTL, so one or two missed heartbeats never fire.
	DownGrace time.Duration
	Log       *logger.Logger
	// Now and NewID default to time.Now and util.GenerateID.
	Now   func() time.Time
	NewID func() string
}

// Evaluator holds the per-target buffers and states. All methods are safe
// for concurrent use: the events consumer and the 15 s sweep call it from
// different goroutines.
type Evaluator struct {
	cfg Config

	mu      sync.Mutex
	started time.Time
	buffers map[alert.Target]*alert.Buffer
	firing  map[alert.Key]alert.Metric
	heard   map[string]time.Time
	problem map[alert.Target]time.Time
	dir     alert.Directory
	dirAt   time.Time
}

// NewEvaluator builds an evaluator; call Load before feeding it events.
func NewEvaluator(cfg Config) *Evaluator {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = util.GenerateID
	}
	return &Evaluator{
		cfg:     cfg,
		started: cfg.Now(),
		buffers: map[alert.Target]*alert.Buffer{},
		firing:  map[alert.Key]alert.Metric{},
		heard:   map[string]time.Time{},
		problem: map[alert.Target]time.Time{},
	}
}

// Load restores the firing states and refills the buffers from the last
// hour of tier-1 samples, so a restart neither re-fires nor waits a full
// window before deciding. Servers count as heard from now.
func (e *Evaluator) Load(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	e.started = now
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	states, err := e.cfg.Store.FiringStates(sctx)
	if err != nil {
		return err
	}
	for _, s := range states {
		e.firing[s.Key] = s.Metric
	}
	if e.cfg.History == nil {
		return nil
	}
	rows, err := e.cfg.History.Tier1Range(sctx, now.Add(-historyWindow), now.Add(time.Minute))
	if err != nil {
		return err
	}
	// Rows come ordered by server, app ("" first) and time, so a server's
	// host memory is known before its apps' rows need it.
	hostMem := map[string]map[time.Time]float64{}
	for _, row := range rows {
		t := alert.Target{ServerID: row.ServerID, AppID: row.AppID}
		if row.AppID == "" {
			if hostMem[row.ServerID] == nil {
				hostMem[row.ServerID] = map[time.Time]float64{}
			}
			hostMem[row.ServerID][row.Start] = row.Data["mem_total"]
			e.buffer(t).Add(alert.Point{At: row.Start, Values: alert.ServerValues(row.Data)})
			continue
		}
		e.buffer(t).Add(alert.Point{At: row.Start, Values: alert.AppValues(row.Data, hostMem[row.ServerID][row.Start])})
	}
	return nil
}

// ObserveHeartbeat records that a server reported (liveness, capabilities)
// and resolves its down alerts.
func (e *Evaluator) ObserveHeartbeat(ctx context.Context, serverID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	dir := e.directory(ctx, now, serverID)
	e.hear(ctx, dir, serverID, now)
}

// ObserveMetrics feeds one server.metrics sample (at = the sample's time)
// and evaluates the threshold rules of the server and its apps.
func (e *Evaluator) ObserveMetrics(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	dir := e.directory(ctx, now, serverID)
	e.hear(ctx, dir, serverID, now)
	srv, ok := dir.Servers[serverID]
	if !ok {
		return
	}
	targets := []alert.Target{{ServerID: serverID}}
	e.buffer(targets[0]).Add(alert.Point{At: at, Values: alert.ServerValues(server)})
	hostMem := server["mem_total"]
	appIDs := make([]string, 0, len(apps))
	for id := range apps {
		if a, known := dir.Apps[id]; known && a.ServerID == serverID {
			appIDs = append(appIDs, id)
		}
	}
	sort.Strings(appIDs)
	for _, id := range appIDs {
		t := alert.Target{ServerID: serverID, AppID: id}
		e.buffer(t).Add(alert.Point{At: at, Values: alert.AppValues(apps[id], hostMem)})
		targets = append(targets, t)
	}
	rules, overrides, ok := e.rules(ctx, srv.OrgID)
	if !ok {
		return
	}
	e.forgetDeletedRules(dir, srv.OrgID, rules)
	for _, t := range targets {
		e.evalThresholds(ctx, dir, srv, t, rules, overrides, now)
	}
}

// ObserveAppStatus feeds one apps.status report: problematic[appID] is
// whether the app is problematic (crashed, restart-looping, unhealthy).
// An app fires once it stayed problematic for the rule's duration and
// resolves on the first report that is not problematic.
func (e *Evaluator) ObserveAppStatus(ctx context.Context, serverID string, problematic map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	dir := e.directory(ctx, now, serverID)
	e.hear(ctx, dir, serverID, now)
	srv, ok := dir.Servers[serverID]
	if !ok {
		return
	}
	appIDs := make([]string, 0, len(problematic))
	for id := range problematic {
		if a, known := dir.Apps[id]; known && a.ServerID == serverID {
			appIDs = append(appIDs, id)
		}
	}
	sort.Strings(appIDs)
	for _, id := range appIDs {
		t := alert.Target{ServerID: serverID, AppID: id}
		if problematic[id] {
			if _, tracked := e.problem[t]; !tracked {
				e.problem[t] = now
			}
		} else {
			delete(e.problem, t)
		}
	}
	rules, overrides, ok := e.rules(ctx, srv.OrgID)
	if !ok {
		return
	}
	e.forgetDeletedRules(dir, srv.OrgID, rules)
	for _, id := range appIDs {
		t := alert.Target{ServerID: serverID, AppID: id}
		for _, r := range rules {
			if r.TargetKind != alert.TargetApp || r.Metric != alert.MetricDown {
				continue
			}
			k := alert.Key{RuleID: r.ID, Target: t}
			s := alert.Effective(r, overrides[k])
			if !s.Enabled {
				e.clear(ctx, k)
				continue
			}
			since, bad := e.problem[t]
			switch {
			case !bad:
				e.transition(ctx, dir, srv, r, s, k, false, 0, now)
			case now.Sub(since) >= s.Duration:
				e.transition(ctx, dir, srv, r, s, k, true, now.Sub(since).Minutes(), now)
			}
		}
	}
}

// Sweep fires the down rules of servers that have been silent for the
// rule's duration (at least DownGrace). A server never heard since the API
// started counts from the start, not from a stale last_seen. App rules
// need samples or status reports, which a down server does not send, so
// they are naturally suppressed while it is down.
func (e *Evaluator) Sweep(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.cfg.Now()
	dir := e.directory(ctx, now, "")
	ids := make([]string, 0, len(dir.Servers))
	for id := range dir.Servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	type orgRules struct {
		rules     []alert.Rule
		overrides map[alert.Key]*alert.Override
		ok        bool
	}
	byOrg := map[string]orgRules{}
	for _, id := range ids {
		srv := dir.Servers[id]
		or, seen := byOrg[srv.OrgID]
		if !seen {
			or.rules, or.overrides, or.ok = e.rules(ctx, srv.OrgID)
			if or.ok {
				e.forgetDeletedRules(dir, srv.OrgID, or.rules)
			}
			byOrg[srv.OrgID] = or
		}
		if !or.ok {
			continue
		}
		last, heard := e.heard[id]
		if !heard {
			last = e.started
		}
		silent := now.Sub(last)
		for _, r := range or.rules {
			if r.TargetKind != alert.TargetServer || r.Metric != alert.MetricDown {
				continue
			}
			k := alert.Key{RuleID: r.ID, Target: alert.Target{ServerID: id}}
			s := alert.Effective(r, or.overrides[k])
			if !s.Enabled {
				e.clear(ctx, k)
				continue
			}
			if silent >= max(s.Duration, e.cfg.DownGrace) {
				e.transition(ctx, dir, srv, r, s, k, true, silent.Minutes(), now)
			}
		}
	}
}

// hear marks serverID as heard now and resolves its firing down alerts.
func (e *Evaluator) hear(ctx context.Context, dir alert.Directory, serverID string, now time.Time) {
	e.heard[serverID] = now
	var down []alert.Key
	for k, m := range e.firing {
		if m == alert.MetricDown && k.ServerID == serverID && k.AppID == "" {
			down = append(down, k)
		}
	}
	srv, known := dir.Servers[serverID]
	if len(down) == 0 || !known {
		return
	}
	rules, overrides, ok := e.rules(ctx, srv.OrgID)
	if !ok {
		return
	}
	byID := make(map[string]alert.Rule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
	}
	for _, k := range down {
		r, exists := byID[k.RuleID]
		if !exists {
			delete(e.firing, k) // the rule is gone; its state cascaded
			continue
		}
		s := alert.Effective(r, overrides[k])
		if !s.Enabled {
			e.clear(ctx, k)
			continue
		}
		e.transition(ctx, dir, srv, r, s, k, false, 0, now)
	}
}

// evalThresholds decides the threshold rules of one target from its buffer.
func (e *Evaluator) evalThresholds(ctx context.Context, dir alert.Directory, srv alert.ServerRef, t alert.Target, rules []alert.Rule, overrides map[alert.Key]*alert.Override, now time.Time) {
	buf := e.buffers[t]
	if buf == nil {
		return
	}
	for _, r := range rules {
		if r.TargetKind != t.Kind() || r.Metric == alert.MetricDown {
			continue
		}
		k := alert.Key{RuleID: r.ID, Target: t}
		s := alert.Effective(r, overrides[k])
		if !s.Enabled {
			e.clear(ctx, k)
			continue
		}
		avg, ok := buf.Average(r.Metric, s.Duration)
		if !ok {
			continue // not enough data: keep the current state
		}
		e.transition(ctx, dir, srv, r, s, k, avg > s.Threshold, avg, now)
	}
}

// transition moves k to firing (or ok) when it is not already there:
// records it compare-and-set (event included), publishes the SSE notice
// and, if this evaluator's write made the transition, queues the
// notification. A store error leaves the local state unchanged so the next
// evaluation retries.
func (e *Evaluator) transition(ctx context.Context, dir alert.Directory, srv alert.ServerRef, r alert.Rule, s alert.Settings, k alert.Key, firing bool, value float64, now time.Time) {
	if _, was := e.firing[k]; was == firing {
		return
	}
	info, _ := alert.Lookup(r.TargetKind, r.Metric)
	names := alert.Names{Server: srv.Name}
	if k.AppID != "" {
		names.App = k.AppID
		if a, ok := dir.Apps[k.AppID]; ok && a.Name != "" {
			names.App = a.Name
		}
	}
	kind := alert.EventResolved
	if firing {
		kind = alert.EventFired
	}
	msg := alert.Compose(alert.Transition{
		Kind: kind, Info: info, Names: names, Value: value, Threshold: s.Threshold,
		Duration: s.Duration, Link: alert.Link(e.cfg.WebURL, k.Target),
	})
	ev := alert.Event{
		ID: e.cfg.NewID(), OrgID: srv.OrgID, RuleID: r.ID, ServerID: k.ServerID, AppID: k.AppID,
		ServerName: names.Server, AppName: names.App, TargetKind: r.TargetKind, Metric: r.Metric,
		Kind: kind, Value: value, Threshold: s.Threshold, Title: msg.Title, Message: msg.Body, CreatedAt: now,
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	var won bool
	var err error
	if firing {
		won, err = e.cfg.Store.Fire(sctx, k, value, s.Threshold, ev)
	} else {
		won, err = e.cfg.Store.Resolve(sctx, k, value, ev)
	}
	if err != nil {
		e.cfg.Log.Warn("alerts: record transition failed", "rule_id", k.RuleID, "server_id", k.ServerID, "app_id", k.AppID, "error", err)
		return
	}
	if firing {
		e.firing[k] = r.Metric
	} else {
		delete(e.firing, k)
	}
	if e.cfg.Publish != nil {
		e.cfg.Publish(sctx, k.ServerID, alert.Notice{
			Kind: kind, RuleID: r.ID, TargetKind: r.TargetKind, Metric: r.Metric, ServerID: k.ServerID,
			AppID: k.AppID, Value: value, Threshold: s.Threshold, Title: msg.Title,
		})
	}
	if won && e.cfg.Queue != nil {
		e.cfg.Queue.Enqueue(Job{EventID: ev.ID, OrgID: srv.OrgID, ChannelIDs: s.ChannelIDs, Title: msg.Title, Body: msg.Body})
	}
}

// clear drops a firing state silently: its rule or override was disabled.
func (e *Evaluator) clear(ctx context.Context, k alert.Key) {
	if _, firing := e.firing[k]; !firing {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if err := e.cfg.Store.ClearState(sctx, k); err != nil {
		e.cfg.Log.Warn("alerts: clear state failed", "rule_id", k.RuleID, "error", err)
		return
	}
	delete(e.firing, k)
}

// rules loads an org's rules and overrides (keyed by rule and target).
func (e *Evaluator) rules(ctx context.Context, orgID string) ([]alert.Rule, map[alert.Key]*alert.Override, bool) {
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	rules, err := e.cfg.Store.ListRules(sctx, orgID)
	if err != nil {
		e.cfg.Log.Warn("alerts: load rules failed", "org_id", orgID, "error", err)
		return nil, nil, false
	}
	list, err := e.cfg.Store.ListOverrides(sctx, orgID)
	if err != nil {
		e.cfg.Log.Warn("alerts: load overrides failed", "org_id", orgID, "error", err)
		return nil, nil, false
	}
	overrides := make(map[alert.Key]*alert.Override, len(list))
	for i := range list {
		overrides[list[i].Key()] = &list[i]
	}
	return rules, overrides, true
}

// directory returns the target directory, reloading it when it is older
// than directoryTTL, or sooner when serverID is unknown. A failed reload
// keeps the previous one. Every reload prunes the per-target state of
// servers and apps that no longer exist.
func (e *Evaluator) directory(ctx context.Context, now time.Time, serverID string) alert.Directory {
	_, known := e.dir.Servers[serverID]
	fresh := e.dir.Servers != nil && now.Sub(e.dirAt) < directoryTTL
	if fresh && (serverID == "" || known || now.Sub(e.dirAt) < directoryRetry) {
		return e.dir
	}
	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	d, err := e.cfg.Store.Directory(sctx)
	if err != nil {
		e.cfg.Log.Warn("alerts: load directory failed", "error", err)
		return e.dir
	}
	e.dir, e.dirAt = d, now
	exists := func(t alert.Target) bool {
		if _, ok := d.Servers[t.ServerID]; !ok {
			return false
		}
		if t.AppID == "" {
			return true
		}
		a, ok := d.Apps[t.AppID]
		return ok && a.ServerID == t.ServerID
	}
	for t := range e.buffers {
		if !exists(t) {
			delete(e.buffers, t)
		}
	}
	for t := range e.problem {
		if !exists(t) {
			delete(e.problem, t)
		}
	}
	for k := range e.firing {
		if !exists(k.Target) {
			delete(e.firing, k)
		}
	}
	for id := range e.heard {
		if _, ok := d.Servers[id]; !ok {
			delete(e.heard, id)
		}
	}
	return d
}

// forgetDeletedRules removes firing entries for rules that no longer exist.
func (e *Evaluator) forgetDeletedRules(dir alert.Directory, orgID string, rules []alert.Rule) {
	rulesByID := make(map[string]bool, len(rules))
	for _, r := range rules {
		rulesByID[r.ID] = true
	}
	for k := range e.firing {
		if !rulesByID[k.RuleID] {
			// The rule is gone; check if this target's org is orgID
			if srv, ok := dir.Servers[k.ServerID]; ok && srv.OrgID == orgID {
				delete(e.firing, k)
			}
		}
	}
}

// buffer returns t's buffer, creating it.
func (e *Evaluator) buffer(t alert.Target) *alert.Buffer {
	b := e.buffers[t]
	if b == nil {
		b = &alert.Buffer{}
		e.buffers[t] = b
	}
	return b
}
