package agent

import (
	"context"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/codec"
	"winterflow/internal/infra/transport/grpc/proto"
)

// Docker resource handlers (registries + networks). Each follows the standard
// shape: decode payload, run the orchestrator op, encode the typed response.

func (d *Dispatcher) handleListRegistries(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	regs, err := d.orch.ListRegistries(ctx)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return d.ok(agentID, req, command.TypeRegistryList, command.ListRegistriesResponse{Registries: regs})
}

func (d *Dispatcher) handleCreateRegistry(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.CreateRegistryRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}
	if err := d.orch.CreateRegistry(ctx, in); err != nil {
		d.log.Error("create registry failed", "address", in.Address, "error", err)
		return d.errResponse(agentID, req, err.Error())
	}
	return d.ok(agentID, req, command.TypeRegistryCreate, command.CreateRegistryResponse{Address: in.Address})
}

func (d *Dispatcher) handleDeleteRegistry(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.DeleteRegistryRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}
	if err := d.orch.DeleteRegistry(ctx, in.Address); err != nil {
		d.log.Error("delete registry failed", "address", in.Address, "error", err)
		return d.errResponse(agentID, req, err.Error())
	}
	return d.ok(agentID, req, command.TypeRegistryDelete, command.DeleteRegistryResponse{Address: in.Address})
}

func (d *Dispatcher) handleListNetworks(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	nets, err := d.orch.ListNetworks(ctx)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return d.ok(agentID, req, command.TypeNetworkList, command.ListNetworksResponse{Networks: nets})
}

func (d *Dispatcher) handleCreateNetwork(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.CreateNetworkRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}
	if err := d.orch.CreateNetwork(ctx, in); err != nil {
		d.log.Error("create network failed", "name", in.Name, "error", err)
		return d.errResponse(agentID, req, err.Error())
	}
	return d.ok(agentID, req, command.TypeNetworkCreate, command.CreateNetworkResponse{Name: in.Name})
}

func (d *Dispatcher) handleDeleteNetwork(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	var in command.DeleteNetworkRequest
	if err := codec.DecodePayload(req.Payload, &in); err != nil {
		return d.errResponse(agentID, req, "invalid payload: "+err.Error())
	}
	if err := d.orch.DeleteNetwork(ctx, in.Name); err != nil {
		d.log.Error("delete network failed", "name", in.Name, "error", err)
		return d.errResponse(agentID, req, err.Error())
	}
	return d.ok(agentID, req, command.TypeNetworkDelete, command.DeleteNetworkResponse{Name: in.Name})
}

// ok encodes a success response, falling back to an error envelope on a marshal
// failure. A small helper to keep the docker handlers terse.
func (d *Dispatcher) ok(agentID string, req *proto.RequestEnvelope, typ command.Type, payload any) *proto.ResponseEnvelope {
	resp, err := codec.EncodeResponse(agentID, req.RequestId, typ,
		proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", payload)
	if err != nil {
		return d.errResponse(agentID, req, err.Error())
	}
	return resp
}
