// Package helper is the contract between the agent and its backup helper
// container: the image, the labels, and how the `backup` subcommand running
// inside reports (exit codes, and a JSON Result as stderr's last line).
package helper

import (
	"bytes"
	"encoding/json"
)

// Image is the helper's image: hard-coded, never configurable.
const Image = "alpine:3.20"

// Labels on every helper container; RoleLabel finds leftovers.
const (
	RoleLabel = "winterflow.role=backup-helper"
	AppLabel  = "winterflow.app"
)

// Exit codes of the `backup` subcommand.
const (
	ExitOK        = 0
	ExitFailed    = 1
	ExitUsage     = 2
	ExitUnchanged = 3 // the source matches --since: nothing written
	ExitMissing   = 4 // nothing to back up: the database file or every path is missing
	ExitPartial   = 5 // a restore failed while swapping and could not undo it: a files restore's paths are partly replaced, a sqlite restore's old -wal/-journal stay set aside
)

// Result is the JSON line a dump ends stderr with.
type Result struct {
	Unchanged   bool     `json:"unchanged,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	RawSize     int64    `json:"raw_size,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// ParseResult reads the Result from a helper's stderr: its last non-empty
// line. ok is false when that line is not a Result (docker failed before
// the helper ran).
func ParseResult(stderr []byte) (Result, bool) {
	lines := bytes.Split(bytes.TrimSpace(stderr), []byte("\n"))
	last := bytes.TrimSpace(lines[len(lines)-1])
	var res Result
	if len(last) == 0 || last[0] != '{' || json.Unmarshal(last, &res) != nil {
		return Result{}, false
	}
	return res, true
}
