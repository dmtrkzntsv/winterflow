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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"get_app_logs, fix and save again, passing the app_id the previous save_app answered (also after an error), " +
	"or the retry creates a second app. A hostname in ingress must be covered by a domain registered on the server " +
	"(list_domains, create_domain). Secret values never pass through this connection: a secret variable is sent " +
	"only as \"<encrypted>\" to keep the stored value, and new secret values are asked from the user with " +
	"request_secrets. Commands run on the server's agent: a tool waits up to 50 seconds for the outcome and " +
	"otherwise answers status \"running\" with a request_id to pass to get_command_result."

// MaxBodyBytes bounds a /mcp request body. A save_app call carries the app's
// whole file set, and REST save-app has no cap of its own, so this is the
// generous bound of a compose project plus its config files.
const MaxBodyBytes = 8 << 20

// NewHandler serves the tools register adds over Streamable HTTP, reading at
// most MaxBodyBytes of a request body; a larger one is answered 413 with a
// JSON-RPC error.
func NewHandler(register Register) http.Handler {
	srv := sdk.NewServer(
		&sdk.Implementation{Name: "winterflow", Version: serverVersion()},
		&sdk.ServerOptions{Instructions: instructions},
	)
	register(srv)
	h := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return srv },
		// DisableLocalhostProtection: the API usually sits behind a
		// reverse proxy or tunnel that dials it over loopback, so the
		// SDK's DNS-rebinding guard would see a loopback address with a
		// public Host header and refuse every call. That guard protects
		// local desktop servers with ambient credentials; /mcp requires a
		// bearer token or the session cookie (whose unsafe requests need
		// the CSRF header) on every call.
		// MaxRequestBodyBytes: limitBody has read the body already; the
		// SDK's own bound (4 MiB by default, a plain-text 413) is set to
		// the same value so it never cuts a body limitBody admitted.
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true,
			MaxRequestBodyBytes: MaxBodyBytes},
	)
	return limitBody(h, MaxBodyBytes)
}

// limitBody reads the request body, at most limit bytes, before next sees
// it, so an oversized body is refused with a JSON-RPC error (a client reads
// /mcp answers as JSON) instead of the SDK's plain-text one, whatever the
// transfer encoding.
func limitBody(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > limit {
			tooLarge(w, limit)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		_ = r.Body.Close()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				tooLarge(w, limit)
				return
			}
			writeRPCError(w, http.StatusBadRequest, -32700, "failed to read the request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

func tooLarge(w http.ResponseWriter, limit int64) {
	w.Header().Set("Connection", "close")
	writeRPCError(w, http.StatusRequestEntityTooLarge, -32600,
		fmt.Sprintf("request body exceeds %d bytes", limit))
}

// writeRPCError answers a JSON-RPC error with no id (the request was not
// read).
func writeRPCError(w http.ResponseWriter, status, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": code, "message": message},
	})
}

func serverVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
