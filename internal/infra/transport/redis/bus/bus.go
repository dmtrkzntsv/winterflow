package redisbus

import (
	"context"
	"encoding/json"
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
				b.log.Error("failed to receive bus message", err)
				return
			}
			out <- bus.Message{Channel: msg.Channel, Payload: msg.Payload}
		}
	}()

	cancel := func() error {
		b.log.Info("closing bus subscription")
		return pubsub.Close()
	}
	return out, cancel, nil
}
