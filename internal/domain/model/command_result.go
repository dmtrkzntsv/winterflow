package model

import (
	"encoding/json"
	"time"
)

// CommandStatus is where a recorded command stands.
type CommandStatus string

const (
	// CommandRunning: dispatched, no result yet.
	CommandRunning CommandStatus = "running"
	// CommandSucceeded: the agent reported success.
	CommandSucceeded CommandStatus = "succeeded"
	// CommandFailed: the agent reported a failure (Error says why).
	CommandFailed CommandStatus = "failed"
	// CommandUnknown: no result was recorded in time (the replica that
	// dispatched it went away, or the agent never answered).
	CommandUnknown CommandStatus = "unknown"
)

// CommandRecord is a command an MCP tool dispatched and its outcome.
type CommandRecord struct {
	RequestID string
	OrgID     string
	ServerID  string
	// Kind is the command type (command.Type), e.g. "app.save".
	Kind   string
	Status CommandStatus
	// Result is the agent's response JSON (nil when there is none); at
	// most 1 MiB, with "truncated": true when it had to be cut.
	Result     json.RawMessage
	Error      string
	CreatedAt  time.Time
	FinishedAt *time.Time
}
