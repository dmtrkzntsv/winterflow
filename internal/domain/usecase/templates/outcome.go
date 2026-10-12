package templates

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
)

// recordTimeout bounds the DB write a command's result makes.
const recordTimeout = 30 * time.Second

// Outcome is a dispatched instance command: RequestID names its SSE
// result and Done delivers the result once, after the DB is updated
// (buffered: the result hook never waits on a caller that stopped
// listening). When nothing was dispatched (a create replayed onto a
// created instance, a delete of a deleted one) Instance is set instead.
type Outcome struct {
	RequestID string
	Done      <-chan port.CommandResult
	Instance  *InstanceView
	// Final reads the instance once Done delivered a success (an unlinked
	// one can no longer be read by its key).
	Final func(ctx context.Context) (InstanceView, error)
}

// observe makes the observer the app usecase calls with inst's result, and
// the channel it then delivers the result on. A failure records the
// agent's error; a success runs onSuccess (the DB side of the command), or
// with onSuccess nil records the result's error (empty: clearing it).
func (uc *UseCase) observe(inst model.Instance, onSuccess func(ctx context.Context) error) (appuc.Observer, <-chan port.CommandResult) {
	done := make(chan port.CommandResult, 1)
	id, key := inst.ID, inst.Key // not inst: the hook may live 15 minutes
	return func(res port.CommandResult) {
		ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
		defer cancel()
		var err error
		if !res.Success || onSuccess == nil {
			err = uc.repo.SetError(ctx, id, res.Error)
		} else {
			err = onSuccess(ctx)
		}
		if err != nil {
			uc.log.Error("instance: record a result", "error", err, "instance_id", id, "key", key)
		}
		select {
		case done <- res:
		default:
		}
	}, done
}

// outcome is a dispatched command's Outcome, or the dispatch's error.
func outcome(requestID string, err error, done <-chan port.CommandResult, final func(ctx context.Context) (InstanceView, error)) (Outcome, error) {
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{RequestID: requestID, Done: done, Final: final}, nil
}
