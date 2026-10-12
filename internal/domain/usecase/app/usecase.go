package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

type UseCase struct {
	dispatcher    port.CommandDispatcher
	repo          port.AppRepository
	domains       port.AppDomainRepository
	serverDomains port.ServerDomainRepository
	// serverStorages indexes each server's storages: the storage
	// routes keep it current; save-app checks WF_STORAGE_ references
	// against it.
	serverStorages port.ServerStorageRepository
	// servers answers whether a user's organizations own a server (nil: no
	// server is known to be theirs).
	servers ServerAccess
	// instances knows the apps template instances manage (nil: none do).
	instances port.InstanceLinks
	log       *logger.Logger
}

// ServerAccess answers whether a server belongs to one of a user's
// organizations (port.ServerRepository satisfies it).
type ServerAccess = port.ServerOwnership

type Deps struct {
	CommandDispatcher      port.CommandDispatcher
	AppRepository          port.AppRepository
	AppDomainRepository    port.AppDomainRepository
	ServerDomainRepository port.ServerDomainRepository
	// ServerStorageRepository indexes each server's storages.
	ServerStorageRepository port.ServerStorageRepository
	// Servers lets a lookup in the catalog stay within the caller's
	// organizations.
	Servers ServerAccess
	// Instances knows the apps template instances manage: save-app and
	// rollback-app refuse them, delete-app retires their keys (nil: no
	// templates).
	Instances port.InstanceLinks
	Log       *logger.Logger
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{
		dispatcher:     d.CommandDispatcher,
		repo:           d.AppRepository,
		domains:        d.AppDomainRepository,
		serverDomains:  d.ServerDomainRepository,
		serverStorages: d.ServerStorageRepository,
		servers:        d.Servers,
		instances:      d.Instances,
		log:            d.Log,
	}
}

// GetApps returns the app list from the DB cache (API-local, synchronous). The
// agent-authoritative reconcile happens separately via RefreshApps.
func (uc *UseCase) GetApps(ctx context.Context, serverID string) ([]model.App, error) {
	return uc.repo.GetApps(ctx, serverID)
}

// ListDomains returns the server's indexed domains grouped by app id, for
// decorating the DB-backed app listing without an agent round-trip.
func (uc *UseCase) ListDomains(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error) {
	if uc.domains == nil {
		return nil, nil
	}
	return uc.domains.ListForServer(ctx, serverID)
}

// RefreshApps dispatches apps.list to the server's agent (its filesystem is the
// source of truth) and returns the request id. On the agent's result the DB
// cache is reconciled (SyncApps: upsert reported, delete missing) and the
// reconciled list is delivered to the user over SSE.
func (uc *UseCase) RefreshApps(ctx context.Context, userID, serverID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppsList,
		Payload: command.ListAppsRequest{},
		OnResult: func(res port.CommandResult) {
			if !res.Success || len(res.Payload) == 0 {
				return
			}
			var listed command.ListAppsResponse
			if err := json.Unmarshal(res.Payload, &listed); err != nil {
				uc.log.Error("RefreshApps: decode result", "error", err)
				return
			}
			if err := uc.repo.SyncApps(context.Background(), serverID, listed.Apps); err != nil {
				uc.log.Error("RefreshApps: sync", "error", err, "server_id", serverID)
			}
			if uc.domains != nil {
				if err := uc.domains.ReplaceForServer(context.Background(), serverID, listed.Apps); err != nil {
					uc.log.Error("RefreshApps: sync domain rows", "error", err, "server_id", serverID)
				}
			}
		},
	})
}

// ControlApp dispatches a lifecycle action (start/stop/restart/update) to the
// server's agent. Pure agent operation: no DB change; the result flows over SSE.
func (uc *UseCase) ControlApp(ctx context.Context, userID, serverID, appID string, action command.ControlAction) (string, error) {
	return uc.ControlAppObserved(ctx, userID, serverID, appID, action, nil)
}

