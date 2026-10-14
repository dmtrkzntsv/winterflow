package rollouts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"winterflow/internal/domain/model"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/util"
)

// QueueUpgrade queues "Upgrade all": the template's created instances on a
// revision below revision (0: the latest) move to it, oldest first, one at
// a time. Another org's template or an unknown revision is not found; a
// template with an upgrade still active is model.ErrRolloutActive.
func (uc *UseCase) QueueUpgrade(ctx context.Context, userID, orgID, templateID string, revision int64) (model.RolloutView, error) {
	tpl, err := uc.d.Templates.GetTemplate(ctx, orgID, templateID)
	if err != nil {
		return model.RolloutView{}, err
	}
	if revision == 0 {
		revision = tpl.LatestRevision
	}
	if _, err := uc.d.Templates.GetRevision(ctx, tpl.ID, revision); err != nil {
		return model.RolloutView{}, err
	}
	scope, err := json.Marshal(model.UpgradeScope{TemplateID: tpl.ID, Revision: revision})
	if err != nil {
		return model.RolloutView{}, err
	}
	v, ok, err := uc.queue(ctx, model.Rollout{
		ID: util.GenerateID(), OrgID: orgID, Kind: model.RolloutTemplateUpgrade, Scope: scope, ScopeKey: tpl.ID, CreatedBy: userID,
	})
	if err == nil && !ok {
		err = model.ErrRolloutActive
	}
	return v, err
}

// upgrader is the template_upgrade kind.
type upgrader struct{ uc *UseCase }

func upgradeScope(r model.Rollout) (model.UpgradeScope, error) {
	var s model.UpgradeScope
	if err := json.Unmarshal(r.Scope, &s); err != nil || s.TemplateID == "" || s.Revision < 1 {
		return model.UpgradeScope{}, errBadScope
	}
	return s, nil
}

// next is the oldest created instance of the template below the target
// revision that this rollout has not skipped, on one of the org's own
// servers.
func (k upgrader) next(ctx context.Context, r model.Rollout) (pick, error) {
	s, err := upgradeScope(r)
	if err != nil {
		return pick{}, err
	}
	all, err := k.uc.d.Templates.ListUpgradableInstances(ctx, r.OrgID, s.TemplateID, s.Revision)
	if err != nil {
		return pick{}, err
	}
	var todo []model.Instance
	for _, inst := range all {
		if !slices.Contains(r.SkipIDs, inst.ID) {
			todo = append(todo, inst)
		}
	}
	if len(todo) == 0 {
		return pick{}, nil
	}
	inst := todo[0]
	return pick{appID: inst.AppID, serverID: inst.ServerID, skipID: inst.ID, remaining: int64(len(todo)), inst: inst}, nil
}

// run re-renders the instance at the target revision with its stored
// values (the upgrade-instance path) and waits for the save and for the
// app to run. An instance the revision needs new values for, or one gone
// meanwhile, is skipped. A step that went through but left the instance
// below the target (recording the save failed) puts it on the skip list
// too, so it is not picked again.
func (k upgrader) run(ctx context.Context, r model.Rollout, p pick) stepResult {
	s, err := upgradeScope(r)
	if err != nil {
		return failed(err.Error())
	}
	out, err := k.uc.d.Instances.UpdateInstance(ctx, r.CreatedBy, r.OrgID, tuc.UpdateInput{
		ServerID: p.inst.ServerID, Key: p.inst.Key, Revision: s.Revision,
	})
	switch {
	case errors.Is(err, tuc.ErrNeedsValues), errors.Is(err, model.ErrNotFound), errors.Is(err, model.ErrKeyRetired):
		return skipped()
	case err != nil:
		return failed(err.Error())
	}
	if _, err := k.uc.awaitResult(ctx, out.Done); err != nil {
		return failed(err.Error())
	}
	if _, err := k.uc.awaitRunning(ctx, p.serverID, p.appID, k.uc.now()); err != nil {
		return failed(err.Error())
	}
	res := done()
	if inst, err := k.uc.d.Templates.GetInstanceByID(ctx, p.inst.ID); err != nil || inst.Revision < s.Revision {
		res.forget = true
	}
	return res
}
