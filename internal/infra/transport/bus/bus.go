// Package bus defines the transport-neutral message bus that bridges the
// distributed API ("brain") and the gRPC Hub.
//
// Two implementations satisfy Bus:
//   - redis bus (internal/infra/transport/redis/bus) for the distributed
//     topology, where API and Hub are separate horizontally-scaled processes.
//   - in-process bus (internal/infra/transport/mem/bus) for standalone, where
//     API and agent live in one process and Redis would be overkill.
//
// The API publishes a CommandMessage on the request queue; the Hub consumes it,
// forwards it to the target agent's gRPC stream, and publishes the agent's
// result back on the response queue, where the API's dispatch.Manager routes it
// to the originating user over SSE (correlated by request_id).
package bus

import "context"

// Message is one published payload and the channel it arrived on.
type Message struct {
	Channel string
	Payload string
}

// Bus is the minimal pub/sub contract shared by the Redis and in-process buses.
type Bus interface {
	// Publish marshals v to JSON and publishes it on channel.
	Publish(ctx context.Context, channel string, v any) error
	// Subscribe streams messages from the given channels until the returned
	// cancel func is called or ctx is done.
	Subscribe(ctx context.Context, channels ...string) (<-chan Message, func() error, error)
}

// CommandMessage is the request-queue payload: a command addressed to an agent.
// It is the bus-level projection of proto.RequestEnvelope.
type CommandMessage struct {
	AgentID   string `json:"agent_id"`
	RequestID string `json:"request_id"`
	Type      string `json:"type"`
	Payload   []byte `json:"payload"`
}

// ErrAgentNotConnected is the Error of the result a hub publishes for a
// command addressed to an agent without an active stream (unknown, not yet
// streaming, or gone). Callers compare it exactly to tell this transient
// bounce from an agent-side failure.
const ErrAgentNotConnected = "agent not connected"

// EventKind enumerates the agent-initiated events published on events:<region>.
type EventKind string

const (
	// EventServerOnline is a liveness pulse (from heartbeat): the server is up.
	EventServerOnline EventKind = "server.online"
	// EventCapabilities reports a server's capabilities/features (on register).
	EventCapabilities EventKind = "server.capabilities"
	// EventAppsStatus reports container status for a server's apps.
	EventAppsStatus EventKind = "apps.status"
	// EventServerMetrics carries one minute's resource sample of a server
	// and its apps (payload: metrics.Sample).
	EventServerMetrics EventKind = "server.metrics"
	// EventVarsReloaded reports one app's shared-variable reload outcome
	// (payload: command.VarsReloadedPayload without server_id).
	EventVarsReloaded EventKind = "vars.reloaded"
)

// EventMessage is the events-queue payload: an unsolicited event from a server's
// agent. ServerID identifies the source; Payload is the JSON body for Kind.
type EventMessage struct {
	ServerID string    `json:"server_id"`
	Kind     EventKind `json:"kind"`
	Payload  []byte    `json:"payload"`
}

// Hub <-> API control messages (agent authentication). Hubs ask over
// hub:requests, the API answers on hub:responses:<hub_id>, and announces
// revocations on hub:broadcast.

// HubRequestVerify asks whether a connecting agent's certificate is still
// the active one of its server.
const HubRequestVerify = "server.verify"

// HubBroadcastRevoked tells every hub to drop a server's agent.
const HubBroadcastRevoked = "server.revoked"

// HubRequest is a hub -> API request on hub:requests; Type is
// HubRequestVerify today.
type HubRequest struct {
	Type            string `json:"type"`
	RequestID       string `json:"request_id"`
	HubID           string `json:"hub_id"`
	ServerID        string `json:"server_id"`
	CertFingerprint string `json:"cert_fingerprint"`
}

// AgentVerdict is the API's answer to an agent authentication check.
type AgentVerdict string

const (
	VerdictAllowed AgentVerdict = "allowed"
	VerdictRevoked AgentVerdict = "revoked"
	VerdictUnknown AgentVerdict = "unknown"
)

// HubResponse is the API -> hub reply on hub:responses:<hub_id>.
type HubResponse struct {
	RequestID string       `json:"request_id"`
	Verdict   AgentVerdict `json:"verdict"`
	Reason    string       `json:"reason,omitempty"`
}

// HubBroadcast is an API -> every hub message on hub:broadcast; Type is
// HubBroadcastRevoked today.
type HubBroadcast struct {
	Type     string `json:"type"`
	ServerID string `json:"server_id"`
}
