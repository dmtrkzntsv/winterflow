package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"winterflow/internal/infra/transport/grpc/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Dispatcher handles a command RequestEnvelope received from the hub and
// returns the ResponseEnvelope to send back. Implemented by the agent
// application layer (internal/app/agent); kept as an interface here so the
// transport stays decoupled from business logic.
type Dispatcher interface {
	Dispatch(ctx context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope
}

type Agent struct {
	cfg    *config.ServerConfig
	log    *logger.Logger
	conn   *grpc.ClientConn
	client proto.AgentServiceClient

	agentID         string
	protocolVersion string
	capabilities    map[string]string
	features        map[string]bool
	apps            []*proto.App
	dispatcher      Dispatcher

	streamMutex  sync.RWMutex
	streamActive bool
	streamCancel context.CancelFunc
	sendMutex    sync.Mutex // serializes stream.Send across heartbeat + handler goroutines

	registered      bool
	registeredMutex sync.RWMutex
}

func NewAgent(log *logger.Logger, cfg *config.ServerConfig, agentID string) *Agent {
	return &Agent{
		cfg:             cfg,
		log:             log,
		agentID:         agentID,
		protocolVersion: "1.0.0",
		capabilities:    make(map[string]string),
		features:        make(map[string]bool),
		apps:            make([]*proto.App, 0),
		streamActive:    false,
		registered:      false,
	}
}

// SetDispatcher installs the command handler the agent invokes for each
// RequestEnvelope received from the hub.
func (a *Agent) SetDispatcher(d Dispatcher) {
	a.dispatcher = d
}

func (a *Agent) SetCapabilities(capabilities map[string]string) {
	a.capabilities = capabilities
}

func (a *Agent) SetFeatures(features map[string]bool) {
	a.features = features
}

func (a *Agent) SetApps(apps []*proto.App) {
	a.apps = apps
}

func (a *Agent) Connect(ctx context.Context) error {
	caCert, err := os.ReadFile(a.cfg.GetAgentCACertPath())
	if err != nil {
		return fmt.Errorf("failed to read CA certificate: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to append CA certificate to pool")
	}

	clientCert, err := tls.LoadX509KeyPair(a.cfg.GetAgentCertPath(), a.cfg.GetAgentKeyPath())
	if err != nil {
		return fmt.Errorf("failed to load client certificate and key: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      certPool,
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2"},
	}

	a.log.Info("TLS configuration loaded",
		"ca_cert_path", a.cfg.GetAgentCACertPath(),
		"cert_path", a.cfg.GetAgentCertPath(),
		"key_path", a.cfg.GetAgentKeyPath())

	creds := credentials.NewTLS(tlsConfig)

	hubAddress := fmt.Sprintf("%s:%s", a.cfg.GetHubHost(), a.cfg.GetHubPort())

	conn, err := grpc.NewClient(hubAddress,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                15 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to hub: %w", err)
	}

	a.conn = conn
	a.client = proto.NewAgentServiceClient(conn)

	a.log.Info("Connected to hub", "address", hubAddress)
	return nil
}

func (a *Agent) Register(ctx context.Context) error {
	if a.client == nil {
		return fmt.Errorf("not connected to hub")
	}

	req := &proto.RegisterAgentRequest{
		Base: &proto.BaseMessage{
			MessageId:       fmt.Sprintf("reg-req-%d", time.Now().UnixNano()),
			Timestamp:       timestamppb.Now(),
			AgentId:         a.agentID,
			ProtocolVersion: a.protocolVersion,
		},
		Capabilities: a.capabilities,
		Features:     a.features,
		Apps:         a.apps,
	}

	a.log.Info("Registering with hub",
		"agent_id", a.agentID,
		"capabilities", a.capabilities,
		"features", a.features,
		"apps_count", len(a.apps))

	resp, err := a.client.RegisterAgent(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to register agent: %w", err)
	}

	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		return fmt.Errorf("registration failed: %s", resp.Base.Detail)
	}

	a.registeredMutex.Lock()
	a.registered = true
	a.registeredMutex.Unlock()

	a.log.Info("Agent registration successful",
		"agent_id", a.agentID,
		"response_detail", resp.Base.Detail)

	return nil
}

func (a *Agent) StartStream(ctx context.Context) error {
	a.registeredMutex.RLock()
	if !a.registered {
		a.registeredMutex.RUnlock()
		return fmt.Errorf("agent must be registered before starting stream")
	}
	a.registeredMutex.RUnlock()

	a.streamMutex.Lock()
	if a.streamActive {
		a.streamMutex.Unlock()
		return fmt.Errorf("stream already active")
	}
	a.streamMutex.Unlock()

	streamCtx, cancel := context.WithCancel(ctx)

	stream, err := a.client.AgentStream(streamCtx)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to start stream: %w", err)
	}

	a.streamMutex.Lock()
	a.streamActive = true
	a.streamCancel = cancel
	a.streamMutex.Unlock()

	a.log.Info("Agent stream started", "agent_id", a.agentID)

	// Start heartbeat routine
	go a.heartbeatRoutine(streamCtx, stream)

	// Handle incoming messages
	go a.handleIncomingMessages(streamCtx, stream)

	return nil
}

