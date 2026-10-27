package tools

import (
	"context"
	"errors"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/domain/model"
)

type requestSecretsInput struct {
	ServerID string            `json:"server_id" jsonschema:"the server the app runs on, from list_servers"`
	AppID    string            `json:"app_id,omitempty" jsonschema:"the app, from list_apps; omit for a new app"`
	Names    []string          `json:"names" jsonschema:"the secret variables to ask for (at most 32), e.g. API_KEY; for an existing app each must be one of its secret variables"`
	Notes    map[string]string `json:"notes,omitempty" jsonschema:"an optional note per name, shown beside its field (at most 500 bytes each), e.g. where to find the value"`
	Message  string            `json:"message,omitempty" jsonschema:"an optional message shown above the fields (at most 2000 bytes)"`
}

type secretRequestInput struct {
	RequestID string `json:"request_id" jsonschema:"the request, from request_secrets"`
}

func (t *tools) secretTools(s *sdk.Server) {
	add(s, "request_secrets", "Ask the user for secret values without seeing them: answers a link for the user to open "+
		"(signed in to Winterflow) within 24 hours. Their browser encrypts each value to the server's key; neither you "+
		"nor Winterflow's API ever see them. Poll get_secret_request until it reads submitted, then call save_app with "+
		"secrets_from: <request_id> (within 24 hours of the submission).", nil, t.requestSecrets)
	add(s, "get_secret_request", "A secret request's status: pending (the user has not submitted it yet), submitted "+
		"(save_app with secrets_from can use it), used (a save took it) or expired.", readOnly, t.getSecretRequest)
}

func (t *tools) requestSecrets(ctx context.Context, in requestSecretsInput) *sdk.CallToolResult {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return fail
	}
	var appID *string
	if in.AppID != "" {
		appID = &in.AppID
	}
	req, raw, err := t.secrets.Create(ctx, userID, in.ServerID, appID, in.Names, in.Notes, in.Message)
	if err != nil {
		return t.fail(err, "create the secret request")
	}
	answer := map[string]any{"request_id": req.ID, "url": t.secretURL(raw), "expires_at": req.ExpiresAt.UTC().Format(time.RFC3339)}
	if strings.TrimRight(t.WebURL, "/") == "" {
		answer["note"] = "Winterflow's WEB_URL is not set: the link is a path; put the address the user opens Winterflow at in front of it"
	}
	return webmcp.JSONResult(answer)
}

// secretURL is the link's address on WEB_URL (a bare path without one).
func (t *tools) secretURL(raw string) string {
	return strings.TrimRight(t.WebURL, "/") + "/secrets/" + raw
}

func (t *tools) getSecretRequest(ctx context.Context, in secretRequestInput) *sdk.CallToolResult {
	userID, fail := t.caller(ctx)
	if fail != nil {
		return fail
	}
	if in.RequestID == "" {
		return webmcp.ErrorText("request_id is required")
	}
	req, err := t.secrets.Get(ctx, userID, in.RequestID)
	if err != nil {
		return t.fail(err, "read the secret request")
	}
	return webmcp.JSONResult(map[string]any{
		"status": req.Status, "names": req.Names, "expires_at": req.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// SecretVariables reports an existing app's secret variables: the ones it
// stores as secrets and the ones its compose files declare encrypted (read
// from the agent, bounded like the keep check's read). An app the API does
// not list on serverID is a new one.
func (t *tools) SecretVariables(ctx context.Context, userID, serverID, appID string) (map[string]bool, bool, error) {
	if t.AppCatalog != nil {
		app, err := t.AppCatalog.GetApp(ctx, appID)
		switch {
		case errors.Is(err, model.ErrAppNotFound), err == nil && app.ServerID != serverID:
			return nil, false, nil
		case err != nil:
			return nil, false, err
		}
	}
	got, err := t.readApp(ctx, userID, serverID, appID, "the app's secret variables")
	if err != nil {
		return nil, false, err
	}
	names := map[string]bool{}
	for _, v := range got.App.Variables {
		if v.Encrypted {
			names[v.ItemName()] = true
		}
	}
	for _, v := range got.DeclaredVariables {
		if v.Encrypted {
			names[v.Name] = true
		}
	}
	return names, true, nil
}
