package model

import "time"

type StreamMessageType string

const (
	StreamMessageOperationResult StreamMessageType = "operation_result"
)

type StreamMessageStatus int

const (
	StreamMessageStatusError   StreamMessageStatus = -1
	StreamMessageStatusUnknown StreamMessageStatus = 0
	StreamMessageStatusSuccess StreamMessageStatus = 1
)

type StreamMessage struct {
	Type      StreamMessageType   `json:"type"`
	Ref       string              `json:"ref"`
	Status    StreamMessageStatus `json:"status,omitempty"`
	Payload   interface{}         `json:"payload,omitempty"`
	Timestamp time.Time           `json:"timestamp"`
}
