package notification

import (
	"encoding/json"
	"sync"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

// streamBuffer is the per-stream channel capacity. Notifications arrive in
// bursts (an apps_status fan-out, its server_status transition and an
// operation_result can land back to back) while the SSE handler is still
// writing the previous one; a small buffer absorbs the burst. It is bounded
// because Publish holds the manager lock and must never block on a stuck
// client: 64 slots cost 1 KiB of string headers per open stream, plus only
// the payloads actually queued, so a handful of browser tabs on a small
// home server stays negligible. Beyond it, notifications are dropped.
const streamBuffer = 64

type Manager struct {
	mu       sync.Mutex
	channels map[string][]chan string
	log      *logger.Logger
}

func NewNotificationManager(log *logger.Logger) *Manager {
	return &Manager{
		channels: make(map[string][]chan string),
		log:      log,
	}
}

func (b *Manager) AddChannel(userID string) chan string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan string, streamBuffer)
	b.channels[userID] = append(b.channels[userID], ch)
	return ch
}

func (b *Manager) RemoveChannel(userID string, ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if chans, ok := b.channels[userID]; ok {
		for i, c := range chans {
			if c == ch {
				chans = append(chans[:i], chans[i+1:]...)
				close(c)
				break
			}
		}
		if len(chans) == 0 {
			delete(b.channels, userID)
		} else {
			b.channels[userID] = chans
		}
	}
}

// Publish queues msg on every stream of userID without blocking: a stream
// whose buffer is full (a stuck or very slow client) misses it.
func (b *Manager) Publish(userID string, msg model.Notification) {
	b.mu.Lock()
	defer b.mu.Unlock()
	msgBytes, _ := json.Marshal(msg)
	data := string(msgBytes)
	if chans, ok := b.channels[userID]; ok {
		for _, ch := range chans {
			select {
			case ch <- data:
			default:
				b.log.Debug("notification dropped: stream buffer full",
					"user_id", userID, "type", msg.Type, "ref", msg.Ref, "buffer", streamBuffer)
			}
		}
	}
}
