// Package membus is an in-process implementation of bus.Bus for the standalone
// topology, where the API and the agent run in the same process and a real
// Redis would be unnecessary. Channels are matched exactly (no patterns), which
// is all the request/response queues need.
package membus

import (
	"context"
	"encoding/json"
	"sync"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

type Bus struct {
	mu   sync.RWMutex
	subs map[string][]chan bus.Message
	log  *logger.Logger
}

func NewBus(log *logger.Logger) *Bus {
	return &Bus{
		subs: make(map[string][]chan bus.Message),
		log:  log,
	}
}

func (b *Bus) Publish(ctx context.Context, channel string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		b.log.Error("failed to marshal bus message", err, "channel", channel)
		return err
	}
	msg := bus.Message{Channel: channel, Payload: string(data)}

	b.mu.RLock()
	subs := append([]chan bus.Message(nil), b.subs[channel]...)
	b.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
			b.log.Warn("dropping bus message, subscriber slow", "channel", channel)
		}
	}
	return nil
}

func (b *Bus) Subscribe(ctx context.Context, channels ...string) (<-chan bus.Message, func() error, error) {
	out := make(chan bus.Message, 64)

	b.mu.Lock()
	for _, c := range channels {
		b.subs[c] = append(b.subs[c], out)
	}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() error {
		once.Do(func() {
			b.mu.Lock()
			for _, c := range channels {
				subs := b.subs[c]
				for i, ch := range subs {
					if ch == out {
						b.subs[c] = append(subs[:i], subs[i+1:]...)
						break
					}
				}
			}
			b.mu.Unlock()
			close(out)
		})
		return nil
	}

	// Honor context cancellation.
	go func() {
		<-ctx.Done()
		_ = cancel()
	}()

	return out, cancel, nil
}
