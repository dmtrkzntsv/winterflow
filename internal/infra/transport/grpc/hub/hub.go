package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
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
	auth       Authorizer
	// revokedAt remembers recent revocations (server id -> when, guarded by
	// agentsLock) so a registration whose auth check was in flight when the
	// revocation arrived is refused. Entries older than revokeMemory are
	// pruned.
	revokedAt map[string]time.Time

	// drainFlag mirrors the hub's drain flag in Redis (StartDrainWatch);
	// stopping is set by Drain. Either makes the hub draining.
	drainFlag atomic.Bool
	stopping  atomic.Bool
	// announceNow asks the announcer to announce at once (the draining
	// state changed).
	announceNow chan struct{}
	// drainMu guards moverCancel, which stops the goroutine sending
	// hub.move (nil while none runs).
	drainMu     sync.Mutex
	moverCancel context.CancelFunc
	// drainCheckEvery overrides DrainCheckEvery (tests).
	drainCheckEvery time.Duration
}

type agent struct {
	streamActive bool
	lastSeen     time.Time
	// fingerprint is the certificate the API authorized at registration;
	// a stream must present the same one.
	fingerprint string
	stream      grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand] // Reference to the active stream
	// closeStream ends the bound stream's handler (revocation, or a newer
	// stream of the same agent); nil until a stream is bound.
	closeStream context.CancelCauseFunc
	// moveSentAt is when the hub last told this agent to move (draining);
	// guarded by agentsLock.
	moveSentAt time.Time

	// sendMu serializes writes to the stream: gRPC forbids concurrent SendMsg
	// on one stream, and both the bus bridge (dispatchToAgent) and the
	// heartbeat path (AgentStream) write to it.
	sendMu sync.Mutex
}

// send writes one command to the given stream under the agent's send mutex.
func (a *agent) send(stream grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand], cmd *proto.ServerCommand) error {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	return stream.Send(cmd)
}

// NewHub builds the hub. auth decides whether a connecting agent's
// certificate is still its server's active one; a nil auth refuses every
// registration as Unavailable.
func NewHub(log *logger.Logger, cfg *config.ServerConfig, b bus.Bus, auth Authorizer) *Hub {
	h := &Hub{
		agents:      make(map[string]*agent),
		revokedAt:   make(map[string]time.Time),
		announceNow: make(chan struct{}, 1),
		cfg:         cfg,
		log:         log,
		srv:         createServer(log, cfg),
		bus:         b,
		auth:        failClosed(auth),
	}
	proto.RegisterAgentServiceServer(h.srv, h)

	return h
}

// failClosed turns a typed-nil *BusAuthorizer into a nil Authorizer, so a
// missing authorizer refuses registrations instead of panicking.
func failClosed(auth Authorizer) Authorizer {
	if ba, ok := auth.(*BusAuthorizer); ok && ba == nil {
		return nil
	}
	return auth
}

// StartBusBridge subscribes to this hub's own request queue
// (bus:requests:<hub_id>, where the API routes the commands of the servers
// assigned to it) and forwards each command onto the target agent's active
// gRPC stream. It runs until ctx is cancelled. Agent responses are published
// back onto the response queue from AgentStream. It also subscribes the hub
// broadcast channel, where the API announces revoked servers.
func (h *Hub) StartBusBridge(ctx context.Context) error {
	if h.bus == nil {
		h.log.Warn("no bus configured, hub will not forward commands")
		return nil
	}
	hubID := h.cfg.GetHubID()
	if hubID == "" {
		return fmt.Errorf("HUB_PUBLIC_ADDR must be host:port (got %q)", h.cfg.GetHubPublicAddr())
	}
	queue := h.cfg.HubRequestQueue(hubID)
	bus.SubscribeJSON(ctx, h.bus, queue, h.log, func(cmd bus.CommandMessage) {
		h.dispatchToAgent(cmd)
	})
	bus.SubscribeJSON(ctx, h.bus, h.cfg.GetHubBroadcastChannel(), h.log, func(msg bus.HubBroadcast) {
		if msg.Type == bus.HubBroadcastRevoked && msg.ServerID != "" {
			h.revoke(msg.ServerID)
		}
	})
	go h.reapStaleAgents(ctx)
	h.log.Info("hub bus bridge started", "request_queue", queue)
	return nil
}

