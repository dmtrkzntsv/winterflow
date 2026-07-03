package bus

import (
	"context"
	"encoding/json"

	"winterflow/pkg/logger"
)

// SubscribeJSON subscribes to queue and decodes every message into T, invoking
// handle for each. Malformed payloads are logged and skipped. The consume loop
// runs in its own goroutine until ctx is done; the call itself returns
// immediately. Fatal on subscribe failure, matching the boot-time call sites —
// a topology without its queues is not runnable.
func SubscribeJSON[T any](ctx context.Context, b Bus, queue string, log *logger.Logger, handle func(T)) {
	msgs, cancel, err := b.Subscribe(ctx, queue)
	if err != nil {
		log.Fatalf("failed to subscribe to %s: %v", queue, err)
	}
	go func() {
		defer func() { _ = cancel() }()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgs:
				if !ok {
					return
				}
				var v T
				if err := json.Unmarshal([]byte(msg.Payload), &v); err != nil {
					log.Error("failed to unmarshal bus message", err, "queue", queue)
					continue
				}
				handle(v)
			}
		}
	}()
}
