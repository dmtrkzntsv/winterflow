// Package agent is the agent-side application layer: it receives command
// envelopes from the hub (via the gRPC transport) and executes them against the
// orchestrator. It implements the transport's Dispatcher interface, keeping the
// transport free of business logic.
package agent

import (
	"context"

	"winterflow/internal/domain/command"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/codec"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/logger"
)

// Dispatcher routes decoded commands to the Docker Compose orchestrator and
// encodes the typed result back into a ResponseEnvelope.
type Dispatcher struct {
	orch *dockercompose.Repository
	log  *logger.Logger
}

func NewDispatcher(orch *dockercompose.Repository, log *logger.Logger) *Dispatcher {
	return &Dispatcher{orch: orch, log: log}
}

// Dispatch decodes the request payload by its command type, runs the matching
// handler, and returns the response envelope. Decode/handler errors become an
// error ResponseEnvelope so the caller always gets a correlated reply.
func (d *Dispatcher) Dispatch(ctx context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	agentID := ""
	if req.Base != nil {
		agentID = req.Base.AgentId
	}

	switch command.Type(req.Type) {
	case command.TypeAppSave:
		return d.handleSaveApp(ctx, agentID, req)
	case command.TypeAppsList:
		return d.handleListApps(ctx, agentID, req)
	case command.TypeAppsStatus:
		return d.handleAppsStatus(ctx, agentID, req)
	case command.TypeAppGet:
		return d.handleGetApp(ctx, agentID, req)
	case command.TypeAppControl:
		return d.handleControlApp(ctx, agentID, req)
	case command.TypeAppDelete:
		return d.handleDeleteApp(ctx, agentID, req)
	case command.TypeAppRename:
		return d.handleRenameApp(ctx, agentID, req)
	case command.TypeAppLogs:
		return d.handleGetLogs(ctx, agentID, req)
	case command.TypeRegistryList:
		return d.handleListRegistries(ctx, agentID, req)
	case command.TypeRegistryCreate:
		return d.handleCreateRegistry(ctx, agentID, req)
	case command.TypeRegistryDelete:
		return d.handleDeleteRegistry(ctx, agentID, req)
	case command.TypeNetworkList:
		return d.handleListNetworks(ctx, agentID, req)
	case command.TypeNetworkCreate:
		return d.handleCreateNetwork(ctx, agentID, req)
	case command.TypeNetworkDelete:
		return d.handleDeleteNetwork(ctx, agentID, req)
	case command.TypeAgentUpdate:
		return d.handleUpdateAgent(ctx, agentID, req)
	default:
		d.log.Warn("unhandled command type", "type", req.Type)
		return d.errResponse(agentID, req, "unsupported command type: "+req.Type)
	}
}

func (d *Dispatcher) handleSaveApp(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.SaveAppRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}

	rev, err := d.orch.SaveApp(ctx, in.App)
	if err != nil {
		d.log.Error("save app failed", "app_id", in.App.AppID, "error", err)
		return d.errResponse(agentID, req, err.Error())
	}

	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.TypeAppSave,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "app saved",
		command.SaveAppResponse{AppID: in.App.AppID, Revision: rev})
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}

func (d *Dispatcher) handleAppsStatus(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	apps, err := d.orch.GetAppsStatus(ctx)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.TypeAppsStatus,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok",
		command.GetAppsStatusResponse{Apps: apps})
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}

func (d *Dispatcher) handleListApps(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	apps, err := d.orch.ListApps(ctx)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.TypeAppsList,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok",
		command.ListAppsResponse{Apps: apps})
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}

func (d *Dispatcher) handleGetApp(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.GetAppRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}
	res, err := d.orch.GetApp(ctx, in.AppID, in.Revision)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.TypeAppGet,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", res)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}

func (d *Dispatcher) handleControlApp(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.ControlAppRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}

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
		return d.errResponse(agentID, req, "unknown control action: "+string(in.Action))
	}
	if err != nil {
		d.log.Error("control app failed", "app_id", in.AppID, "action", in.Action, "error", err)
		return d.errResponse(agentID, req, err.Error())
	}

	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.TypeAppControl,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok",
		command.ControlAppResponse{AppID: in.AppID, Action: in.Action})
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}

func (d *Dispatcher) handleDeleteApp(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.DeleteAppRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}
	if err := d.orch.DeleteApp(ctx, in.AppID); err != nil {
		d.log.Error("delete app failed", "app_id", in.AppID, "error", err)
		return d.errResponse(agentID, req, err.Error())
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.TypeAppDelete,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok",
		command.DeleteAppResponse{AppID: in.AppID})
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}

func (d *Dispatcher) handleRenameApp(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.RenameAppRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}
	if err := d.orch.RenameApp(ctx, in.AppID, in.Name); err != nil {
		d.log.Error("rename app failed", "app_id", in.AppID, "error", err)
		return d.errResponse(agentID, req, err.Error())
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.TypeAppRename,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok",
		command.RenameAppResponse{AppID: in.AppID, Name: in.Name})
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}

func (d *Dispatcher) handleGetLogs(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.GetLogsRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}
	res, err := d.orch.GetLogs(ctx, in)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, command.TypeAppLogs,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", res)
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
