package app

import (
	"context"
	"encoding/json"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// ServerDomains reads the server's registrations from the server_domains
// index (no agent round-trip).
func (uc *UseCase) ServerDomains(ctx context.Context, serverID string) ([]model.RegisteredDomain, error) {
	if uc.serverDomains == nil {
		return []model.RegisteredDomain{}, nil
	}
	return uc.serverDomains.ListForServer(ctx, serverID)
}

// ListServerDomains dispatches domain.list to the server's agent. The result
// reconciles the server_domains index and is delivered over SSE to userID
// (none for the system's own sync when an agent comes online).
func (uc *UseCase) ListServerDomains(ctx context.Context, userID, serverID string) (string, error) {
	return uc.ListServerDomainsObserved(ctx, userID, serverID, nil)
}

// ListServerDomainsObserved is ListServerDomains with an observer on the
// result (it runs after the index is reconciled).
func (uc *UseCase) ListServerDomainsObserved(ctx context.Context, userID, serverID string, observe Observer) (string, error) {
	in := port.DispatchInput{
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
	}
	Observe(&in, observe)
	return uc.dispatcher.Dispatch(ctx, in)
}

// CreateDomain dispatches domain.create (callers validate req first). On
// the agent's confirmation the registration enters the index.
func (uc *UseCase) CreateDomain(ctx context.Context, userID, serverID string, req command.CreateDomainRequest) (string, error) {
	return uc.CreateDomainObserved(ctx, userID, serverID, req, nil)
}

// CreateDomainObserved is CreateDomain with an observer on the result (it
// runs after the index is updated).
func (uc *UseCase) CreateDomainObserved(ctx context.Context, userID, serverID string, req command.CreateDomainRequest, observe Observer) (string, error) {
	in := port.DispatchInput{
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
	}
	Observe(&in, observe)
	return uc.dispatcher.Dispatch(ctx, in)
}

// DeleteDomain dispatches domain.delete. On the agent's confirmation the
// registration leaves the index; a refusal (in use) changes nothing.
func (uc *UseCase) DeleteDomain(ctx context.Context, userID, serverID, name string) (string, error) {
	return uc.DeleteDomainObserved(ctx, userID, serverID, name, nil)
}

// DeleteDomainObserved is DeleteDomain with an observer on the result (it
// runs after the index is updated).
func (uc *UseCase) DeleteDomainObserved(ctx context.Context, userID, serverID, name string, observe Observer) (string, error) {
	in := port.DispatchInput{
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
	}
	Observe(&in, observe)
	return uc.dispatcher.Dispatch(ctx, in)
}
