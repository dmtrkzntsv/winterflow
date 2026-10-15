// Package rollouts is the rollout queue: queued, resumable,
// one-app-at-a-time jobs (upgrade a template's instances, reload the apps a
// shared-variable change made stale). One worker goroutine per API process
// claims a rollout with a lease, takes its steps and renews the lease
// meanwhile; another process takes over a rollout whose lease expired. No
// transaction is held while a step waits on an agent.
package rollouts

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// Timing is the worker's bounds. Every wait of a step is bounded; the idle
// worker costs one indexed query a minute. A zero field takes its default
// (defaultTiming); tests shorten them.
type Timing struct {
	// Lease is how long a claim (or renewal) holds a rollout; Renew renews
	// it well inside that.
	Lease, Renew time.Duration
	// Idle is how often the idle worker looks for claimable work.
	Idle time.Duration
	// Command bounds a step's command (an image pull, a reload); the
	// agent's own bound is the same 10 minutes, so its timeout answer
	// arrives first.
	Command time.Duration
	// Health bounds the wait for the app to report running; HealthPoll is
	// how often the status cache (memory only) is re-read meanwhile.
	Health, HealthPoll time.Duration
	// HealthSettle is how far apart the receive times of the two healthy
	// reports a step needs must be (the agent reports every 30 s, so the
	// second is normally the next one).
	HealthSettle time.Duration
	// Settle is how long a vars_reload waits for a server to report the
	// change it was pushed; SettlePoll how often it looks.
	Settle, SettlePoll time.Duration
}

// defaultTiming is the production Timing.
var defaultTiming = Timing{
	Lease:        2 * time.Minute,
	Renew:        30 * time.Second,
	Idle:         time.Minute,
	Command:      10*time.Minute + 30*time.Second,
	Health:       5 * time.Minute,
	HealthPoll:   5 * time.Second,
	HealthSettle: 20 * time.Second,
	Settle:       5 * time.Minute,
	SettlePoll:   5 * time.Second,
}

// withDefaults is t with each zero field set to its default.
func (t Timing) withDefaults() Timing {
	for _, f := range []struct {
		v   *time.Duration
		def time.Duration
	}{
		{&t.Lease, defaultTiming.Lease}, {&t.Renew, defaultTiming.Renew}, {&t.Idle, defaultTiming.Idle},
		{&t.Command, defaultTiming.Command}, {&t.Health, defaultTiming.Health}, {&t.HealthPoll, defaultTiming.HealthPoll},
		{&t.HealthSettle, defaultTiming.HealthSettle}, {&t.Settle, defaultTiming.Settle}, {&t.SettlePoll, defaultTiming.SettlePoll},
	} {
		if *f.v == 0 {
			*f.v = f.def
		}
	}
	return t
}

const (
	// keepEnded is how many ended rollouts a scope keeps.
	keepEnded = 20
	// notifyTimeout bounds the reads a notification needs.
	notifyTimeout = 30 * time.Second
)

// InstanceUpgrader re-renders an instance at another revision and
// dispatches its save (*templates.UseCase).
type InstanceUpgrader interface {
	UpdateInstance(ctx context.Context, userID, orgID string, in tuc.UpdateInput) (tuc.Outcome, error)
}

// Reports is the status cache as the worker reads it (*status.Cache).
type Reports interface {
	LatestReport(serverID string, now time.Time) (status.Report, bool)
}

// AppNames names apps in views (port.AppRepository satisfies it).
type AppNames interface {
	GetApp(ctx context.Context, appID string) (model.App, error)
}

// Members lists an org's user ids, the audience of rollout events (the
// server repository satisfies it).
type Members interface {
	GetOrgUserIDs(ctx context.Context, orgID string) ([]string, error)
}

// Publisher delivers one SSE notification to a user.
type Publisher interface {
	Publish(userID string, n model.Notification)
}

// Deps wires the usecase. With zero Deps (route documentation) it is never
// called.
type Deps struct {
	Repo          port.RolloutRepository
	Templates     port.TemplateRepository
	Instances     InstanceUpgrader
	Dispatcher    port.CommandDispatcher
	Status        Reports
	Apps          AppNames
	Members       Members
	Notifications Publisher
	Log           *logger.Logger
	// Timing is the worker's bounds (zero: the defaults).
	Timing Timing
}

// UseCase queues, lists and stops rollouts, and drives them (Run). Build
// one per API process and share it: Wake reaches the worker only through
// the same value.
type UseCase struct {
	d     Deps
	owner string
	wake  chan struct{}
	kinds map[string]runner
	now   func() time.Time
	t     Timing
}

// New builds the usecase; Run starts its worker.
func New(d Deps) *UseCase {
	uc := &UseCase{
		d: d, owner: util.GenerateID(), wake: make(chan struct{}, 1), now: time.Now, t: d.Timing.withDefaults(),
	}
	uc.kinds = map[string]runner{model.RolloutTemplateUpgrade: upgrader{uc}, model.RolloutVarsReload: reloader{uc}}
	return uc
}

// Wake makes the worker look for work now. It never blocks.
func (uc *UseCase) Wake() {
	select {
	case uc.wake <- struct{}{}:
	default:
	}
}

