// Package mcp is the MCP edge: it builds the SDK server, serves it over the
// Streamable HTTP transport at /mcp and defines the seam through which tool
// packages register their tools.
//
// The transport is stateless with JSON responses: every call is one POST
// answered with one JSON body, so /mcp survives restarts, needs no sticky
// sessions across API replicas, and passes reverse proxies and tunnels that
// buffer or cut streams. A tool that waits for an agent bounds its wait
// itself (see the tools package).
package mcp

import (
	"net/http"
	"runtime/debug"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Register adds tools to the server.
type Register func(*sdk.Server)

// Compose runs every non-nil fn in order.
func Compose(fns ...Register) Register {
	return func(s *sdk.Server) {
		for _, fn := range fns {
			if fn != nil {
				fn(s)
			}
		}
	}
}

// instructions is what a client tells its model about this server.
const instructions = "Winterflow deploys Docker Compose apps to the user's servers. " +
	"Each server runs an agent; an app lives on one server and is a set of files (a compose.yml and anything it " +
	"references) plus variables, an optional git source and optional ingress (hostnames routed to the app). " +
	"Ids are never guessed: take server ids from list_servers and app ids from list_apps. " +
	"The deploy loop: write or edit the compose file (call get_app first when editing, and send the full state back " +
	"to save_app: what you leave out is removed), declare what an install fills in under the compose file's " +
	"x-winterflow block (variables with required, encrypted, pattern and description; expose entries naming the " +
	"service ports Winterflow publishes and routes hostnames to), save_app, then check get_app_status and " +
	"get_app_logs, fix and save again. A hostname in ingress must be covered by a domain registered on the server " +
	"(list_domains, create_domain). Secret values never pass through this connection: a secret variable is sent " +
	"only as \"<encrypted>\" to keep the stored value, and new secret values are asked from the user with " +
	"request_secrets. Commands run on the server's agent: a tool waits up to 50 seconds for the outcome and " +
	"otherwise answers status \"running\" with a request_id to pass to get_command_result."

// NewHandler serves the tools register adds over Streamable HTTP.
func NewHandler(register Register) http.Handler {
	srv := sdk.NewServer(
		&sdk.Implementation{Name: "winterflow", Version: serverVersion()},
		&sdk.ServerOptions{Instructions: instructions},
	)
	register(srv)
	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return srv },
		// DisableLocalhostProtection: the API usually sits behind a
		// reverse proxy or tunnel that dials it over loopback, so the
		// SDK's DNS-rebinding guard would see a loopback address with a
		// public Host header and refuse every call. That guard protects
		// local desktop servers with ambient credentials; /mcp requires a
		// bearer token or the session cookie (whose unsafe requests need
		// the CSRF header) on every call.
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true},
	)
}

func serverVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
