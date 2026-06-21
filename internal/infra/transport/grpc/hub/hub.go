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
	metrics      map[string]string
	cancelFunc   context.CancelFunc                                                // To cancel active stream
	stream       grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand] // Reference to the active stream

	pendingRequests     map[string]interface{}
	pendingRequestsLock sync.RWMutex
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
	msgs, cancel, err := h.bus.Subscribe(ctx, h.cfg.GetBusRequestQueue())
	if err != nil {
		return fmt.Errorf("subscribe request queue: %w", err)
	}
	go func() {
		defer cancel()
		for msg := range msgs {
			var cmd bus.CommandMessage
			if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
				h.log.Error("failed to unmarshal command message", err)
				continue
			}
			h.dispatchToAgent(cmd)
		}
	}()
	h.log.Info("hub bus bridge started", "request_queue", h.cfg.GetBusRequestQueue())
	return nil
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
		h.publishResult(cmd.RequestID, model.NotificationStatusError, nil, "agent not connected")
		return
	}

	req := &proto.ServerCommand{
		Command: &proto.ServerCommand_Request{
			Request: &proto.RequestEnvelope{
				Base: &proto.BaseMessage{
					MessageId:       cmd.RequestID,
					Timestamp:       timestamppb.Now(),
					AgentId:         cmd.AgentID,
					ProtocolVersion: "1.0.0",
				},
				RequestId:     cmd.RequestID,
				Type:          cmd.Type,
				ContentType:   "application/json",
				SchemaVersion: "1.0.0",
				Payload:       cmd.Payload,
			},
		},
	}
	if err := stream.Send(req); err != nil {
		h.log.Error("failed to send command to agent", "error", err, "agent_id", cmd.AgentID)
		h.publishResult(cmd.RequestID, model.NotificationStatusError, nil, "failed to deliver command to agent")
	}
}

// publishResult emits an agent's reply (or a delivery error) onto the response
// queue as a model.Notification keyed by the request id, which the API's
// dispatch.Manager routes to the originating user over SSE.
func (h *Hub) publishResult(requestID string, statusCode model.NotificationStatus, payload []byte, detail string) {
	if h.bus == nil {
		return
	}
	ntf := model.Notification{
		Type:      model.NotificationOperationResult,
		Ref:       requestID,
		Status:    statusCode,
		Timestamp: time.Now(),
	}
	if statusCode == model.NotificationStatusError {
		ntf.Error = detail
	} else if len(payload) > 0 {
		ntf.Payload = json.RawMessage(payload)
	}
	if err := h.bus.Publish(context.Background(), h.cfg.GetBusResponseQueue(), ntf); err != nil {
		h.log.Error("failed to publish result", "error", err, "request_id", requestID)
	}
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

	// Cancel all active agent streams
	h.agentsLock.Lock()
	for id, server := range h.agents {
		if server.streamActive && server.cancelFunc != nil {
			h.log.Info("Cancelling active stream", "agent_id", id)
			server.cancelFunc()
		}
	}
	h.agentsLock.Unlock()

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
			streamActive:        false,
			lastSeen:            time.Now(),
			metrics:             make(map[string]string),
			cancelFunc:          nil,
			stream:              nil,
			pendingRequests:     make(map[string]interface{}),
			pendingRequestsLock: sync.RWMutex{},
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
	//var streamCtx context.Context
	var cancel context.CancelFunc

	defer func() {
		if cancel != nil {
			cancel()
		}
		if agent != nil && agentID != "" {
			h.agentsLock.Lock()
			agent.streamActive = false
			agent.stream = nil
			agent.cancelFunc = nil
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

					_, cancel = context.WithCancel(stream.Context())
					agent.cancelFunc = cancel
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
			statusCode := model.NotificationStatusSuccess
			detail := payload.Response.Base.Detail
			if payload.Response.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
				statusCode = model.NotificationStatusError
			}
			h.publishResult(payload.Response.RequestId, statusCode, payload.Response.Payload, detail)

		default:
			h.log.Warn("Received unknown message type from agent", "agent_id", agentID)
		}
	}
}