// queue inserts r (queued) and tells the worker and the org; false when
// its scope already has an active rollout. The answer is read before the
// worker is woken, so it shows the rollout queued.
func (uc *UseCase) queue(ctx context.Context, r model.Rollout) (model.RolloutView, bool, error) {
	ok, err := uc.d.Repo.InsertRollout(ctx, r)
	if err != nil || !ok {
		return model.RolloutView{}, ok, err
	}
	stored, err := uc.d.Repo.GetRollout(ctx, r.ID)
	uc.Wake()
	if err != nil {
		return model.RolloutView{}, true, err
	}
	v := uc.view(ctx, stored)
	uc.publish(ctx, stored.OrgID, v)
	return v, true, nil
}

// List returns an org's rollouts of kind: the active ones first, then the
// ended ones newest first (scopeKey narrows to one scope: a template id for
// template_upgrade, the org id for vars_reload). A template the org can't
// read, or another org as a vars_reload scope, is not found.
func (uc *UseCase) List(ctx context.Context, orgID, kind, scopeKey string) ([]model.RolloutView, error) {
	switch {
	case kind != model.RolloutTemplateUpgrade && kind != model.RolloutVarsReload:
		return nil, model.Rejected("kind must be template_upgrade or vars_reload")
	case kind == model.RolloutVarsReload && scopeKey != "" && scopeKey != orgID:
		return nil, model.ErrRolloutNotFound
	case kind == model.RolloutTemplateUpgrade && scopeKey != "":
		if _, err := uc.d.Templates.GetTemplate(ctx, orgID, scopeKey); err != nil {
			return nil, err
		}
		scopeKey = model.UpgradeScopeKey(orgID, scopeKey)
	}
	list, err := uc.d.Repo.ListRollouts(ctx, orgID, kind, scopeKey)
	if err != nil {
		return nil, err
	}
	out := make([]model.RolloutView, 0, len(list))
	names := map[string]string{} // app id -> name: each app looked up once
	for _, r := range list {
		name, ok := names[r.CurrentAppID]
		if !ok {
			name = uc.appName(ctx, r.CurrentAppID)
			names[r.CurrentAppID] = name
		}
		out = append(out, viewOf(r, name))
	}
	return out, nil
}

// Stop ends the org's rollout at once when nobody drives it (queued, parked
// while it waits for a server, or its owner's lease expired), else asks it
// to stop after the step in flight. An ended rollout answers as it is.
func (uc *UseCase) Stop(ctx context.Context, orgID, id string) (model.RolloutView, error) {
	ended, err := uc.d.Repo.StopUnleased(ctx, orgID, id, uc.now())
	if err != nil {
		return model.RolloutView{}, err
	}
	requested := false
	if ended {
		// Committed: announce it and prune the scope's old ones whatever
		// the read below does.
		uc.ended(ctx, id)
	} else if requested, err = uc.d.Repo.RequestStop(ctx, orgID, id); err != nil {
		return model.RolloutView{}, err
	}
	r, err := uc.d.Repo.GetRollout(ctx, id)
	if err != nil {
		return model.RolloutView{}, err
	}
	if r.OrgID != orgID {
		return model.RolloutView{}, model.ErrRolloutNotFound
	}
	v := uc.view(ctx, r)
	if requested {
		uc.Wake()
		uc.publish(ctx, orgID, v)
	}
	return v, nil
}

// view is a rollout as the API shows it.
func (uc *UseCase) view(ctx context.Context, r model.Rollout) model.RolloutView {
	return viewOf(r, uc.appName(ctx, r.CurrentAppID))
}

// viewOf is r as the API shows it, its app in flight named name.
func viewOf(r model.Rollout, name string) model.RolloutView {
	v := model.RolloutView{
		ID: r.ID, Kind: r.Kind, ScopeKey: r.ScopeKey, State: string(r.State), StopRequested: r.StopRequested,
		Done: r.Done, Skipped: r.Skipped, Total: r.Total, CurrentAppID: r.CurrentAppID,
		CurrentServerID: r.CurrentServerID, CurrentAppName: name, Error: r.Error, CreatedAt: r.CreatedAt,
		CreatedBy: r.CreatedBy, FinishedAt: r.FinishedAt,
	}
	if r.Kind == model.RolloutTemplateUpgrade {
		if s, err := upgradeScope(r); err == nil {
			// The API names an upgrade's scope by its template; the stored
			// key also holds the org.
			v.ScopeKey = s.TemplateID
			v.Revision = s.Revision
		}
	}
	return v
}

// appName is the name of appID ("" when there is none or it is unknown).
func (uc *UseCase) appName(ctx context.Context, appID string) string {
	if appID == "" || uc.d.Apps == nil {
		return ""
	}
	app, err := uc.d.Apps.GetApp(ctx, appID)
	if err != nil {
		return ""
	}
	return app.Name
}

// publish sends a rollout event to every member of the org.
func (uc *UseCase) publish(ctx context.Context, orgID string, v model.RolloutView) {
	if uc.d.Members == nil || uc.d.Notifications == nil {
		return
	}
	users, err := uc.d.Members.GetOrgUserIDs(ctx, orgID)
	if err != nil {
		uc.d.Log.Debug("rollout event: list members", "org_id", orgID, "error", err)
		return
	}
	n := model.Notification{Type: model.NotificationRollout, Payload: v, Timestamp: uc.now()}
	for _, id := range users {
		uc.d.Notifications.Publish(id, n)
	}
}
