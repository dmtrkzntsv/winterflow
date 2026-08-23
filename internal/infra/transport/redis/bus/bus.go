package redisbus

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"

	"github.com/redis/go-redis/v9"
)

type Bus struct {
	rdb *redis.Client
	log *logger.Logger
}

func NewBus(rdb *redis.Client, log *logger.Logger) *Bus {
	return &Bus{rdb: rdb, log: log}
}

func (b *Bus) Publish(ctx context.Context, channel string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		b.log.Error("failed to marshal bus message", err, "channel", channel)
		return err
	}

	res := b.rdb.Publish(ctx, channel, data)
	if res.Err() != nil {
		b.log.Error("failed to publish bus message", res.Err(), "channel", channel)
		return res.Err()
	}
	b.log.Info("published bus message", channel)
	return nil
}

func (b *Bus) Subscribe(ctx context.Context, channels ...string) (<-chan bus.Message, func() error, error) {
	pubsub := b.rdb.Subscribe(ctx, channels...)
	out := make(chan bus.Message, 64)

	go func() {
		defer close(out)
		for {
			msg, err := pubsub.ReceiveMessage(ctx)
			if err != nil {
				// Terminal: canceled or the subscription was closed. Anything
				// else (a Redis blip go-redis couldn't absorb) must not kill
				// the consumer for the rest of the process lifetime — back off
				// and retry.
				if ctx.Err() != nil || errors.Is(err, redis.ErrClosed) {
					return
				}
				b.log.Error("failed to receive bus message, retrying", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			// A send must never outlive the consumer: if the reader is gone
			// with a full buffer, blocking here would leak this goroutine.
			select {
			case out <- bus.Message{Channel: msg.Channel, Payload: msg.Payload}:
			case <-ctx.Done():
				return
			}
		}
	}()

	cancel := func() error {
		b.log.Info("closing bus subscription")
		return pubsub.Close()
	}
	return out, cancel, nil
}
