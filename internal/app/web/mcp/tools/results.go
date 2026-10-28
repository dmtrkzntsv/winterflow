package tools

import (
	"context"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/cmdresult"
)

type commandResultInput struct {
	RequestID string `json:"request_id" jsonschema:"the request_id a tool answered with status running"`
}

func (t *tools) results(s *sdk.Server) {
	add(s, "get_command_result", "Wait (up to 50 seconds) for the outcome of a command a tool left running, by its "+
		"request_id. Answers the result, status running again, or status unknown (expired after a day, or not "+
		"this organization's). A failed command is an error with the agent's message. The result is the agent's "+
		"answer as stored (a get_app result is shown as get_app shows it; a get_app_logs result has every service's "+
		"lines); a stored result over 1 MiB is cut to fit and says \"truncated\": true.", readOnly, t.getCommandResult)
}

func (t *tools) getCommandResult(ctx context.Context, in commandResultInput) *sdk.CallToolResult {
	userID, fail := t.caller(ctx)
	if fail != nil {
		return fail
	}
	if in.RequestID == "" {
		return webmcp.ErrorText("request_id is required")
	}
	ctx, cancel := context.WithTimeout(ctx, t.Wait+beginMargin)
	defer cancel()
	orgID, err := t.Users.PrimaryOrganizationID(ctx, userID)
	if err != nil {
		return t.fail(err, "resolve your organization")
	}
	rec, err := t.Results.Await(ctx, orgID, in.RequestID, nil, t.Wait)
	switch {
	case errors.Is(err, model.ErrNotFound):
		return webmcp.JSONResult(pending{Status: cmdresult.Unknown, RequestID: in.RequestID,
			Hint: "no such command in your organization (results are kept for a day)"})
	case err != nil && rec.RequestID == "":
		return t.fail(err, "read the command's result")
	case err != nil:
		// The context ended first: the command may still finish.
		return running(in.RequestID)
	}
	if _, fail := t.settle(rec); fail != nil {
		return fail
	}
	if rec.Kind == string(command.TypeAppGet) {
		// As get_app answers it: decoded, secrets masked, never a view of
		// a result cut to fit.
		return answerApp(rec)
	}
	return result(rec)
}
