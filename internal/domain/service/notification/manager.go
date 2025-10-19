package notification

import (
	"encoding/json"
	"sync"
	"winterflow/internal/domain/model"
)

type Manager struct {
	mu       sync.Mutex
	channels map[string][]chan string
}

func NewNotificationManager() *Manager {
	return &Manager{
		channels: make(map[string][]chan string),
	}
}

func (b *Manager) AddChannel(userID string) chan string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan string, 1)
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
			}
		}
	}
}