// ControlAppObserved is ControlApp with an observer on the result.
func (uc *UseCase) ControlAppObserved(ctx context.Context, userID, serverID, appID string, action command.ControlAction, observe Observer) (string, error) {
	in := port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppControl,
		Payload: command.ControlAppRequest{AppID: appID, Action: action},
	}
	if observe != nil {
		in.OnResult = func(res port.CommandResult) { observe(res) }
		in.Wait = ObservedResultTTL
	}
	return uc.dispatcher.Dispatch(ctx, in)
}

// DeleteApp dispatches an app.delete to the agent and, on success, removes the
// app from the DB cache. deleteBackups also deletes the app's backups (else
// the agent marks them as a deleted app's).
func (uc *UseCase) DeleteApp(ctx context.Context, userID, serverID, appID string, deleteVolumes, deleteBackups bool) (string, error) {
	return uc.DeleteAppObserved(ctx, userID, serverID, appID, deleteVolumes, deleteBackups, nil)
}

// DeleteAppObserved is DeleteApp with an observer on the result (it runs
// after the app's rows are removed). On success the app's live template
// instance, if any, is retired: its key stays taken.
func (uc *UseCase) DeleteAppObserved(ctx context.Context, userID, serverID, appID string, deleteVolumes, deleteBackups bool, observe Observer) (string, error) {
	in := port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppDelete,
		Payload: command.DeleteAppRequest{AppID: appID, DeleteVolumes: deleteVolumes, DeleteBackups: deleteBackups},
		OnResult: func(res port.CommandResult) {
			defer notify(observe, res)
			if !res.Success {
				return
			}
			if err := uc.repo.DeleteApp(context.Background(), appID); err != nil {
				uc.log.Error("DeleteApp: remove row", "error", err, "app_id", appID)
			}
			if uc.domains != nil {
				if err := uc.domains.DeleteForApp(context.Background(), appID); err != nil {
					uc.log.Error("DeleteApp: remove domain rows", "error", err, "app_id", appID)
				}
			}
			if uc.instances != nil {
				if err := uc.instances.RetireForApp(context.Background(), serverID, appID); err != nil {
					uc.log.Error("DeleteApp: retire the template instance", "error", err, "app_id", appID)
				}
			}
		},
	}
	if observe != nil {
		in.Wait = ObservedResultTTL
	}
	return uc.dispatcher.Dispatch(ctx, in)
}

// RenameApp dispatches an app.rename to the agent and, on success, updates the
// app's name in the DB cache.
func (uc *UseCase) RenameApp(ctx context.Context, userID, serverID, appID, name string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppRename,
		Payload: command.RenameAppRequest{AppID: appID, Name: name},
		OnResult: func(res port.CommandResult) {
			if !res.Success {
				return
			}
			if err := uc.repo.RenameApp(context.Background(), appID, name); err != nil {
				uc.log.Error("RenameApp: update row", "error", err, "app_id", appID)
			}
		},
	})
}

// GetApp dispatches an app.get to the agent (its filesystem holds the config).
// The result is delivered over SSE, correlated by request id.
func (uc *UseCase) GetApp(ctx context.Context, userID, serverID, appID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppGet,
		Payload: command.GetAppRequest{AppID: appID},
	})
}

// GetRevisions dispatches an app.revisions to the agent: the app's git
// history, newest first. The result is delivered over SSE.
func (uc *UseCase) GetRevisions(ctx context.Context, userID, serverID, appID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppRevisions,
		Payload: command.GetRevisionsRequest{AppID: appID},
	})
}

