package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/codec"
	"winterflow/internal/infra/transport/grpc/handler"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"winterflow/internal/infra/transport/grpc/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Hub struct {
	proto.UnimplementedAgentServiceServer
	agents     map[string]*agent
	agentsLock sync.RWMutex
	cfg        *config.ServerConfig
	log        *logger.Logger
	srv        *grpc.Server
	bus        bus.Bus
}

type agent struct {
	streamActive bool
	lastSeen     time.Time
	stream       grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand] // Reference to the active stream
}

func NewHub(log *logger.Logger, cfg *config.ServerConfig, b bus.Bus) *Hub {
	h := &Hub{
		agents: make(map[string]*agent),
		cfg:    cfg,
		log:    log,
		srv:    createServer(log, cfg),
		bus:    b,
	}
	proto.RegisterAgentServiceServer(h.srv, h)

	return h
}

// StartBusBridge subscribes to the request queue and forwards each command
// onto the target agent's active gRPC stream. It runs until ctx is cancelled.
// The Hub is the only consumer of the request queue; agent responses are
// published back onto the response queue from AgentStream.
func (h *Hub) StartBusBridge(ctx context.Context) error {
	if h.bus == nil {
		h.log.Warn("no bus configured, hub will not forward commands")
		return nil
	}
	bus.SubscribeJSON(ctx, h.bus, h.cfg.GetBusRequestQueue(), h.log, func(cmd bus.CommandMessage) {
		h.dispatchToAgent(cmd)
	})
	go h.reapStaleAgents(ctx)
	h.log.Info("hub bus bridge started", "request_queue", h.cfg.GetBusRequestQueue())
	return nil
}

const (
	// staleAgentTTL is how long an agent may go without a heartbeat before the
	// hub drops its registration. Agents heartbeat every 30s; ~3 missed beats.
	staleAgentTTL = 100 * time.Second
	reapInterval  = 30 * time.Second
)

// reapStaleAgents periodically removes agents that haven't been seen within the
// TTL — covering wedged streams or registrations whose stream never connected,
// which the stream-close path alone can't catch.
func (h *Hub) reapStaleAgents(ctx context.Context) {
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.reapOnce(now)
		}
	}
}

// reapOnce removes agents idle past the TTL as of now. Returns the count
// removed (exposed for testing).
func (h *Hub) reapOnce(now time.Time) int {
	h.agentsLock.Lock()
	defer h.agentsLock.Unlock()
	removed := 0
	for id, a := range h.agents {
		if now.Sub(a.lastSeen) <= staleAgentTTL {
			continue
		}
		delete(h.agents, id)
		removed++
		h.log.Warn("reaped stale agent", "agent_id", id, "last_seen", a.lastSeen)
	}
	return removed
}

// dispatchToAgent locates the addressed agent's stream and sends the command.
// If the agent is unknown or offline it publishes an error result back so the
// caller doesn't block until timeout.
func (h *Hub) dispatchToAgent(cmd bus.CommandMessage) {
	h.agentsLock.RLock()
	a, ok := h.agents[cmd.AgentID]
	active := ok && a.streamActive && a.stream != nil
	stream := func() grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand] {
		if active {
			return a.stream
		}
		return nil
	}()
	h.agentsLock.RUnlock()

	if !active {
		h.log.Warn("command for offline/unknown agent", "agent_id", cmd.AgentID, "request_id", cmd.RequestID)
		h.publishError(cmd.RequestID, "agent not connected")
		return
	}

	req := &proto.ServerCommand{
		Command: &proto.ServerCommand_Request{
			Request: codec.EnvelopeFromCommand(cmd),
		},
	}
	if err := stream.Send(req); err != nil {
		h.log.Error("failed to send command to agent", "error", err, "agent_id", cmd.AgentID)
		h.publishError(cmd.RequestID, "failed to deliver command to agent")
	}
}

// publishNotification emits a result onto the response queue, where the API's
// dispatch.Manager routes it to the originating user over SSE (keyed by Ref).
func (h *Hub) publishNotification(n model.Notification) {
	if h.bus == nil {
		return
	}
	if err := h.bus.Publish(context.Background(), h.cfg.GetBusResponseQueue(), n); err != nil {
		h.log.Error("failed to publish result", "error", err, "request_id", n.Ref)
	}
}

// publishError emits a synthetic delivery failure for a command the hub could
// not forward (unknown/offline agent, dead stream).
func (h *Hub) publishError(requestID, detail string) {
	h.publishNotification(model.Notification{
		Type:      model.NotificationOperationResult,
		Ref:       requestID,
		Status:    model.NotificationStatusError,
		Error:     detail,
		Timestamp: time.Now(),
	})
}

