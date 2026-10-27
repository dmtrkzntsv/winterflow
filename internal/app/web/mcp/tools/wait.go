package tools

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/cmdresult"
)

// beginMargin is the time a tool's context leaves, past its wait, for
// recording the command (Begin) and answering.
const beginMargin = 10 * time.Second

// dispatchFunc dispatches a command with observe on its result and returns
// its request id.
type dispatchFunc func(ctx context.Context, observe appuc.Observer) (string, error)

// pending is a command's answer while it has no outcome yet.
type pending struct {
	Status    model.CommandStatus `json:"status"`
	RequestID string              `json:"request_id"`
	Hint      string              `json:"hint"`
}

// succeeded is a finished command's answer: the agent's result.
type succeeded struct {
	Status    model.CommandStatus `json:"status"`
	RequestID string              `json:"request_id"`
	Result    json.RawMessage     `json:"result,omitempty"`
}

// deployed is a finished save's or lifecycle action's answer: the result
// and the app's status in the server's latest report (null when there is
// none yet); Note says it may need a later report.
type deployed struct {
	succeeded
	AppStatus *command.AppStatus `json:"app_status"`
	Note      string             `json:"note"`
}

// dispatch sends a command as userID, records it, and waits at most
// t.Wait for its outcome. It returns the succeeded record, or the tool's
// answer otherwise: a dispatch error, the agent's failure (isError),
// running or unknown. what names the command in errors ("save app").
func (t *tools) dispatch(ctx context.Context, userID, serverID string, kind command.Type, what string,
	send dispatchFunc) (cmdresult.Record, *sdk.CallToolResult) {
	return t.dispatchWithin(ctx, userID, serverID, kind, what, t.Wait, send)
}

// dispatchWithin is dispatch waiting at most bound. A succeeded record's
// Result is the agent's whole answer when it came to this process (the
// stored copy is cut to cmdresult.MaxResultBytes); else the stored copy,
// which then may carry "truncated": true.
func (t *tools) dispatchWithin(ctx context.Context, userID, serverID string, kind command.Type, what string,
	bound time.Duration, send dispatchFunc) (cmdresult.Record, *sdk.CallToolResult) {
	// Begin and Await use this context: bound it, so a stuck write or a
	// dropped client cannot hold the call past the wait.
	ctx, cancel := context.WithTimeout(ctx, bound+beginMargin)
	defer cancel()
	orgID, err := t.Users.PrimaryOrganizationID(ctx, userID)
	if err != nil {
		return cmdresult.Record{}, t.fail(err, "resolve your organization")
	}
	tr := t.Results.Track(orgID, serverID, string(kind))
	record := tr.Observer()
	var (
		mu   sync.Mutex
		full []byte
	)
	requestID, err := send(ctx, func(res port.CommandResult) {
		mu.Lock()
		full = res.Payload
		mu.Unlock()
		record(res)
	})
	if err != nil {
		return cmdresult.Record{}, t.fail(err, what)
	}
	if err := tr.Begin(ctx, requestID); err != nil {
		t.Log.Error("mcp: record a dispatched command", "error", err, "request_id", requestID)
		return cmdresult.Record{}, webmcp.ErrorText("the command was sent (request_id " + requestID +
			") but its result cannot be followed here; check the app with get_app_status")
	}
	rec, err := tr.Await(ctx, bound)
	if err != nil {
		// The context ended first (the client went away): the command
		// goes on, its outcome is still recorded.
		return cmdresult.Record{}, running(requestID)
	}
	if rec.Status == cmdresult.Succeeded {
		mu.Lock()
		if full != nil {
			rec.Result = full
		}
		mu.Unlock()
	}
	return t.settle(rec)
}

// isTruncated reports a stored result cut to fit (cmdresult's
// "truncated": true): its arrays lost their oldest items, or, for a result
// whose bulk is not in a top-level array (app.get), everything.
func isTruncated(raw []byte) bool {
	var probe struct {
		Truncated bool `json:"truncated"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.Truncated
}

// settle maps a record to its answer: the record itself when it
// succeeded, else what the tool answers.
func (t *tools) settle(rec cmdresult.Record) (cmdresult.Record, *sdk.CallToolResult) {
	switch rec.Status {
	case cmdresult.Succeeded:
		return rec, nil
	case cmdresult.Failed:
		return rec, webmcp.ErrorText(rec.Error)
	case cmdresult.Running:
		return rec, running(rec.RequestID)
	}
	return rec, webmcp.JSONResult(pending{Status: cmdresult.Unknown, RequestID: rec.RequestID,
		Hint: "the outcome was not recorded; check the app with get_app_status or list_apps"})
}

// running is the answer for a command still running at the bound.
func running(requestID string) *sdk.CallToolResult {
	return webmcp.JSONResult(pending{Status: cmdresult.Running, RequestID: requestID, Hint: "call get_command_result"})
}

// result is a succeeded record's answer with its result as is.
func result(rec cmdresult.Record) *sdk.CallToolResult {
	return webmcp.JSONResult(succeeded{Status: rec.Status, RequestID: rec.RequestID, Result: rec.Result})
}

// statusNote says what an app's status after a deploy is worth.
const statusNote = "app_status is the server's latest report and may predate this change; " +
	"containers can take a while to settle: poll get_app_status, and read get_app_logs if they do not come up"

// withStatus is a succeeded save's or lifecycle action's answer: the
// result plus the app's latest container status.
func (t *tools) withStatus(rec cmdresult.Record, serverID, appID string) *sdk.CallToolResult {
	return webmcp.JSONResult(deployed{
		succeeded: succeeded{Status: rec.Status, RequestID: rec.RequestID, Result: rec.Result},
		AppStatus: t.appStatus(serverID, appID), Note: statusNote,
	})
}

// appStatus is the app's status in the server's latest report (nil when
// none reports it).
func (t *tools) appStatus(serverID, appID string) *command.AppStatus {
	for _, s := range t.Status.AppStatuses(serverID, time.Now()) {
		if s.AppID == appID {
			s := s
			return &s
		}
	}
	return nil
}