// RollbackApp dispatches an app.rollback: the agent restores the given commit
// as a new revision and redeploys. The result is delivered over SSE. On
// success the restored config is re-fetched so the domain index follows the
// rollback. An app a live template instance manages is refused.
func (uc *UseCase) RollbackApp(ctx context.Context, userID, serverID, appID, hash string) (string, error) {
	if err := uc.requireUnmanaged(ctx, serverID, appID); err != nil {
		return "", err
	}
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppRollback,
		Payload: command.RollbackAppRequest{AppID: appID, Hash: hash},
		OnResult: func(res port.CommandResult) {
			if !res.Success || uc.domains == nil {
				return
			}
			_, err := uc.dispatcher.Dispatch(context.Background(), port.DispatchInput{
				AgentID: serverID,
				UserID:  userID,
				Type:    command.TypeAppGet,
				Payload: command.GetAppRequest{AppID: appID},
				OnResult: func(got port.CommandResult) {
					if !got.Success || len(got.Payload) == 0 {
						return
					}
					var resp command.GetAppResponse
					if err := json.Unmarshal(got.Payload, &resp); err != nil {
						uc.log.Error("RollbackApp: decode app.get", "error", err)
						return
					}
					ing, err := model.ParseIngress(resp.App.Config)
					if err != nil {
						uc.log.Error("RollbackApp: parse restored config", "error", err)
						return
					}
					if ing == nil {
						return
					}
					if err := uc.domains.ReplaceForApp(context.Background(), appID, serverID, ing); err != nil {
						uc.log.Error("RollbackApp: update domain index", "error", err, "app_id", appID)
					}
				},
			})
			if err != nil {
				uc.log.Error("RollbackApp: dispatch app.get", "error", err)
			}
		},
	})
}

// GetImageTags dispatches an image.tags to the agent, which queries the
// registry with its own docker credentials. The tag list returns over SSE.
func (uc *UseCase) GetImageTags(ctx context.Context, userID, serverID, image string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeImageTags,
		Payload: command.ImageTagsRequest{Image: image},
	})
}

// GetLogs dispatches an app.logs to the agent. The log entries return over SSE.
func (uc *UseCase) GetLogs(ctx context.Context, userID, serverID, appID string, since int64, tail int32) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppLogs,
		Payload: command.GetLogsRequest{AppID: appID, Since: since, Tail: tail},
	})
}

// DomainCheck answers GET /api/v1/domains/check: the apps on the server
// already claiming the hostname, and the registration covering it (nil when
// none does).
type DomainCheck struct {
	Claims       []model.DomainClaim
	Registration *model.RegisteredDomain
}

// ValidateCheckDomain is the syntax check of GET /api/v1/domains/check's
// hostname (the ingress rule), wrapped in ErrIngressInvalid. The handler
// runs it before any DB query.
func ValidateCheckDomain(domain string) error {
	probe := model.Ingress{Domains: []model.IngressDomain{{Domain: domain, UpstreamPort: 1}}}
	if err := probe.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
	}
	return nil
}

// CheckDomain reports whether a hostname (already validated with
// ValidateCheckDomain) is free and covered on serverID — the live-typing
// check behind GET /api/v1/domains/check. A hostname is unique per server
// only.
func (uc *UseCase) CheckDomain(ctx context.Context, serverID, domain, excludeAppID string) (DomainCheck, error) {
	var out DomainCheck
	if uc.serverDomains != nil {
		regs, err := uc.serverDomains.ListForServer(ctx, serverID)
		if err != nil {
			return DomainCheck{}, err
		}
		if win, ok := model.Winning(regs, domain); ok {
			out.Registration = &win
		}
	}
	if uc.domains != nil {
		claims, err := uc.domains.FindClaims(ctx, serverID, []string{domain}, excludeAppID)
		if err != nil {
			return DomainCheck{}, err
		}
		out.Claims = claims
	}
	return out, nil
}

