package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
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

func NewHub(log *logger.Logger, cfg *config.ServerConfig) *Hub {
	h := &Hub{
		agents: make(map[string]*agent),
		cfg:    cfg,
		log:    log,
		srv:    createServer(log, cfg),
	}
	proto.RegisterAgentServiceServer(h.srv, h)

	return h
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

	h.log.Info("Agent registration successful", "agent_id", agentID)
	return response, nil
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

		default:
			h.log.Warn("Received unknown message type from agent", "agent_id", agentID)
		}
	}
}
