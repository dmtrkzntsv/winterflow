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

// EventKind enumerates the agent-initiated events published on events:<region>.
type EventKind string

const (
	// EventServerOnline is a liveness pulse (from heartbeat): the server is up.
	EventServerOnline EventKind = "server.online"
	// EventCapabilities reports a server's capabilities/features (on register).
	EventCapabilities EventKind = "server.capabilities"
	// EventAppsStatus reports container status for a server's apps.
	EventAppsStatus EventKind = "apps.status"
)

// EventMessage is the events-queue payload: an unsolicited event from a server's
// agent. ServerID identifies the source; Payload is the JSON body for Kind.
type EventMessage struct {
	ServerID string    `json:"server_id"`
	Kind     EventKind `json:"kind"`
	Payload  []byte    `json:"payload"`
}
