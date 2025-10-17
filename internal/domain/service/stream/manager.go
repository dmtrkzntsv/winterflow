package stream

import (
	"encoding/json"
	"sync"
	"winterflow/internal/domain/model"
)

type Manager struct {
	mu       sync.Mutex
	sessions map[string][]chan string
}

func NewStreamManager() *Manager {
	return &Manager{
		sessions: make(map[string][]chan string),
	}
}

func (b *Manager) AddSession(userID string) chan string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan string, 1)
	b.sessions[userID] = append(b.sessions[userID], ch)
	return ch
}

func (b *Manager) RemoveSession(userID string, ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if chans, ok := b.sessions[userID]; ok {
		for i, c := range chans {
			if c == ch {
				chans = append(chans[:i], chans[i+1:]...)
				close(c)
				break
			}
		}
		if len(chans) == 0 {
			delete(b.sessions, userID)
		} else {
			b.sessions[userID] = chans
		}
	}
}

func (b *Manager) Publish(userID string, msg model.StreamMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	msgBytes, _ := json.Marshal(msg)
	data := string(msgBytes)
	if chans, ok := b.sessions[userID]; ok {
		for _, ch := range chans {
			select {
			case ch <- data:
			default:
			}
		}
	}
}
