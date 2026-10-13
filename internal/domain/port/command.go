package port

import (
	"context"
	"errors"
	"time"

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

	// OnResult, if set, runs when the agent's result arrives (before SSE
	// delivery). Use it for side effects like persisting the confirmed entity
	// to the DB. It must be quick and non-blocking; it does not affect routing.
	OnResult func(CommandResult)

	// Wait is how long the result is awaited (0: the dispatcher's default):
	// longer for a command whose result comes when long work ends
	// (backup.run).
	Wait time.Duration
}

// CommandResult is the outcome of a dispatched command, passed to OnResult.
type CommandResult struct {
	Success bool
	Error   string
	// Payload is the raw JSON of the agent's typed response (e.g.
	// command.SaveAppResponse); decode with the codec.
	Payload []byte
}

// Err is a failed result's error (nil on success); a failure without a
// message reads "the agent reported a failure".
func (r CommandResult) Err() error {
	switch {
	case r.Success:
		return nil
	case r.Error == "":
		return errors.New("the agent reported a failure")
	}
	return errors.New(r.Error)
}

// NewResultChan is a channel that holds one result and the hook that
// offers a result to it without ever blocking (a second result, or one
// nobody waits for, is dropped): an OnResult for a caller that awaits the
// result.
func NewResultChan() (<-chan CommandResult, func(CommandResult)) {
	ch := make(chan CommandResult, 1)
	return ch, func(res CommandResult) {
		select {
		case ch <- res:
		default:
		}
	}
}

// AwaitResult blocks until ch delivers, bound passes, or ctx ends; ok
// reports a delivered result.
func AwaitResult(ctx context.Context, ch <-chan CommandResult, bound time.Duration) (res CommandResult, ok bool) {
	t := time.NewTimer(bound)
	defer t.Stop()
	select {
	case res := <-ch:
		return res, true
	case <-t.C:
	case <-ctx.Done():
	}
	return CommandResult{}, false
}
