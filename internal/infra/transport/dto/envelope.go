package dto

type RequestEnvelopeDTO struct {
	AgentID   string      `json:"agent_id"`
	RequestID string      `json:"request_id"`
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp int64       `json:"timestamp"`
}

type ResponseEnvelopeDTO struct {
	AgentID   string `json:"agent_id"`
	RequestID string `json:"request_id"`
	Type      string `json:"type"`
	Payload   []byte `json:"payload"`
	Timestamp int64  `json:"timestamp"`
}