// publishEvent forwards an agent-initiated event (liveness, capabilities,
// status) to the region's events queue, where the API persists/caches it and
// pushes to SSE.
func (h *Hub) publishEvent(kind bus.EventKind, serverID string, payload []byte) {
	if h.bus == nil {
		return
	}
	if err := h.bus.Publish(context.Background(), h.cfg.GetBusEventsQueue(), bus.EventMessage{
		ServerID: serverID,
		Kind:     kind,
		Payload:  payload,
	}); err != nil {
		h.log.Error("failed to publish event", "error", err, "kind", kind, "server_id", serverID)
	}
}

func createServer(log *logger.Logger, cfg *config.ServerConfig) *grpc.Server {
	caCert, err := os.ReadFile(cfg.GetHubCACertPath())
	if err != nil {
		log.Fatal("Failed to read CA certificate", "error", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		log.Fatal("Failed to append CA certificate to pool")
	}

	serverCert, err := tls.LoadX509KeyPair(cfg.GetHubCertPath(), cfg.GetHubKeyPath())
	if err != nil {
		log.Fatal("Failed to load server certificate and key", "error", err)
	}

	tlsConfig := &tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    certPool,
		MinVersion:   tls.VersionTLS12,
		// gRPC requires HTTP/2, so we must advertise it via ALPN.
		// Without this, some clients will send a ClientHello but abort after
		// receiving no suitable protocol selection, leading to mysterious
		// "connection reset" / TransientFailure errors.
		NextProtos: []string{"h2"},
	}

	log.Info("TLS configuration", "client_auth", tlsConfig.ClientAuth, "ca_cert_path", cfg.GetHubCACertPath())
	log.Info("Hub certificate loaded", "cert", cfg.GetHubCertPath(), "key", cfg.GetHubKeyPath())

	creds := credentials.NewTLS(tlsConfig)
	srv := grpc.NewServer(
		grpc.Creds(creds),
		grpc.StatsHandler(handler.NewLogStatsHandler(log)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:                  15 * time.Second,
			Timeout:               5 * time.Second,
			MaxConnectionIdle:     30 * time.Minute,
			MaxConnectionAge:      2 * time.Hour,
			MaxConnectionAgeGrace: 5 * time.Minute,
		}),
	)

	return srv
}

func (h *Hub) ListenAndServe(ctx context.Context) error {
	lis, err := net.Listen("tcp", ":"+h.cfg.GetHubPort())
	if err != nil {
		h.log.Fatal("Failed to listen", "error", err)
	}

	h.log.Info("gRPC server listening", "port", h.cfg.GetHubPort())
	if err := h.srv.Serve(lis); err != nil {
		h.log.Error("Failed to serve", "error", err)
	}

	return nil
}

func (h *Hub) Shutdown(ctx context.Context) error {
	h.log.Info("Shutting down gRPC server")

	// Active agent streams are torn down by GracefulStop / Stop below; the
	// stream handlers' defers clean up the registry.

	// Create a channel to signal when graceful stop completes
	done := make(chan struct{})

	go func() {
		h.srv.GracefulStop()
		close(done)
	}()

	// Wait for either graceful shutdown to complete or context timeout
	select {
	case <-done:
		h.log.Info("gRPC server shutdown completed gracefully")
		return nil
	case <-ctx.Done():
		h.log.Warn("Graceful shutdown timeout, forcing stop")
		h.srv.Stop()
		return ctx.Err()
	}
}

func (h *Hub) RegisterAgent(ctx context.Context, req *proto.RegisterAgentRequest) (*proto.RegisterAgentResponse, error) {
	if req.Base == nil {
		return nil, status.Errorf(codes.InvalidArgument, "base message is required")
	}

	agentID := req.Base.AgentId
	if agentID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "agent_id is required")
	}

	h.log.Info("Agent registration request",
		"agent_id", agentID,
		"capabilities", req.Capabilities,
		"features", req.Features,
		"apps_count", len(req.Apps))

	h.agentsLock.Lock()
	defer h.agentsLock.Unlock()

	if existingAgent, exists := h.agents[agentID]; exists {
		h.log.Info("Agent already registered, updating info", "agent_id", agentID)
		existingAgent.lastSeen = time.Now()
	} else {
		h.log.Info("Registering new agent", "agent_id", agentID)
		h.agents[agentID] = &agent{
			streamActive: false,
			lastSeen:     time.Now(),
			stream:       nil,
		}
	}

	response := &proto.RegisterAgentResponse{
		Base: &proto.BaseResponse{
			MessageId:       fmt.Sprintf("reg-resp-%d", time.Now().UnixNano()),
			Timestamp:       timestamppb.Now(),
			ResponseCode:    proto.ResponseCode_RESPONSE_CODE_SUCCESS,
			Detail:          "Agent registered successfully",
			AgentId:         agentID,
			ProtocolVersion: req.Base.ProtocolVersion,
		},
	}

	// Report the agent's capabilities/features so the API can persist them.
	if caps, err := json.Marshal(capabilitiesEvent{Capabilities: req.Capabilities, Features: req.Features}); err == nil {
		h.publishEvent(bus.EventCapabilities, agentID, caps)
	}

	h.log.Info("Agent registration successful", "agent_id", agentID)
	return response, nil
}

