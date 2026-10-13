// Package agent is the agent-side application layer: it receives command
// envelopes from the hub (via the gRPC transport) and executes them against the
// orchestrator. It implements the transport's Dispatcher interface, keeping the
// transport free of business logic.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/codec"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/logger"
)

// Dispatcher routes decoded commands to the Docker Compose orchestrator and
// encodes the typed result back into a ResponseEnvelope. Adding a command is
// one registration line in newHandlers plus the orchestrator method.
type Dispatcher struct {
	orch    *dockercompose.Repository
	ingress port.IngressManager
	reloads *ReloadWorker
	backups Backups
	runWait time.Duration // bound of a backup.run / backup.restore
	// reloadWait bounds a single-app vars.reload.
	reloadWait time.Duration
	log        *logger.Logger
	handlers   map[command.Type]handlerFunc
}

type handlerFunc func(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope

// NewDispatcher wires the registry. A nil ingress (tests) stands in as
// noIngress: reloads are no-ops and domain commands fail with errNoIngress.
func NewDispatcher(orch *dockercompose.Repository, ingress port.IngressManager, log *logger.Logger) *Dispatcher {
	if ingress == nil {
		ingress = noIngress{}
	}
	d := &Dispatcher{orch: orch, ingress: ingress, backups: noBackups{}, runWait: command.BackupRunWait,
		reloadWait: command.ReloadAppWait, log: log}
	d.handlers = d.newHandlers()
	return d
}

// errNoReloads answers vars.reload on a dispatcher without a reload worker.
var errNoReloads = errors.New("shared variable reload is not available on this agent")

// WithReloads attaches the shared-variable reload worker (production
// wiring); without one vars.reload answers errNoReloads. The worker runs on
// its own goroutine: vars.reload only queues work, so a long reload never
// holds up the dispatcher (standalone drains commands on one goroutine).
func (d *Dispatcher) WithReloads(w *ReloadWorker) *Dispatcher {
	d.reloads = w
	return d
}

// reloadIngress rebuilds the proxy config after a mutation. Warnings are
// returned for responses that carry them (app.save, domain.create/delete)
// and logged either way.
func (d *Dispatcher) reloadIngress(ctx context.Context) []string {
	warnings := d.ingress.Reload(ctx)
	for _, w := range warnings {
		d.log.Warn("ingress", "warning", w)
	}
	return warnings
}

// errNoIngress answers domain commands on a dispatcher built without an
// ingress manager (tests); production always wires one.
var errNoIngress = errors.New("ingress is not available on this agent")

// noIngress is the IngressManager of a dispatcher built without one.
type noIngress struct{}

func (noIngress) Reload(context.Context) []string { return nil }
func (noIngress) Enabled() bool                   { return false }
func (noIngress) ListDomains(context.Context) ([]command.Domain, error) {
	return nil, errNoIngress
}
func (noIngress) CreateDomain(context.Context, command.CreateDomainRequest) (command.Domain, error) {
	return command.Domain{}, errNoIngress
}
func (noIngress) DeleteDomain(context.Context, string) error { return errNoIngress }

// Dispatch decodes the request payload by its command type, runs the matching
// handler, and returns the response envelope. Decode/handler errors become an
// error ResponseEnvelope so the caller always gets a correlated reply.
func (d *Dispatcher) Dispatch(ctx context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	agentID := ""
	if req.Base != nil {
		agentID = req.Base.AgentId
	}
	h, ok := d.handlers[command.Type(req.Type)]
	if !ok {
		d.log.Warn("unhandled command type", "type", req.Type)
		return d.errResponse(agentID, req, "unsupported command type: "+req.Type)
	}
	return h(ctx, agentID, req)
}

// validator is implemented by requests that carry ids the orchestrator turns
// into paths or CLI args (app ids, volume names).
type validator interface{ Validate() error }

// handle adapts a typed orchestrator call into a handlerFunc: decode the
// request into Req, validate it, run fn, encode Resp. All per-command
// boilerplate lives here exactly once — including the agent's single
// fail-closed check on payload ids, so no orchestrator entry point sees an
// unvalidated one.
func handle[Req any, Resp any](d *Dispatcher, fn func(context.Context, Req) (Resp, error)) handlerFunc {
	return func(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
		var in Req
		if err := codec.DecodePayload(req.Payload, &in); err != nil {
			return d.errResponse(agentID, req, "invalid payload: "+err.Error())
		}
		if v, ok := any(in).(validator); ok {
			if err := v.Validate(); err != nil {
				return d.errResponse(agentID, req, "invalid payload: "+err.Error())
			}
		}
		out, err := fn(ctx, in)
		if err != nil {
			d.log.Error("command failed", "type", req.Type, "error", err)
			return d.errResponse(agentID, req, err.Error())
		}
		return d.ok(agentID, req, out)
	}
}

// newHandlers is the command registry: one line per command type.
func (d *Dispatcher) newHandlers() map[command.Type]handlerFunc {
	return map[command.Type]handlerFunc{
		command.TypeAppSave: handle(d, func(ctx context.Context, in command.SaveAppRequest) (command.SaveAppResponse, error) {
			hash, warnings, err := d.orch.SaveAppResult(ctx, in.App, in.Draft)
			resp := command.SaveAppResponse{AppID: in.App.AppID, Revision: hash, Warnings: warnings}
			if err == nil {
				resp.Warnings = append(resp.Warnings, d.reloadIngress(ctx)...)
			}
			d.backups.Reload()
			return resp, err
		}),
		command.TypeAppsList: handle(d, func(ctx context.Context, _ command.ListAppsRequest) (command.ListAppsResponse, error) {
			apps, err := d.orch.ListApps(ctx)
			return command.ListAppsResponse{Apps: apps}, err
		}),
		command.TypeAppsStatus: handle(d, func(ctx context.Context, _ command.GetAppsStatusRequest) (command.GetAppsStatusResponse, error) {
			apps, err := d.orch.GetAppsStatus(ctx)
			rev := d.orch.VarsRevision()
			return command.GetAppsStatusResponse{Apps: d.backups.Decorate(apps, time.Now()), VarsRevision: &rev}, err
		}),
		command.TypeAppGet: handle(d, func(ctx context.Context, in command.GetAppRequest) (command.GetAppResponse, error) {
			return d.orch.GetApp(ctx, in.AppID)
		}),
		command.TypeAppControl: handle(d, func(ctx context.Context, in command.ControlAppRequest) (command.ControlAppResponse, error) {
			var err error
			switch in.Action {
			case command.ControlStart:
				err = d.orch.StartApp(ctx, in.AppID)
			case command.ControlStop:
				err = d.orch.StopApp(ctx, in.AppID)
			case command.ControlRestart:
				err = d.orch.RestartApp(ctx, in.AppID)
			case command.ControlUpdate:
				err = d.orch.UpdateApp(ctx, in.AppID)
			default:
				err = fmt.Errorf("unknown control action %q", in.Action)
			}
			if in.Action == command.ControlUpdate {
				d.backups.Reload() // a git-sourced app's compose file may have moved
			}
			return command.ControlAppResponse{AppID: in.AppID, Action: in.Action}, err
		}),
		command.TypeAppDelete: handle(d, func(ctx context.Context, in command.DeleteAppRequest) (command.DeleteAppResponse, error) {
			d.backups.Forget(in.AppID) // stop a backup of it before its volumes go
			res, err := d.orch.DeleteApp(ctx, in.AppID, in.DeleteVolumes, in.DeleteBackups)
			d.backups.Forget(in.AppID) // and drop what a reload meanwhile read back
			resp := command.DeleteAppResponse{AppID: in.AppID, Warnings: res.Warnings,
				VolumesDeleted: res.VolumesDeleted, BackupsDeleted: res.BackupsDeleted}
			if err != nil {
				// handle drops the body on error, so fold the warnings in.
				if len(res.Warnings) > 0 {
					err = fmt.Errorf("%w (cleanup: %s)", err, strings.Join(res.Warnings, "; "))
				}
				return resp, err
			}
			if d.reloads != nil {
				d.reloads.Forget(in.AppID)
			}
			d.reloadIngress(ctx)
			return resp, nil
		}),
		command.TypeAppRename: handle(d, func(ctx context.Context, in command.RenameAppRequest) (command.RenameAppResponse, error) {
			if err := d.orch.RenameApp(ctx, in.AppID, in.Name); err != nil {
				return command.RenameAppResponse{AppID: in.AppID, Name: in.Name}, err
			}
			d.reloadIngress(ctx)
			d.backups.Reload()
			return command.RenameAppResponse{AppID: in.AppID, Name: in.Name}, nil
		}),
		command.TypeAppLogs: handle(d, func(ctx context.Context, in command.GetLogsRequest) (command.GetLogsResponse, error) {
			return d.orch.GetLogs(ctx, in)
		}),
		command.TypeAppRevisions: handle(d, func(ctx context.Context, in command.GetRevisionsRequest) (command.GetRevisionsResponse, error) {
			revs, current, err := d.orch.Revisions(ctx, in.AppID)
			return command.GetRevisionsResponse{
				AppID:     in.AppID,
				Current:   current,
				Deployed:  d.orch.DeployedRevision(in.AppID),
				Revisions: revs,
			}, err
		}),
		command.TypeAppRollback: handle(d, func(ctx context.Context, in command.RollbackAppRequest) (command.RollbackAppResponse, error) {
			newHead, err := d.orch.Rollback(ctx, in.AppID, in.Hash)
			d.backups.Reload() // the restored revision has its own backups
			if err != nil {
				return command.RollbackAppResponse{AppID: in.AppID, Revision: newHead}, err
			}
			d.reloadIngress(ctx)
			return command.RollbackAppResponse{AppID: in.AppID, Revision: newHead}, nil
		}),

		command.TypeRegistryList: handle(d, func(ctx context.Context, _ command.ListRegistriesRequest) (command.ListRegistriesResponse, error) {
			regs, err := d.orch.ListRegistries(ctx)
			return command.ListRegistriesResponse{Registries: regs}, err
		}),
		command.TypeRegistryCreate: handle(d, func(ctx context.Context, in command.CreateRegistryRequest) (command.CreateRegistryResponse, error) {
			return command.CreateRegistryResponse{Address: in.Address}, d.orch.CreateRegistry(ctx, in)
		}),
		command.TypeRegistryDelete: handle(d, func(ctx context.Context, in command.DeleteRegistryRequest) (command.DeleteRegistryResponse, error) {
			return command.DeleteRegistryResponse{Address: in.Address}, d.orch.DeleteRegistry(ctx, in.Address)
		}),

		command.TypeNetworkList: handle(d, func(ctx context.Context, _ command.ListNetworksRequest) (command.ListNetworksResponse, error) {
			nets, err := d.orch.ListNetworks(ctx)
			return command.ListNetworksResponse{Networks: nets}, err
		}),
		command.TypeNetworkCreate: handle(d, func(ctx context.Context, in command.CreateNetworkRequest) (command.CreateNetworkResponse, error) {
			return command.CreateNetworkResponse{Name: in.Name}, d.orch.CreateNetwork(ctx, in)
		}),
		command.TypeNetworkDelete: handle(d, func(ctx context.Context, in command.DeleteNetworkRequest) (command.DeleteNetworkResponse, error) {
			return command.DeleteNetworkResponse{Name: in.Name}, d.orch.DeleteNetwork(ctx, in.Name)
		}),

		command.TypeVolumeList: handle(d, func(ctx context.Context, _ command.ListVolumesRequest) (command.ListVolumesResponse, error) {
			vols, err := d.orch.ListVolumes(ctx)
			return command.ListVolumesResponse{Volumes: vols}, err
		}),
		command.TypeVolumeCreate: handle(d, func(ctx context.Context, in command.CreateVolumeRequest) (command.CreateVolumeResponse, error) {
			return command.CreateVolumeResponse{Name: in.Name}, d.orch.CreateVolume(ctx, in.Name)
		}),
		command.TypeVolumeDelete: handle(d, func(ctx context.Context, in command.DeleteVolumeRequest) (command.DeleteVolumeResponse, error) {
			return command.DeleteVolumeResponse{Name: in.Name}, d.orch.DeleteVolume(ctx, in.Name)
		}),

		command.TypeDomainList: handle(d, func(ctx context.Context, _ command.ListDomainsRequest) (command.ListDomainsResponse, error) {
			doms, err := d.ingress.ListDomains(ctx)
			return command.ListDomainsResponse{Domains: doms}, err
		}),
		command.TypeDomainCreate: handle(d, func(ctx context.Context, in command.CreateDomainRequest) (command.CreateDomainResponse, error) {
			dom, err := d.ingress.CreateDomain(ctx, in)
			if err != nil {
				return command.CreateDomainResponse{}, err
			}
			return command.CreateDomainResponse{Domain: dom, Warnings: d.reloadIngress(ctx)}, nil
		}),
		command.TypeDomainDelete: handle(d, func(ctx context.Context, in command.DeleteDomainRequest) (command.DeleteDomainResponse, error) {
			if err := d.ingress.DeleteDomain(ctx, in.Name); err != nil {
				return command.DeleteDomainResponse{}, err
			}
			return command.DeleteDomainResponse{Name: in.Name, Warnings: d.reloadIngress(ctx)}, nil
		}),

		command.TypeVarsSync: handle(d, func(ctx context.Context, in command.SyncVarsRequest) (command.SyncVarsResponse, error) {
			return d.orch.SyncVars(ctx, in)
		}),
		command.TypeVarsReload: handle(d, d.reloadVars),
		command.TypeStorageList: handle(d, func(ctx context.Context, in command.ListStoragesRequest) (command.ListStoragesResponse, error) {
			list, err := d.orch.ListStorages(ctx, !in.RegistryOnly)
			return command.ListStoragesResponse{Storages: list}, err
		}),
		command.TypeStorageTest: handle(d, func(ctx context.Context, in command.TestStorageRequest) (command.TestStorageResponse, error) {
			return d.orch.TestStorage(ctx, in.Storage)
		}),
		command.TypeStorageSave: handle(d, func(ctx context.Context, in command.SaveStorageRequest) (command.SaveStorageResponse, error) {
			defer d.backups.Reload()
			return d.orch.SaveStorage(ctx, in.Storage, in.Create)
		}),
		command.TypeStorageDelete: handle(d, func(ctx context.Context, in command.DeleteStorageRequest) (command.DeleteStorageResponse, error) {
			defer d.backups.Reload()
			return command.DeleteStorageResponse{Name: in.Name}, d.orch.DeleteStorage(ctx, in.Name)
		}),
		command.TypeStorageOrphans: handle(d, func(ctx context.Context, in command.ListOrphansRequest) (command.ListOrphansResponse, error) {
			orphans, err := d.orch.ListOrphans(ctx, in.Name)
			return command.ListOrphansResponse{Name: in.Name, Orphans: orphans}, err
		}),
		command.TypeStorageDeleteOrphan: handle(d, func(ctx context.Context, in command.DeleteOrphanRequest) (command.DeleteOrphanResponse, error) {
			n, err := d.orch.DeleteOrphan(ctx, in.Name, in.AppID)
			return command.DeleteOrphanResponse{Name: in.Name, AppID: in.AppID, Deleted: n}, err
		}),

		command.TypeBackupUpdate: handle(d, func(ctx context.Context, in command.UpdateBackupsRequest) (command.UpdateBackupsResponse, error) {
			defer d.backups.Reload()
			return d.orch.UpdateBackups(ctx, in.AppID, in.Backups, in.Base)
		}),
		command.TypeBackupPoints: handle(d, func(ctx context.Context, in command.ListRestorePointsRequest) (command.ListRestorePointsResponse, error) {
			points, err := d.orch.ListRestorePoints(ctx, in.AppID, in.Backup)
			return command.ListRestorePointsResponse{AppID: in.AppID, Backup: in.Backup, Points: points}, err
		}),
		command.TypeBackupRun:     handle(d, d.runBackup),
		command.TypeBackupRestore: handle(d, d.restoreBackup),
		command.TypeBackupDeletePoint: handle(d, func(ctx context.Context, in command.DeleteRestorePointRequest) (command.DeleteRestorePointResponse, error) {
			err := d.orch.DeleteRestorePoint(ctx, in.AppID, in.Backup, in.Point)
			return command.DeleteRestorePointResponse{AppID: in.AppID, Backup: in.Backup, Point: in.Point}, err
		}),

		command.TypeImageTags: handle(d, func(ctx context.Context, in command.ImageTagsRequest) (command.ImageTagsResponse, error) {
			tags, err := d.orch.ImageTags(ctx, in.Image)
			return command.ImageTagsResponse{Image: in.Image, Tags: tags}, err
		}),

		command.TypeAgentUpdate: handle(d, func(ctx context.Context, in command.UpdateAgentRequest) (command.UpdateAgentResponse, error) {
			return d.orch.UpdateAgent(ctx, in)
		}),
	}
}

// ok encodes a success response, falling back to an error envelope on a
// marshal failure.
func (d *Dispatcher) ok(agentID string, req *proto.RequestEnvelope, payload any) *proto.ResponseEnvelope {
	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.Type(req.Type),
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", payload)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}

func (d *Dispatcher) errResponse(agentID string, req *proto.RequestEnvelope, detail string) *proto.ResponseEnvelope {
	resp, _ := codec.EncodeResponse(agentID, req.RequestId, command.Type(req.Type),
		proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR, detail, nil)
	return resp
}

// reloadVars queues a vars.reload. The bulk form answers once queued; the
// single-app form (exactly one app id) answers with that app's outcome, at
// most reloadWait later. vars.reload is a detached command, so the wait
// never holds up the command pipeline.
func (d *Dispatcher) reloadVars(ctx context.Context, in command.ReloadVarsRequest) (command.ReloadVarsResponse, error) {
	if d.reloads == nil {
		return command.ReloadVarsResponse{}, errNoReloads
	}
	if len(in.AppIDs) != 1 {
		d.reloads.Enqueue(in)
		return command.ReloadVarsResponse{Queued: true}, nil
	}
	appID := in.AppIDs[0]
	resp := command.ReloadVarsResponse{Queued: true, AppID: appID}
	ctx, cancel := context.WithTimeout(ctx, d.reloadWait)
	defer cancel()
	select {
	case o := <-d.reloads.EnqueueApp(appID):
		resp.Names, resp.Skipped = o.Names, o.Skipped
		return resp, o.Err
	case <-ctx.Done():
		return resp, fmt.Errorf("the reload of app %s did not end within %s", appID, d.reloadWait)
	}
}
