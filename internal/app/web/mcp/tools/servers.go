package tools

import (
	"context"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/domain/service/status"
)

type noInput struct{}

// serverView is one server in list_servers.
type serverView struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Online     bool            `json:"online"`
	LastSeenAt *time.Time      `json:"last_seen_at"`
	Features   map[string]bool `json:"features,omitempty"`
}

func (t *tools) servers(s *sdk.Server) {
	add(s, "list_servers", "The servers of your organization, with whether each one's agent is online. "+
		"Every other tool takes a server_id from here.", readOnly,
		func(ctx context.Context, _ noInput) *sdk.CallToolResult {
			userID, fail := t.caller(ctx)
			if fail != nil {
				return fail
			}
			servers, err := t.Servers.GetServers(ctx, userID)
			if err != nil {
				return t.fail(err, "list servers")
			}
			now := time.Now()
			out := make([]serverView, 0, len(servers))
			for _, srv := range servers {
				out = append(out, serverView{
					ID: srv.ID, Name: srv.Name, LastSeenAt: srv.LastSeenAt, Features: srv.Features,
					Online: t.Status.ServerLiveness(srv.ID, now) == status.LivenessOnline,
				})
			}
			return webmcp.JSONResult(map[string]any{"servers": out})
		})
}