func (a *Agent) heartbeatRoutine(ctx context.Context, stream grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand]) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.log.Info("Heartbeat routine stopping", "agent_id", a.agentID)
			return
		case <-ticker.C:
			heartbeat := &proto.AgentMessage{
				Message: &proto.AgentMessage_Heartbeat{
					Heartbeat: &proto.AgentHeartbeat{
						Base: &proto.BaseMessage{
							MessageId:       fmt.Sprintf("hb-%d", time.Now().UnixNano()),
							Timestamp:       timestamppb.Now(),
							AgentId:         a.agentID,
							ProtocolVersion: a.protocolVersion,
						},
					},
				},
			}

			if err := a.sendMessage(stream, heartbeat); err != nil {
				a.log.Error("Failed to send heartbeat", "error", err, "agent_id", a.agentID)
				return
			}

			a.log.Debug("Heartbeat sent", "agent_id", a.agentID)
		}
	}
}

func (a *Agent) handleIncomingMessages(ctx context.Context, stream grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand]) {
	defer func() {
		a.streamMutex.Lock()
		a.streamActive = false
		if a.streamCancel != nil {
			a.streamCancel()
			a.streamCancel = nil
		}
		a.streamMutex.Unlock()
		a.log.Info("Agent stream closed", "agent_id", a.agentID)
	}()

	for {
		select {
		case <-ctx.Done():
			a.log.Info("Message handler stopping", "agent_id", a.agentID)
			return
		default:
			msg, err := stream.Recv()
			if err != nil {
				a.log.Error("Error receiving message from hub", "error", err, "agent_id", a.agentID)
				return
			}

			switch cmd := msg.Command.(type) {
			case *proto.ServerCommand_HeartbeatResponse:
				if cmd.HeartbeatResponse.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
					a.log.Debug("Heartbeat acknowledged", "agent_id", a.agentID)
				} else {
					a.log.Warn("Heartbeat not acknowledged",
						"agent_id", a.agentID,
						"response_code", cmd.HeartbeatResponse.Base.ResponseCode,
						"detail", cmd.HeartbeatResponse.Base.Detail)
				}

			case *proto.ServerCommand_Request:
				a.log.Info("Received request from hub",
					"agent_id", a.agentID,
					"request_id", cmd.Request.RequestId,
					"type", cmd.Request.Type)

				// Dispatch in its own goroutine so a slow handler (e.g. a
				// `docker compose up`) doesn't stall heartbeats or other
				// commands on the receive loop. stream.Send is safe here:
				// gRPC permits one concurrent Send and one Recv.
				go a.handleRequest(ctx, stream, cmd.Request)

			default:
				a.log.Warn("Received unknown command type from hub", "agent_id", a.agentID)
			}
		}
	}
}

// sendMessage serializes all stream.Send calls. gRPC streams allow only one
// concurrent Send, and both the heartbeat routine and per-request handler
// goroutines send on the same stream.
func (a *Agent) sendMessage(stream grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand], msg *proto.AgentMessage) error {
	a.sendMutex.Lock()
	defer a.sendMutex.Unlock()
	return stream.Send(msg)
}

// handleRequest dispatches a single command and sends the response back. It
// runs in its own goroutine so long-running commands don't block the receive
// loop.
func (a *Agent) handleRequest(ctx context.Context, stream grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand], req *proto.RequestEnvelope) {
	var resp *proto.ResponseEnvelope
	if a.dispatcher == nil {
		a.log.Error("no dispatcher configured", "agent_id", a.agentID, "type", req.Type)
		resp = &proto.ResponseEnvelope{
			Base: &proto.BaseResponse{
				MessageId:       fmt.Sprintf("resp-%d", time.Now().UnixNano()),
				Timestamp:       timestamppb.Now(),
				ResponseCode:    proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR,
				Detail:          "agent has no command dispatcher",
				AgentId:         a.agentID,
				ProtocolVersion: a.protocolVersion,
			},
			RequestId:     req.RequestId,
			Type:          req.Type,
			ContentType:   "application/json",
			SchemaVersion: "1.0.0",
		}
	} else {
		resp = a.dispatcher.Dispatch(ctx, req)
	}

	if err := a.sendMessage(stream, &proto.AgentMessage{
		Message: &proto.AgentMessage_Response{Response: resp},
	}); err != nil {
		a.log.Error("Failed to send response", "error", err, "agent_id", a.agentID, "request_id", req.RequestId)
	}
}

func (a *Agent) Stop(ctx context.Context) error {
	a.log.Info("Stopping agent", "agent_id", a.agentID)

	a.streamMutex.Lock()
	if a.streamActive && a.streamCancel != nil {
		a.streamCancel()
		a.streamActive = false
		a.streamCancel = nil
	}
	a.streamMutex.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		if err != nil {
			a.log.Error("Error closing connection", "error", err)
			return err
		}
	}

	a.log.Info("Agent stopped successfully", "agent_id", a.agentID)
	return nil
}

func (a *Agent) IsConnected() bool {
	if a.conn == nil {
		return false
	}
	return a.conn.GetState().String() == "READY"
}

func (a *Agent) IsRegistered() bool {
	a.registeredMutex.RLock()
	defer a.registeredMutex.RUnlock()
	return a.registered
}

func (a *Agent) IsStreamActive() bool {
	a.streamMutex.RLock()
	defer a.streamMutex.RUnlock()
	return a.streamActive
}