var (
	// errRevoked ends a revoked server's stream.
	errRevoked = errors.New("server revoked")
	// errReplaced ends a stream when the same agent opens a newer one.
	errReplaced = errors.New("replaced by a newer stream")
	// errStale ends a stream whose registration the reaper dropped.
	errStale = errors.New("agent idle past its TTL")
)

// errDraining is the FailedPrecondition message a draining hub refuses new
// registrations with.
const errDraining = "hub is draining"

// revokeMemory is how long a revocation is remembered against registrations
// whose auth check started before it; far beyond AuthorizeTimeout.
const revokeMemory = 5 * time.Minute

// revoke drops a server's agent if this hub holds it: its stream (if bound)
// is closed and its registration deleted.
func (h *Hub) revoke(serverID string) {
	h.agentsLock.Lock()
	defer h.agentsLock.Unlock()
	now := time.Now()
	h.pruneRevokedLocked(now)
	h.revokedAt[serverID] = now
	a, ok := h.agents[serverID]
	if !ok {
		return
	}
	if a.closeStream != nil {
		a.closeStream(errRevoked)
	}
	delete(h.agents, serverID)
	h.log.Info("server revoked, agent dropped", "agent_id", serverID)
}

// pruneRevokedLocked forgets revocations older than revokeMemory. Caller
// holds agentsLock.
func (h *Hub) pruneRevokedLocked(now time.Time) {
	for id, at := range h.revokedAt {
		if now.Sub(at) > revokeMemory {
			delete(h.revokedAt, id)
		}
	}
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

// reapOnce removes agents idle past the TTL as of now, closing their streams
// (no stream outlives its registration, which revocation goes through).
// Returns the count removed (exposed for testing).
func (h *Hub) reapOnce(now time.Time) int {
	h.agentsLock.Lock()
	defer h.agentsLock.Unlock()
	h.pruneRevokedLocked(now)
	removed := 0
	for id, a := range h.agents {
		if now.Sub(a.lastSeen) <= staleAgentTTL {
			continue
		}
		if a.closeStream != nil {
			a.closeStream(errStale)
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
		h.publishError(cmd.RequestID, bus.ErrAgentNotConnected)
		return
	}

	req := &proto.ServerCommand{
		Command: &proto.ServerCommand_Request{
			Request: codec.EnvelopeFromCommand(cmd),
		},
	}
	if err := a.send(stream, req); err != nil {
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
	return h.Serve(lis)
}

// Serve runs the gRPC server on lis until the hub shuts down.
func (h *Hub) Serve(lis net.Listener) error {
	h.log.Info("gRPC server listening", "addr", lis.Addr().String())
	if err := h.srv.Serve(lis); err != nil {
		h.log.Error("Failed to serve", "error", err)
	}

	return nil
}

// Shutdown drains first (at most DrainTimeout, less if ctx ends sooner):
// the hub announces draining, tells its agents to move and waits for their
// streams to close. Then it stops the gRPC server gracefully, forcing it
// when ctx ends.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.log.Info("Draining the hub before it stops", "agents", h.activeStreams())
	drainCtx, cancelDrain := context.WithTimeout(ctx, DrainTimeout)
	if err := h.Drain(drainCtx); err != nil {
		h.log.Warn("Drain incomplete, stopping anyway", "agents_left", h.activeStreams())
	}
	cancelDrain()

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
	// Not a denial: the agent asks the API which hub to use instead.
	if h.isDraining() {
		return nil, status.Error(codes.FailedPrecondition, errDraining)
	}

	certID, fingerprint, err := peerIdentity(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "no client certificate: %v", err)
	}
	if agentID != certID {
		h.log.Warn("agent id does not match certificate", "agent_id", agentID, "cert_cn", certID)
		return nil, status.Error(codes.PermissionDenied, "agent id does not match certificate")
	}
	if h.auth == nil {
		return nil, status.Error(codes.Unavailable, "cannot verify server right now")
	}
	checkStarted := time.Now()
	verdict, reason, err := h.auth.Authorize(ctx, agentID, fingerprint)
	if err != nil {
		h.log.Warn("agent auth check failed", "agent_id", agentID, "error", err)
		return nil, status.Error(codes.Unavailable, "cannot verify server right now")
	}
	if verdict == bus.VerdictMoved {
		// Not a denial: the agent asks the API where to go. Nothing is
		// recorded.
		if reason == "" {
			reason = "server belongs to another hub"
		}
		h.log.Info("agent belongs to another hub", "agent_id", agentID)
		return nil, status.Error(codes.FailedPrecondition, reason)
	}
	if verdict != bus.VerdictAllowed {
		if reason == "" {
			reason = string(verdict)
		}
		h.log.Warn("agent refused", "agent_id", agentID, "verdict", verdict, "reason", reason)
		return nil, status.Errorf(codes.PermissionDenied, "server %s: %s", verdict, reason)
	}

	h.log.Info("Agent registration request",
		"agent_id", agentID,
		"capabilities", req.Capabilities,
		"features", req.Features,
		"apps_count", len(req.Apps))

	h.agentsLock.Lock()
	defer h.agentsLock.Unlock()

	// Draining started while the check was in flight.
	if h.isDraining() {
		return nil, status.Error(codes.FailedPrecondition, errDraining)
	}

	// A revocation that arrived while the check was in flight wins over its
	// "allowed".
	if at, revoked := h.revokedAt[agentID]; revoked && !at.Before(checkStarted) {
		h.log.Warn("agent revoked during its auth check", "agent_id", agentID)
		return nil, status.Error(codes.PermissionDenied, "server revoked")
	}

	if existingAgent, exists := h.agents[agentID]; exists {
		h.log.Info("Agent already registered, updating info", "agent_id", agentID)
		existingAgent.lastSeen = time.Now()
		if existingAgent.fingerprint != fingerprint {
			// A new certificate was authorized: a stream opened with the
			// old one must not keep running.
			if existingAgent.closeStream != nil {
				existingAgent.closeStream(errReplaced)
				existingAgent.closeStream = nil
			}
			// Unbind it, so its cleanup leaves this registration alone.
			existingAgent.stream = nil
			existingAgent.streamActive = false
			existingAgent.fingerprint = fingerprint
		}
	} else {
		h.log.Info("Registering new agent", "agent_id", agentID)
		h.agents[agentID] = &agent{
			streamActive: false,
			lastSeen:     time.Now(),
			stream:       nil,
			fingerprint:  fingerprint,
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

// recvResult is one Recv outcome handed from the receive goroutine to the
// stream handler.
type recvResult struct {
	msg *proto.AgentMessage
	err error
}

func (h *Hub) AgentStream(stream grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand]) error {
	// The stream's identity is its client certificate; every message must
	// carry the same agent id.
	certID, fingerprint, err := peerIdentity(stream.Context())
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "no client certificate: %v", err)
	}

	// ctx ends when the client goes away or the server is revoked
	// (agent.closeStream); Recv runs on its own goroutine so the handler can
	// return on revocation while a Recv is blocked. Returning cancels the
	// stream, which unblocks that Recv.
	ctx, closeStream := context.WithCancelCause(stream.Context())
	defer closeStream(nil)

	// Only a registered agent may stream: RegisterAgent is where the API
	// vouches for the certificate. One stream per agent: a newer stream
	// closes the one bound before it.
	h.agentsLock.Lock()
	agent, registered := h.agents[certID]
	if !registered {
		h.agentsLock.Unlock()
		h.log.Warn("Agent not registered, closing stream", "agent_id", certID)
		return status.Error(codes.Unauthenticated, "agent not registered")
	}
	if agent.fingerprint != fingerprint {
		h.agentsLock.Unlock()
		h.log.Warn("stream certificate differs from the registered one, closing stream", "agent_id", certID)
		return status.Error(codes.Unauthenticated, "certificate does not match registration")
	}
	if agent.closeStream != nil {
		agent.closeStream(errReplaced)
	}
	agent.streamActive = true
	agent.stream = stream
	agent.closeStream = closeStream
	agent.lastSeen = time.Now()
	h.agentsLock.Unlock()
	agentID := certID

	defer func() {
		h.agentsLock.Lock()
		// Remove the agent entirely on stream close: a disconnected agent
		// should not linger in the registry (it would appear registered, and
		// stale entries accumulate across reconnects). It re-registers on
		// reconnect. Only delete while the entry still holds this stream, so
		// a newer stream that already took it over isn't clobbered.
		if cur, ok := h.agents[agentID]; ok && cur.stream == stream {
			delete(h.agents, agentID)
		}
		h.agentsLock.Unlock()
		h.log.Info("Agent stream closed", "agent_id", agentID)
	}()

	msgs := make(chan recvResult)
	go func() {
		for {
			msg, err := stream.Recv()
			select {
			case msgs <- recvResult{msg: msg, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	h.log.Info("New agent stream connection established", "agent_id", certID)

	for {
		var msg *proto.AgentMessage
		select {
		case <-ctx.Done():
			switch cause := context.Cause(ctx); {
			case errors.Is(cause, errRevoked):
				h.log.Info("closing revoked agent's stream", "agent_id", certID)
				return status.Error(codes.PermissionDenied, "server revoked")
			case errors.Is(cause, errReplaced):
				h.log.Info("closing agent stream replaced by a newer one", "agent_id", certID)
				return status.Error(codes.Aborted, "replaced by a newer stream")
			case errors.Is(cause, errStale):
				h.log.Info("closing reaped agent's stream", "agent_id", certID)
				return status.Error(codes.Unavailable, "agent idle past its TTL")
			}
			return ctx.Err()
		case r := <-msgs:
			if r.err != nil {
				h.log.Error("Error receiving message from agent", "error", r.err, "agent_id", agentID)
				return r.err
			}
			msg = r.msg
		}

		if id, ok := messageAgentID(msg); ok && id != certID {
			h.log.Warn("agent id does not match certificate in stream", "cert_cn", certID, "received", id)
			return status.Error(codes.PermissionDenied, "agent id does not match certificate")
		}

		switch payload := msg.Message.(type) {
		case *proto.AgentMessage_Heartbeat:
			if payload.Heartbeat.Base == nil {
				h.log.Warn("Received heartbeat without base message", "agent_id", agentID)
				continue
			}

			h.agentsLock.Lock()
			agent.lastSeen = time.Now()
			h.agentsLock.Unlock()

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

			if err := agent.send(stream, heartbeatResponse); err != nil {
				h.log.Error("Error sending heartbeat response", "error", err, "agent_id", agentID)
				return err
			}

			h.log.Debug("Heartbeat processed", "agent_id", agentID)

		case *proto.AgentMessage_Event:
			// Unsolicited agent push (e.g. apps.status): forward verbatim onto
			// the events queue. Also a liveness signal.
			h.agentsLock.Lock()
			agent.lastSeen = time.Now()
			h.agentsLock.Unlock()
			h.publishEvent(bus.EventKind(payload.Event.Kind), certID, payload.Event.Payload)

		case *proto.AgentMessage_Response:
			if payload.Response.Base == nil {
				h.log.Warn("Received response without base message", "agent_id", agentID)
				continue
			}

			h.log.Info("Received response from agent",
				"agent_id", certID,
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

// messageAgentID returns the agent id a stream message claims; ok is false
// when the message carries no header.
func messageAgentID(msg *proto.AgentMessage) (id string, ok bool) {
	switch p := msg.Message.(type) {
	case *proto.AgentMessage_Heartbeat:
		if b := p.Heartbeat.GetBase(); b != nil {
			return b.AgentId, true
		}
	case *proto.AgentMessage_Event:
		if b := p.Event.GetBase(); b != nil {
			return b.AgentId, true
		}
	case *proto.AgentMessage_Response:
		if b := p.Response.GetBase(); b != nil {
			return b.AgentId, true
		}
	}
	return "", false
}
