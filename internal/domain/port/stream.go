package port

import "winterflow/internal/domain/model"

type StreamManager interface {
	AddSession(userID string) chan string
	RemoveSession(userID string, ch chan string)
	Publish(userID string, msg model.StreamMessage)
}