// requireCovered refuses an ingress hostname no domain registered on
// serverID covers (the agent drops uncovered hosts again in BuildConfig).
// Hostnames appID already holds are grandfathered: the registration index
// may still be empty right after an upgrade until the agent's first
// domain.list, and domain.delete is refused while an app uses a covered
// hostname. They are looked up only once a host turns out uncovered.
func (uc *UseCase) requireCovered(ctx context.Context, serverID, appID string, ing *model.Ingress) error {
	if uc.serverDomains == nil {
		return nil
	}
	regs, err := uc.serverDomains.ListForServer(ctx, serverID)
	if err != nil {
		uc.log.Error("SaveApp: list registered domains", "error", err, "server_id", serverID)
		return err
	}
	var held map[string]bool
	for _, host := range ing.DomainNames() {
		if _, ok := model.Winning(regs, host); ok {
			continue
		}
		if held == nil {
			if held, err = uc.heldHostnames(ctx, serverID, appID); err != nil {
				uc.log.Error("SaveApp: list held hostnames", "error", err, "app_id", appID)
				return err
			}
		}
		if !held[host] {
			return model.NotCoveredError(host)
		}
	}
	return nil
}

// heldHostnames is the set of hostnames appID holds on serverID per the
// app_domains index (empty for a new app or without the index).
func (uc *UseCase) heldHostnames(ctx context.Context, serverID, appID string) (map[string]bool, error) {
	held := map[string]bool{}
	if uc.domains == nil {
		return held, nil
	}
	hosts, err := uc.domains.ListForApp(ctx, serverID, appID)
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		held[h] = true
	}
	return held, nil
}

// Observer is a caller's extra hook on a dispatched command's result: it
// runs after the usecase's own result handling (the DB is updated by
// then). It must not block.
type Observer func(port.CommandResult)

// ObservedResultTTL is how long the dispatcher keeps the result of a command
// that has an observer: its caller may hold the request for the handler's
// wait bound (10 minutes) and the observer must still run after it, while the
// dispatcher's default (10 minutes) would drop a late result unseen. It is
// the wait bound plus 5 minutes of slack.
const ObservedResultTTL = 15 * time.Minute

// CheckedSave is an app.save that passed every save-app check, ready to
// dispatch.
type CheckedSave struct {
	serverID string
	app      model.App
	req      command.SaveAppRequest
	ing      *model.Ingress
}

// AppID is the id the save addresses (assigned when the app was new).
func (s CheckedSave) AppID() string { return s.app.ID }

// SaveApp dispatches an app.save command to the server's agent and returns
// the request id immediately. This call does not block. It is an upsert:
// an empty app.ID means create (the API assigns identity), a present one
// means edit. payload carries the app's config blob plus its files and
// variables (secrets pre-encrypted by the browser); app is the catalog record
// persisted to the DB on success. When the agent confirms the save, the
// OnResult hook persists the app and the result is delivered to the user
// over SSE. An app a live template instance manages is refused: it changes
// through its instance (the instance routes use CheckSave and DispatchSave).
func (uc *UseCase) SaveApp(ctx context.Context, userID, serverID string, app model.App, payload command.AppPayload, draft bool) (string, error) {
	if err := uc.requireUnmanaged(ctx, serverID, app.ID); err != nil {
		return "", err
	}
	checked, err := uc.CheckSave(ctx, serverID, app, payload, draft)
	if err != nil {
		return "", err
	}
	return uc.DispatchSave(ctx, userID, checked, nil)
}

// requireUnmanaged refuses an app a live template instance manages
// (model.ManagedByTemplate, a conflict).
func (uc *UseCase) requireUnmanaged(ctx context.Context, serverID, appID string) error {
	if uc.instances == nil || appID == "" {
		return nil
	}
	name, ok, err := uc.instances.LiveInstanceForApp(ctx, serverID, appID)
	if err != nil {
		uc.log.Error("look up the app's template instance", "error", err, "app_id", appID)
		return err
	}
	if ok {
		return model.ManagedByTemplate(name)
	}
	return nil
}