// capabilitiesEvent is the JSON body of a bus.EventCapabilities event.
type capabilitiesEvent struct {
	Capabilities map[string]string `json:"capabilities"`
	Features     map[string]bool   `json:"features"`
}

func (h *Hub) AgentStream(stream grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand]) error {
	var agentID string
	var agent *agent

	defer func() {
		if agentID != "" {
			h.agentsLock.Lock()
			// Remove the agent entirely on stream close: a disconnected agent
			// should not linger in the registry (it would appear registered, and
			// stale entries accumulate across reconnects). It re-registers on
			// reconnect. Only delete if the map still points at this stream, so a
			// fast reconnect that already replaced the entry isn't clobbered.
			if cur, ok := h.agents[agentID]; ok && cur == agent {
				delete(h.agents, agentID)
			}
			h.agentsLock.Unlock()
			h.log.Info("Agent stream closed", "agent_id", agentID)
		}
	}()

	h.log.Info("New agent stream connection established")

	for {
		msg, err := stream.Recv()
		if err != nil {
			h.log.Error("Error receiving message from agent", "error", err, "agent_id", agentID)
			return err
		}

		switch payload := msg.Message.(type) {
		case *proto.AgentMessage_Heartbeat:
			if payload.Heartbeat.Base == nil {
				h.log.Warn("Received heartbeat without base message", "agent_id", agentID)
				continue
			}

			currentAgentID := payload.Heartbeat.Base.AgentId
			if agentID == "" {
				agentID = currentAgentID
				h.log.Info("Agent stream identified", "agent_id", agentID)

				h.agentsLock.Lock()
				if existingAgent, exists := h.agents[agentID]; exists {
					agent = existingAgent
					agent.streamActive = true
					agent.stream = stream
					agent.lastSeen = time.Now()
				} else {
					h.agentsLock.Unlock()
					h.log.Warn("Agent not registered, closing stream", "agent_id", agentID)
					return status.Errorf(codes.Unauthenticated, "agent not registered")
				}
				h.agentsLock.Unlock()
			} else if currentAgentID != agentID {
				h.log.Warn("Agent ID mismatch in stream", "expected", agentID, "received", currentAgentID)
				return status.Errorf(codes.InvalidArgument, "agent ID mismatch")
			}

			if agent != nil {
				h.agentsLock.Lock()
				agent.lastSeen = time.Now()
				h.agentsLock.Unlock()
			}

			// A heartbeat is a liveness pulse: tell the API the server is up so
			// it can update last_seen + the in-memory status cache.
			h.publishEvent(bus.EventServerOnline, agentID, nil)

			heartbeatResponse := &proto.ServerCommand{
				Command: &proto.ServerCommand_HeartbeatResponse{
					HeartbeatResponse: &proto.AgentHeartbeatResponse{
						Base: &proto.BaseResponse{
							MessageId:       fmt.Sprintf("hb-resp-%d", time.Now().UnixNano()),
							Timestamp:       timestamppb.Now(),
							ResponseCode:    proto.ResponseCode_RESPONSE_CODE_SUCCESS,
							Detail:          "Heartbeat acknowledged",
							AgentId:         agentID,
							ProtocolVersion: payload.Heartbeat.Base.ProtocolVersion,
						},
					},
				},
			}

			if err := stream.Send(heartbeatResponse); err != nil {
				h.log.Error("Error sending heartbeat response", "error", err, "agent_id", agentID)
				return err
			}

			h.log.Debug("Heartbeat processed", "agent_id", agentID)

		case *proto.AgentMessage_Response:
			if payload.Response.Base == nil {
				h.log.Warn("Received response without base message", "agent_id", agentID)
				continue
			}

			h.log.Info("Received response from agent",
				"agent_id", agentID,
				"request_id", payload.Response.RequestId,
				"type", payload.Response.Type,
				"response_code", payload.Response.Base.ResponseCode)

			// Forward the agent's reply back onto the response queue, keyed by
			// request_id, so the originating API can route the result to the
			// user over SSE.
			h.publishNotification(codec.NotificationFromResponse(payload.Response.RequestId, payload.Response))

		default:
			h.log.Warn("Received unknown message type from agent", "agent_id", agentID)
		}
	}
}
