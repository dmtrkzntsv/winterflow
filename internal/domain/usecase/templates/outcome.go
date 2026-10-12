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

// observe wraps record (the DB side of inst's result) into the observer the
// app usecase calls, and the channel it then delivers the result on.
func (uc *UseCase) observe(inst model.Instance, record func(ctx context.Context, res port.CommandResult) error) (appuc.Observer, <-chan port.CommandResult) {
	done := make(chan port.CommandResult, 1)
	id, key := inst.ID, inst.Key // not inst: the hook may live 15 minutes
	return func(res port.CommandResult) {
		ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
		defer cancel()
		if err := record(ctx, res); err != nil {
			uc.log.Error("instance: record a result", "error", err, "instance_id", id, "key", key)
		}
		select {
		case done <- res:
		default:
		}
	}, done
}
