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
	orch     *dockercompose.Repository
	ingress  port.IngressManager
	log      *logger.Logger
	handlers map[command.Type]handlerFunc
}

type handlerFunc func(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope

// NewDispatcher wires the registry. A nil ingress (tests) stands in as
// noIngress: reloads are no-ops and domain commands fail with errNoIngress.
func NewDispatcher(orch *dockercompose.Repository, ingress port.IngressManager, log *logger.Logger) *Dispatcher {
	if ingress == nil {
		ingress = noIngress{}
	}
	d := &Dispatcher{orch: orch, ingress: ingress, log: log}
	d.handlers = d.newHandlers()
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
			save := d.orch.SaveApp
			if in.Draft {
				save = d.orch.SaveAppDraft
			}
			hash, err := save(ctx, in.App)
			resp := command.SaveAppResponse{AppID: in.App.AppID, Revision: hash}
			if err == nil {
				resp.Warnings = d.reloadIngress(ctx)
			}
			return resp, err
		}),
		command.TypeAppsList: handle(d, func(ctx context.Context, _ command.ListAppsRequest) (command.ListAppsResponse, error) {
			apps, err := d.orch.ListApps(ctx)
			return command.ListAppsResponse{Apps: apps}, err
		}),
		command.TypeAppsStatus: handle(d, func(ctx context.Context, _ command.GetAppsStatusRequest) (command.GetAppsStatusResponse, error) {
			apps, err := d.orch.GetAppsStatus(ctx)
			return command.GetAppsStatusResponse{Apps: apps}, err
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
			return command.ControlAppResponse{AppID: in.AppID, Action: in.Action}, err
		}),
		command.TypeAppDelete: handle(d, func(ctx context.Context, in command.DeleteAppRequest) (command.DeleteAppResponse, error) {
			res, err := d.orch.DeleteApp(ctx, in.AppID, in.DeleteVolumes)
			resp := command.DeleteAppResponse{AppID: in.AppID, Warnings: res.Warnings, VolumesDeleted: res.VolumesDeleted}
			if err != nil {
				// handle drops the body on error, so fold the warnings in.
				if len(res.Warnings) > 0 {
					err = fmt.Errorf("%w (cleanup: %s)", err, strings.Join(res.Warnings, "; "))
				}
				return resp, err
			}
			d.reloadIngress(ctx)
			return resp, nil
		}),
		command.TypeAppRename: handle(d, func(ctx context.Context, in command.RenameAppRequest) (command.RenameAppResponse, error) {
			if err := d.orch.RenameApp(ctx, in.AppID, in.Name); err != nil {
				return command.RenameAppResponse{AppID: in.AppID, Name: in.Name}, err
			}
			d.reloadIngress(ctx)
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

		command.TypeStorageList: handle(d, func(ctx context.Context, in command.ListStoragesRequest) (command.ListStoragesResponse, error) {
			list, err := d.orch.ListStorages(ctx, !in.RegistryOnly)
			return command.ListStoragesResponse{Storages: list}, err
		}),
		command.TypeStorageTest: handle(d, func(ctx context.Context, in command.TestStorageRequest) (command.TestStorageResponse, error) {
			return d.orch.TestStorage(ctx, in.Storage)
		}),
		command.TypeStorageSave: handle(d, func(ctx context.Context, in command.SaveStorageRequest) (command.SaveStorageResponse, error) {
			return d.orch.SaveStorage(ctx, in.Storage, in.Create)
		}),
		command.TypeStorageDelete: handle(d, func(ctx context.Context, in command.DeleteStorageRequest) (command.DeleteStorageResponse, error) {
			return command.DeleteStorageResponse{Name: in.Name}, d.orch.DeleteStorage(ctx, in.Name)
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