// CheckSave runs save-app's checks (reserved variables, ingress syntax,
// domain coverage and claims, storage and backup references) without
// dispatching anything: a template instance claims its key only once its
// rendered app passes them. An empty app.ID gets a new id.
func (uc *UseCase) CheckSave(ctx context.Context, serverID string, app model.App, payload command.AppPayload, draft bool) (CheckedSave, error) {
	// New apps have no id yet; the API owns identity, so assign one here and use
	// it both on the wire (the agent keys its on-disk storage by app id) and on
	// the persisted catalog record.
	isNew := app.ID == ""
	if isNew {
		app.ID = util.GenerateID()
	}
	payload.AppID = app.ID
	if len(payload.Config) == 0 {
		// Fall back to the catalog record as the config blob when the caller
		// didn't supply a richer config (keeps older callers working).
		cfgBytes, _ := json.Marshal(app)
		payload.Config = cfgBytes
	}

	// The agent runs the same Validate; here a reserved variable name is a
	// synchronous 400 instead of an async failure.
	req := command.SaveAppRequest{App: payload, Draft: draft}
	if err := req.Validate(); err != nil {
		return CheckedSave{}, err
	}

	// Ingress rides the config blob. Parse + validate + conflict-check here so
	// a bad config is a synchronous 400, not an async agent failure. ing==nil
	// (no "ingress" key) means the feature is untouched: skip everything.
	ing, err := model.ParseIngress(payload.Config)
	if err != nil {
		return CheckedSave{}, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
	}
	if ing != nil {
		if err := ing.Validate(); err != nil {
			return CheckedSave{}, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
		}
		if err := uc.requireCovered(ctx, serverID, app.ID, ing); err != nil {
			return CheckedSave{}, err
		}
		if uc.domains != nil {
			claims, err := uc.domains.FindClaims(ctx, serverID, ing.DomainNames(), app.ID)
			if err != nil {
				uc.log.Error("SaveApp: find domain claims", "error", err, "app_id", app.ID)
				return CheckedSave{}, err
			}
			if len(claims) > 0 {
				var parts []string
				for _, c := range claims {
					parts = append(parts, fmt.Sprintf("%s is already used by app %q on this server", c.Domain, c.AppName))
				}
				return CheckedSave{}, fmt.Errorf("%w: %s", model.ErrDomainTaken, strings.Join(parts, "; "))
			}
		}
	}

	if err := uc.requireStorageRefs(ctx, serverID, payload, isNew); err != nil {
		return CheckedSave{}, err
	}
	return CheckedSave{serverID: serverID, app: app, req: req, ing: ing}, nil
}

// DispatchSave dispatches a checked save. On the agent's confirmation the
// catalog record and the domain index are updated, then observe (if any)
// runs.
func (uc *UseCase) DispatchSave(ctx context.Context, userID string, s CheckedSave, observe Observer) (string, error) {
	serverID, app, ing := s.serverID, s.app, s.ing
	in := port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppSave,
		Payload: s.req,
		OnResult: func(res port.CommandResult) {
			defer notify(observe, res)
			if !res.Success {
				return
			}
			var saved command.SaveAppResponse
			if len(res.Payload) > 0 {
				if err := json.Unmarshal(res.Payload, &saved); err != nil {
					uc.log.Error("SaveApp: decode result", "error", err)
					return
				}
			}
			persisted := app
			persisted.ServerID = serverID
			if saved.AppID != "" {
				persisted.ID = saved.AppID
			}
			if err := uc.repo.SaveApp(context.Background(), persisted); err != nil {
				uc.log.Error("SaveApp: persist", "error", err, "app_id", persisted.ID)
			}
			if uc.domains != nil && ing != nil {
				if err := uc.domains.ReplaceForApp(context.Background(), persisted.ID, serverID, ing); err != nil {
					// Index only: reconcile heals it on the next apps.list.
					uc.log.Error("SaveApp: update domain index", "error", err, "app_id", persisted.ID)
				}
			}
		},
	}
	if observe != nil {
		in.Wait = ObservedResultTTL
	}
	return uc.dispatcher.Dispatch(ctx, in)
}

// notify runs observe, when set, with res.
func notify(observe Observer, res port.CommandResult) {
	if observe != nil {
		observe(res)
	}
}
