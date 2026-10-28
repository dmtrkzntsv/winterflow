package tools

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/domain/model"
)

type sharedVarsInput struct {
	ServerID string `json:"server_id,omitempty" jsonschema:"also list this server's own variables, from list_servers"`
}

// sharedVar is a shared variable as MCP shows it: a secret by name only.
type sharedVar struct {
	Name        string `json:"name"`
	Secret      bool   `json:"secret"`
	Value       string `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

func (t *tools) sharedVars(s *sdk.Server) {
	add(s, "list_shared_variables", "The organization's shared variables and, with server_id, that server's own "+
		"(they win over the organization's). Every app on a server can use them as ${NAME}; an app's own variable "+
		"of the same name wins. Plain values are shown, secrets by name only.", readOnly, t.listSharedVars)
}

func (t *tools) listSharedVars(ctx context.Context, in sharedVarsInput) *sdk.CallToolResult {
	var orgID string
	out := map[string]any{}
	if in.ServerID != "" {
		if _, fail := t.owner(ctx, in.ServerID); fail != nil {
			return fail
		}
		info, err := t.SharedVars.ServerVarsInfo(ctx, in.ServerID)
		if err != nil {
			return t.fail(err, "list shared variables")
		}
		orgID = info.OrgID
		vars, err := t.SharedVars.ListServer(ctx, in.ServerID)
		if err != nil {
			return t.fail(err, "list shared variables")
		}
		out["server"] = viewShared(vars)
	} else {
		userID, fail := t.caller(ctx)
		if fail != nil {
			return fail
		}
		id, err := t.Users.PrimaryOrganizationID(ctx, userID)
		if err != nil {
			return t.fail(err, "resolve your organization")
		}
		orgID = id
	}
	vars, err := t.SharedVars.ListOrg(ctx, orgID)
	if err != nil {
		return t.fail(err, "list shared variables")
	}
	out["org"] = viewShared(vars)
	return webmcp.JSONResult(out)
}

func viewShared(vars []model.SharedVariable) []sharedVar {
	out := make([]sharedVar, 0, len(vars))
	for _, v := range vars {
		sv := sharedVar{Name: v.Name, Secret: v.IsSecret, Description: v.Description}
		if !v.IsSecret {
			sv.Value = v.Value
		}
		out = append(out, sv)
	}
	return out
}
