package port

import (
	"context"
	"winterflow/internal/domain/command"
)

// CommandDispatcher sends a typed command to an agent and returns immediately
// with a correlation id. The command is fire-and-forward: the agent's result
// is delivered asynchronously to the originating user over SSE (the dispatcher
// records request_id → userID so the bus response subscriber can route it).
//
// This replaces the old request/reply (reply.Manager) wait — handlers respond
// 202 with the returned requestID and never block on the agent.
type CommandDispatcher interface {
	Dispatch(ctx context.Context, in DispatchInput) (requestID string, err error)
}

// DispatchInput is one command to send to an agent on behalf of a user.
type DispatchInput struct {
	AgentID string       // target server/agent id
	UserID  string       // owner of the request; results route back here over SSE
	Type    command.Type // command type
	Payload any          // JSON-serializable payload struct
}
