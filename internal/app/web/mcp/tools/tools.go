// Package tools is the MCP tools of the deploy loop: thin wrappers over the
// usecases the REST handlers call, with the same checks and errors. Every
// tool that addresses a server checks first that the caller's organization
// owns it, exactly as REST does: a server id is never authorization.
//
// An agent-bound tool dispatches its command, records it (cmdresult) and
// waits at most Deps.Wait (50 s) for the outcome; a command still running
// then answers {status: "running", request_id}, for get_command_result.
package tools

import (
	"context"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webmcp "winterflow/internal/app/web/mcp"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/cmdresult"
	"winterflow/internal/domain/usecase/secretreq"
	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/logger"
)

// DefaultWait is how long a tool waits for its command's outcome: below
// the ~100 s idle timeout of the tunnels and proxies in front of /mcp.
const DefaultWait = 50 * time.Second

// Deps is what the tools call.
type Deps struct {
	Log *logger.Logger
	// Users resolves the caller's organization.
	Users webutil.OrgResolver
	// Servers lists the caller's servers and answers ownership.
	Servers port.ServerRepository
	// Apps is the app usecase the REST app handlers use.
	Apps *appuc.UseCase
	// Dispatcher sends the commands no usecase method covers (the docker
	// resource lists).
	Dispatcher port.CommandDispatcher
	// Status is the latest container status per server.
	Status *status.Cache
	// SharedVars reads org and server shared variables.
	SharedVars port.SharedVariableRepository
	// Results records the dispatched commands and their outcomes.
	Results *cmdresult.Store
	// SecretRequests stores secret links; AppCatalog is the API's app list
	// (which apps exist, for a secret request's name check).
	SecretRequests port.SecretRequestRepository
	AppCatalog     port.AppRepository
	// WebURL is where the user opens Winterflow (WEB_URL): secret links
	// are built on it.
	WebURL string
	// Wait bounds a tool's wait for a command (0: DefaultWait).
	Wait time.Duration
}

// FromDeps wires the tools over the process's dependencies.
func FromDeps(d *bootstrap.Deps) Deps {
	return Deps{
		Log:     d.Log,
		Users:   d.UserService,
		Servers: d.ServerRepository,
		Apps: appuc.NewUseCase(&appuc.Deps{
			CommandDispatcher:       d.CommandDispatcher,
			AppRepository:           d.AppRepository,
			AppDomainRepository:     d.AppDomainRepository,
			ServerDomainRepository:  d.ServerDomainRepository,
			ServerStorageRepository: d.ServerStorageRepository,
			Servers:                 d.ServerRepository,
			Instances:               instanceLinks(d),
			Log:                     d.Log,
		}),
		Dispatcher: d.CommandDispatcher,
		Status:     d.StatusCache,
		SharedVars: d.SharedVariableRepository,
		Results:    d.CommandResults,

		SecretRequests: d.SecretRequestRepository,
		AppCatalog:     d.AppRepository,
		WebURL:         webURL(d),
	}
}

// webURL is d's WEB_URL ("" without a config).
func webURL(d *bootstrap.Deps) string {
	if d.Cfg == nil {
		return ""
	}
	return d.Cfg.GetWebURL()
}

// instanceLinks is d's template repository, or a nil interface (never a
// typed nil) without one.
func instanceLinks(d *bootstrap.Deps) port.InstanceLinks {
	if d.TemplateRepository == nil {
		return nil
	}
	return d.TemplateRepository
}

// tools is the registered tools' receiver.
type tools struct {
	Deps
	// secrets is the secret links usecase, checking a request's names
	// against the app's secret variables through the tools' agent read.
	secrets *secretreq.UseCase
}

// New registers every tool.
func New(d Deps) webmcp.Register {
	if d.Wait <= 0 {
		d.Wait = DefaultWait
	}
	t := &tools{Deps: d}
	var apps secretreq.Apps
	if d.AppCatalog != nil {
		apps = d.AppCatalog
	}
	t.secrets = secretreq.New(secretreq.Deps{Repo: d.SecretRequests, Orgs: d.Users, Servers: d.Servers, Apps: apps, AppSecrets: t})
	return webmcp.Compose(t.servers, t.apps, t.domains, t.docker, t.sharedVars, t.results, t.secretTools)
}

// handler is a tool's body: its result, never a transport error.
type handler[In any] func(ctx context.Context, in In) *sdk.CallToolResult

// readOnly marks a tool that changes nothing.
var readOnly = &sdk.ToolAnnotations{ReadOnlyHint: true}

// add registers a tool whose input schema is In's.
func add[In any](s *sdk.Server, name, description string, ann *sdk.ToolAnnotations, h handler[In]) {
	sdk.AddTool(s, &sdk.Tool{Name: name, Description: description, Annotations: ann},
		func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
			return h(ctx, in), nil, nil
		})
}

// forbidden is the answer for a server the caller's organization does not
// own (or that does not exist: the two read the same).
const forbidden = "forbidden: no such server in your organization (take server ids from list_servers)"

// caller is the authenticated user; a tool error without one.
func (t *tools) caller(ctx context.Context) (string, *sdk.CallToolResult) {
	userID := webmcp.UserID(ctx)
	if userID == "" {
		t.Log.Error("mcp: tool called without a principal")
		return "", webmcp.ErrorText("internal error")
	}
	return userID, nil
}

// owner is the caller once their organization is known to own serverID,
// the check every server-addressed REST route makes
// (webutil.RequireServerAccess): a lookup error denies too.
func (t *tools) owner(ctx context.Context, serverID string) (string, *sdk.CallToolResult) {
	userID, fail := t.caller(ctx)
	if fail != nil {
		return "", fail
	}
	if serverID == "" {
		return "", webmcp.ErrorText("server_id is required")
	}
	owns, err := t.Servers.UserOwnsServer(ctx, userID, serverID)
	if err != nil {
		t.Log.Error("mcp: check server ownership", "error", err, "server_id", serverID)
		return "", webmcp.ErrorText(forbidden)
	}
	if !owns {
		return "", webmcp.ErrorText(forbidden)
	}
	return userID, nil
}

// fail logs an unexpected error and answers it as a tool error (a domain
// error keeps its message).
func (t *tools) fail(err error, what string) *sdk.CallToolResult {
	if !webmcp.IsDomainError(err) {
		t.Log.Error("mcp: "+what, "error", err)
		return webmcp.ErrorText("failed to " + what)
	}
	return webmcp.ErrorResult(err)
}
