package app

import (
	"context"
	"encoding/json"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// ListServerDomains dispatches domain.list to the server's agent. The result
// reconciles the server_domains index and is delivered over SSE.
func (uc *UseCase) ListServerDomains(ctx context.Context, userID, serverID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeDomainList,
		Payload: command.ListDomainsRequest{},
		OnResult: func(res port.CommandResult) {
			if !res.Success || len(res.Payload) == 0 || uc.serverDomains == nil {
				return
			}
			var listed command.ListDomainsResponse
			if err := json.Unmarshal(res.Payload, &listed); err != nil {
				uc.log.Error("ListServerDomains: decode result", "error", err)
				return
			}
			rows := make([]model.RegisteredDomain, 0, len(listed.Domains))
			for _, d := range listed.Domains {
				rows = append(rows, d.RegisteredDomain)
			}
			if err := uc.serverDomains.ReplaceForServer(context.Background(), serverID, rows); err != nil {
				uc.log.Error("ListServerDomains: sync index", "error", err, "server_id", serverID)
			}
		},
	})
}

// CreateDomain dispatches domain.create (callers validate req first). On
// the agent's confirmation the registration enters the index.
func (uc *UseCase) CreateDomain(ctx context.Context, userID, serverID string, req command.CreateDomainRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeDomainCreate,
		Payload: req,
		OnResult: func(res port.CommandResult) {
			if !res.Success || len(res.Payload) == 0 || uc.serverDomains == nil {
				return
			}
			var created command.CreateDomainResponse
			if err := json.Unmarshal(res.Payload, &created); err != nil {
				uc.log.Error("CreateDomain: decode result", "error", err)
				return
			}
			if err := uc.serverDomains.Upsert(context.Background(), serverID, created.Domain.RegisteredDomain); err != nil {
				uc.log.Error("CreateDomain: update index", "error", err, "server_id", serverID)
			}
		},
	})
}

// DeleteDomain dispatches domain.delete. On the agent's confirmation the
// registration leaves the index; a refusal (in use) changes nothing.
func (uc *UseCase) DeleteDomain(ctx context.Context, userID, serverID, name string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeDomainDelete,
		Payload: command.DeleteDomainRequest{Name: name},
		OnResult: func(res port.CommandResult) {
			if !res.Success || uc.serverDomains == nil {
				return
			}
			if err := uc.serverDomains.Delete(context.Background(), serverID, name); err != nil {
				uc.log.Error("DeleteDomain: update index", "error", err, "server_id", serverID)
			}
		},
	})
}
