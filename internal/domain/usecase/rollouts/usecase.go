// Package rollouts is the rollout queue: queued, resumable,
// one-app-at-a-time jobs (upgrade a template's instances, reload the apps a
// shared-variable change made stale). One worker goroutine per API process
// claims a rollout with a lease, takes its steps and renews the lease
// meanwhile; another process takes over a rollout whose lease expired. No
// transaction is held while a step waits on an agent.
package rollouts

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// Bounds. Every wait of a step is bounded; the idle worker costs one
// indexed query a minute.
const (
	// leaseFor is how long a claim (or renewal) holds a rollout.
	leaseFor = 2 * time.Minute
	// renewEvery renews the lease well inside it.
	renewEvery = 30 * time.Second
	// idleTick is how often the idle worker looks for claimable work.
	idleTick = time.Minute
	// commandWait bounds a step's command (an image pull, a reload); the
	// agent's own bound is the same 10 minutes, so its timeout answer
	// arrives first.
	commandWait = 10*time.Minute + 30*time.Second
	// healthWait bounds the wait for the app to report running.
	healthWait = 5 * time.Minute
	// healthPoll re-reads the status cache (memory only) while waiting.
	healthPoll = time.Second
	// settleWait is how long a vars_reload waits for a server to report
	// the change it was pushed; settlePoll how often it looks.
	settleWait = 5 * time.Minute
	settlePoll = 5 * time.Second
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

// Members lists an org's users, the audience of rollout events
// (port.UserService satisfies it).
type Members interface {
	ListMembers(ctx context.Context, orgID string) ([]model.Member, error)
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

	// Bounds, shortened by tests.
	lease, renew, idle, cmdWait, health, healthPoll, settle, settlePoll time.Duration
}

// New builds the usecase; Run starts its worker.
func New(d Deps) *UseCase {
	uc := &UseCase{
		d: d, owner: util.GenerateID(), wake: make(chan struct{}, 1), now: time.Now,
		lease: leaseFor, renew: renewEvery, idle: idleTick, cmdWait: commandWait,
		health: healthWait, healthPoll: healthPoll, settle: settleWait, settlePoll: settlePoll,
	}
	uc.kinds = map[string]runner{model.RolloutTemplateUpgrade: upgrader{uc}}
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
// ended ones newest first (scopeKey narrows to one scope). Another org's
// template, or another org as a vars_reload scope, is not found.
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
	}
	list, err := uc.d.Repo.ListRollouts(ctx, orgID, kind, scopeKey)
	if err != nil {
		return nil, err
	}
	out := make([]model.RolloutView, 0, len(list))
	for _, r := range list {
		out = append(out, uc.view(ctx, r))
	}
	return out, nil
}

// Stop asks the org's rollout to stop after the step in flight. An ended
// rollout answers as it is.
func (uc *UseCase) Stop(ctx context.Context, orgID, id string) (model.RolloutView, error) {
	ok, err := uc.d.Repo.RequestStop(ctx, orgID, id)
	if err != nil {
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
	if ok {
		uc.Wake()
		uc.publish(ctx, orgID, v)
	}
	return v, nil
}

// view is a rollout as the API shows it.
func (uc *UseCase) view(ctx context.Context, r model.Rollout) model.RolloutView {
	v := model.RolloutView{
		ID: r.ID, Kind: r.Kind, ScopeKey: r.ScopeKey, State: string(r.State), StopRequested: r.StopRequested,
		Done: r.Done, Skipped: r.Skipped, Total: r.Total, CurrentAppID: r.CurrentAppID,
		CurrentServerID: r.CurrentServerID, Error: r.Error, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy,
		FinishedAt: r.FinishedAt,
	}
	if r.Kind == model.RolloutTemplateUpgrade {
		var s model.UpgradeScope
		if json.Unmarshal(r.Scope, &s) == nil {
			v.Revision = s.Revision
		}
	}
	if r.CurrentAppID != "" && uc.d.Apps != nil {
		if app, err := uc.d.Apps.GetApp(ctx, r.CurrentAppID); err == nil {
			v.CurrentAppName = app.Name
		}
	}
	return v
}

// publish sends a rollout event to every member of the org.
func (uc *UseCase) publish(ctx context.Context, orgID string, v model.RolloutView) {
	if uc.d.Members == nil || uc.d.Notifications == nil {
		return
	}
	members, err := uc.d.Members.ListMembers(ctx, orgID)
	if err != nil {
		uc.d.Log.Debug("rollout event: list members", "org_id", orgID, "error", err)
		return
	}
	n := model.Notification{Type: model.NotificationRollout, Payload: v, Timestamp: uc.now()}
	for _, m := range members {
		uc.d.Notifications.Publish(m.ID, n)
	}
}
