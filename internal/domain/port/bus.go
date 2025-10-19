package port

import "context"

type Message struct {
	Channel string
	Data    []byte
}

type Bus interface {
	Publish(ctx context.Context, channel string, v any) error
	Subscribe(ctx context.Context, channels ...string) (<-chan Message, func() error, error)
}
