package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"winterflow/pkg/agentsig"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/grpc/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
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
	streamDone   chan struct{} // closed when the active stream's receive loop exits
	streamErr    error         // why the receive loop ended (read after streamDone closes)
	stream       grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand]
	sendMutex    sync.Mutex // serializes stream.Send across heartbeat + handler goroutines
	// lifeCtx is the context of the agent's lifetime (Run's), which detached
	// commands run on: they outlive the stream they arrived on.
	lifeCtx      context.Context
	detachedWait time.Duration // tests shorten detachedReplyWait
	// streamReady is closed while a stream is active and replaced by an open
	// channel when it goes down: detached replies wait on it for the next
	// connection.
	streamReady       chan struct{}
	streamReadyClosed bool

	registered      bool
	registeredMutex sync.RWMutex

	// bringUp is one connect+register+stream attempt (connectRegisterStream;
	// tests replace it): the stream's done channel, a func giving the error
	// that ended the stream once done is closed, or the bring-up error.
	bringUp func(ctx context.Context) (<-chan struct{}, func() error, error)
	// deniedBackoff are the waits between PermissionDenied retries; the
	// attempt after the last one gives up with ErrRevoked.
	deniedBackoff []time.Duration
	// retryBackoff overrides the {base, max} of the ordinary reconnect
	// backoff (tests).
	retryBackoff *[2]time.Duration
	// settle is how long a stream must stay up for its connection to count as
	// a success that resets the denial count (default streamSettle).
	settle time.Duration
	// now and sleep are the clock and waiter Run uses (tests replace them).
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) bool

	// Migration: the API (apiURL) names the hub to connect to, asked with a
	// request signed by the agent key (loadKey); hubStore keeps its answer.
	// fallbackHub is the env's HUB_HOST/HUB_PORT address (tests replace it).
	hubStore    *HubStore
	apiURL      string
	httpClient  *http.Client
	loadKey     func() (*ecdsa.PrivateKey, error)
	fallbackHub func() string
	// moveCh carries a hub.move from the stream to Run (buffered, 1).
	moveCh chan struct{}
	// askFloor overrides the least gap between hub-prompted asks (tests).
	askFloor time.Duration
	// pinRefresh overrides the gap between pin-set refreshes while
	// connected (tests).
	pinRefresh time.Duration

	// Hub certificate pinning (verifyHub): pinErr is the last refusal,
	// seenFP the fingerprint of the last accepted hub certificate.
	pinMu        sync.Mutex
	pinErr       error
	seenFP       string
	warnedNoPins atomic.Bool
}

const streamSettle = 2 * time.Minute

// ErrRevoked is returned by Run once the hub has refused this server
// maxDenials times in a row: the server was removed or its certificate is no
// longer active, and retrying cannot help.
var ErrRevoked = errors.New("server was removed or its certificate is not active")

// ExitRevoked is the process exit status after ErrRevoked; the systemd unit
// lists it in RestartPreventExitStatus.
const ExitRevoked = 78

const maxDenials = 5

var defaultDeniedBackoff = []time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute, 4 * time.Minute}

func NewAgent(log *logger.Logger, cfg *config.ServerConfig, agentID string) *Agent {
	a := &Agent{
		cfg:             cfg,
		log:             log,
		agentID:         agentID,
		protocolVersion: "1.0.0",
		capabilities:    make(map[string]string),
		features:        make(map[string]bool),
		apps:            make([]*proto.App, 0),
		streamActive:    false,
		streamReady:     make(chan struct{}),
		registered:      false,
		deniedBackoff:   defaultDeniedBackoff,
		moveCh:          make(chan struct{}, 1),
	}
	if cfg != nil {
		a.hubStore = NewHubStore(cfg.GetAgentDataDir())
		a.apiURL = cfg.GetWinterflowAPIURL()
		keyPath := cfg.GetAgentKeyPath()
		a.loadKey = func() (*ecdsa.PrivateKey, error) { return agentsig.LoadKey(keyPath) }
	}
	return a
}

// detachedReplyWait bounds how long a detached command's reply waits for a
// connected stream before it is dropped.
const detachedReplyWait = 5 * time.Minute

// markUpLocked records that a stream is active and wakes the replies waiting
// for one. The caller holds streamMutex.
func (a *Agent) markUpLocked() {
	if !a.streamReadyClosed {
		close(a.streamReady)
		a.streamReadyClosed = true
	}
}

// markDownLocked records that no stream is active. The caller holds
// streamMutex.
func (a *Agent) markDownLocked() {
	a.streamActive = false
	if a.streamReadyClosed {
		a.streamReady = make(chan struct{})
		a.streamReadyClosed = false
	}
}

// currentStream is the active stream, or nil and a channel that closes when
// one is.
func (a *Agent) currentStream() (grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand], <-chan struct{}) {
	a.streamMutex.RLock()
	defer a.streamMutex.RUnlock()
	if a.streamActive && a.stream != nil {
		return a.stream, nil
	}
	return nil, a.streamReady
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
		// After the CA and host name checks: the hub marker and the pin set.
		VerifyPeerCertificate: a.verifyHub,
	}

	a.log.Info("TLS configuration loaded",
		"ca_cert_path", a.cfg.GetAgentCACertPath(),
		"cert_path", a.cfg.GetAgentCertPath(),
		"key_path", a.cfg.GetAgentKeyPath())

	creds := credentials.NewTLS(tlsConfig)

	addr := a.hubAddr()

	conn, err := grpc.NewClient(addr,
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

	a.log.Info("Connected to hub", "address", addr)
	return nil
}

// hubAddress joins HUB_HOST and HUB_PORT, bracketing an IPv6 address (given
// bare or already in brackets).
func hubAddress(host, port string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	return net.JoinHostPort(host, port)
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

	done := make(chan struct{})
	a.streamMutex.Lock()
	a.streamActive = true
	a.streamCancel = cancel
	a.streamDone = done
	a.streamErr = nil
	a.stream = stream
	a.lifeCtx = ctx
	a.markUpLocked()
	a.streamMutex.Unlock()

	a.log.Info("Agent stream started", "agent_id", a.agentID)

	// Start heartbeat routine
	go a.heartbeatRoutine(streamCtx, stream)

	// Handle incoming messages; signals `done` when the stream ends so the
	// supervising Run loop can reconnect.
	go a.handleIncomingMessages(streamCtx, stream, done)

	return nil
}

func (a *Agent) heartbeatRoutine(ctx context.Context, stream grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand]) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Beat immediately: the hub marks the stream active on the first
	// heartbeat, so waiting a full tick would leave a window right after
	// connect where commands bounce with "agent not connected".
	if err := a.sendHeartbeat(stream); err != nil {
		a.log.Error("Failed to send initial heartbeat", "error", err, "agent_id", a.agentID)
		return
	}

	for {
		select {
		case <-ctx.Done():
			a.log.Info("Heartbeat routine stopping", "agent_id", a.agentID)
			return
		case <-ticker.C:
			if err := a.sendHeartbeat(stream); err != nil {
				a.log.Error("Failed to send heartbeat", "error", err, "agent_id", a.agentID)
				return
			}
			a.log.Debug("Heartbeat sent", "agent_id", a.agentID)
		}
	}
}

func (a *Agent) sendHeartbeat(stream grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand]) error {
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
	return a.sendMessage(stream, heartbeat)
}

func (a *Agent) handleIncomingMessages(ctx context.Context, stream grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand], done chan struct{}) {
	defer func() {
		a.streamMutex.Lock()
		a.markDownLocked()
		a.stream = nil
		if a.streamCancel != nil {
			a.streamCancel()
			a.streamCancel = nil
		}
		a.streamMutex.Unlock()
		close(done) // wake the supervising Run loop to reconnect
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
				a.streamMutex.Lock()
				a.streamErr = err
				a.streamMutex.Unlock()
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

// SendEvent pushes an unsolicited, fire-and-forget event (e.g. apps.status)
// up the active stream. Returns an error when no stream is active; callers
// treat that as "skip this tick", not fatal — the reporter fires again.
func (a *Agent) SendEvent(kind string, payload []byte) error {
	a.streamMutex.RLock()
	stream := a.stream
	active := a.streamActive
	a.streamMutex.RUnlock()
	if !active || stream == nil {
		return fmt.Errorf("no active stream")
	}
	return a.sendMessage(stream, &proto.AgentMessage{
		Message: &proto.AgentMessage_Event{
			Event: &proto.AgentEvent{
				Base: &proto.BaseMessage{
					MessageId:       fmt.Sprintf("ev-%d", time.Now().UnixNano()),
					Timestamp:       timestamppb.Now(),
					AgentId:         a.agentID,
					ProtocolVersion: a.protocolVersion,
				},
				Kind:    kind,
				Payload: payload,
			},
		},
	})
}

// handleRequest dispatches a single command and sends the response back. It
// runs in its own goroutine so long-running commands don't block the receive
// loop.
func (a *Agent) handleRequest(ctx context.Context, stream grpc.BidiStreamingClient[proto.AgentMessage, proto.ServerCommand], req *proto.RequestEnvelope) {
	// hub.move is the hub's own message (it is draining), not a command:
	// Run asks the API where to go. No reply.
	if command.Type(req.Type) == command.HubMove {
		a.requestMove()
		return
	}
	// A detached command (backup.run) waits for work that outlives a stream
	// (the hub recycles every connection about every two hours): it runs on
	// the agent's lifetime, and its reply goes out on whichever stream is
	// current when it is ready.
	detached := command.Type(req.Type).Detached()
	if detached {
		a.streamMutex.RLock()
		if a.lifeCtx != nil {
			ctx = a.lifeCtx
		}
		a.streamMutex.RUnlock()
	}
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

	msg := &proto.AgentMessage{Message: &proto.AgentMessage_Response{Response: resp}}
	if detached {
		a.sendDetachedReply(ctx, msg, req.RequestId)
		return
	}
	if err := a.sendMessage(stream, msg); err != nil {
		a.log.Error("Failed to send response", "error", err, "agent_id", a.agentID, "request_id", req.RequestId)
	}
}

// sendDetachedReply sends a detached command's reply on the current stream,
// waiting for the next connection when there is none (or the send fails on a
// stream that is going down), for at most detachedReplyWait or the agent's
// lifetime; then it drops the reply with a log line.
func (a *Agent) sendDetachedReply(ctx context.Context, msg *proto.AgentMessage, requestID string) {
	limit := time.NewTimer(a.replyWait())
	defer limit.Stop()
	for {
		stream, ready := a.currentStream()
		var retry <-chan time.Time
		if stream != nil {
			err := a.sendMessage(stream, msg)
			if err == nil {
				return
			}
			a.log.Warn("Failed to send detached response, will retry", "error", err, "agent_id", a.agentID, "request_id", requestID)
			retry = time.After(time.Second) // the stream is going down; look again shortly
		}
		select {
		case <-ready:
		case <-retry:
		case <-limit.C:
			a.log.Error("Dropping detached response: no stream to send it on", "agent_id", a.agentID, "request_id", requestID)
			return
		case <-ctx.Done():
			a.log.Warn("Dropping detached response: the agent is stopping", "agent_id", a.agentID, "request_id", requestID)
			return
		}
	}
}

func (a *Agent) replyWait() time.Duration {
	if a.detachedWait > 0 {
		return a.detachedWait
	}
	return detachedReplyWait
}

// Run supervises the agent connection for the lifetime of ctx: it connects,
// registers, starts the stream, and waits. When the stream drops (network blip,
// hub restart) it reconnects with exponential backoff. It returns nil when ctx
// is canceled, or ErrRevoked after maxDenials consecutive PermissionDenied
// answers (from bring-up or from a stream the hub cut). Only PermissionDenied
// counts: Unavailable and every other failure retry forever; a stream that
// ends for another reason resets the count.
//
// It asks the API which hub to connect to (migrate) when the hub sends
// hub.move, when the hub answers FailedPrecondition (the server belongs to
// another hub, or the hub is draining), when the hub's certificate is
// refused (ErrHubNotTrusted: not in the pin set, or not a hub's), all at
// most once per askFloor, once the agent has had no stream for
// unreachableAsk, then every unreachableAsk while that lasts, at start
// (before the first connection, unless the API answered within askFloor),
// and, while connected, refreshEvery after the last answered ask (the pin
// set; hub.json's refreshed_at carries that time across restarts). An
// answer naming another hub is stored in hub.json and dialed at once; its
// pin set replaces the stored one; a stay or a failed ask keeps retrying the
// current hub.
func (a *Agent) Run(ctx context.Context) error {
	baseBackoff, maxBackoff := 1*time.Second, 30*time.Second
	if a.retryBackoff != nil {
		baseBackoff, maxBackoff = a.retryBackoff[0], a.retryBackoff[1]
	}
	backoff := baseBackoff
	denials := 0
	settle := streamSettle
	if a.settle > 0 {
		settle = a.settle
	}
	now, sleep := a.now, a.sleep
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = util.Sleep
	}
	bringUp := a.bringUp
	if bringUp == nil {
		bringUp = a.connectRegisterStream
	}

	// refused counts a denial and waits its backoff; false means stop (ctx
	// canceled or given up, with the reason in err).
	refused := func(cause error) (bool, error) {
		denials++
		if denials >= maxDenials {
			a.log.Error("hub refused this server repeatedly, giving up", "error", cause, "denials", denials)
			return false, ErrRevoked
		}
		wait := a.deniedBackoff[min(denials-1, len(a.deniedBackoff)-1)]
		a.log.Warn("hub refused this server, will retry",
			"error", cause, "denials", denials, "retry_in", wait.String())
		return sleep(ctx, wait), nil
	}

	// downSince is when the agent last had no stream (Run's start, or the
	// end of the last one); lastAsk when it last asked the API where to go.
	downSince := now()
	var lastAsk time.Time
	// nextRefresh is when a connected agent asks again for the pin set:
	// refreshEvery after the last answered ask (every ask fetches the set),
	// counted from hub.json's refreshed_at so a restart does not reset it.
	nextRefresh := now().Add(a.refreshEvery())
	if a.hubStore != nil {
		if at := a.hubStore.RefreshedAt(); !at.IsZero() {
			nextRefresh = at.Add(a.refreshEvery())
		}
	}
	// refreshAs asks the API (migrate) and schedules the next refresh.
	refreshAs := func(why string, connected bool) (bool, error) {
		reconnect, err := a.migrate(ctx, why, connected)
		if err == nil {
			nextRefresh = now().Add(a.refreshEvery())
		} else {
			// Try again sooner than a full period.
			nextRefresh = now().Add(min(unreachableAsk, a.refreshEvery()))
		}
		return reconnect, err
	}
	// askAs is refreshAs counted for askFloor.
	askAs := func(why string, connected bool) (bool, error) {
		lastAsk = now()
		return refreshAs(why, connected)
	}
	ask := func(why string) bool {
		reconnect, _ := askAs(why, false)
		return reconnect
	}
	// floorLeft is how long until a hub-prompted ask may run (askFloor
	// after the last ask of any kind).
	floorLeft := func() time.Duration {
		if lastAsk.IsZero() {
			return 0
		}
		return a.floor() - now().Sub(lastAsk)
	}
	// unreachable asks once the hub has been out of reach for
	// unreachableAsk, then every unreachableAsk while it still is.
	unreachable := func() bool {
		if now().Sub(downSince) < unreachableAsk {
			return false
		}
		if !lastAsk.IsZero() && now().Sub(lastAsk) < unreachableAsk {
			return false
		}
		return ask("hub unreachable")
	}

	// The pin set may have changed while the agent was down (a hub
	// certificate retired): ask before the first connection, unless the
	// API answered within askFloor (a restart loop must not hammer it). It
	// does not count for askFloor: a hub prompt right after it may ask
	// again. A failed ask connects with the stored set and retries while
	// connected.
	if a.apiURL != "" && (a.hubStore == nil || now().Sub(a.hubStore.RefreshedAt()) >= a.floor()) {
		_, _ = refreshAs("refreshing the trusted hub certificates at start", false)
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		a.drainMove()

		done, streamErr, err := bringUp(ctx)
		if err != nil {
			a.teardownConn()
			if errors.Is(err, ErrHubNotTrusted) {
				// The hub's certificate is not in the pin set (or not a
				// hub's): the set may be stale. Ask now, at most once per
				// askFloor; a changed set or another hub redials at once.
				if floorLeft() <= 0 && ask("hub certificate refused") {
					backoff = baseBackoff
					continue
				}
				a.log.Warn("agent connection attempt failed, will retry",
					"error", err, "retry_in", backoff.String())
				if !sleep(ctx, backoff) {
					return nil
				}
				backoff = nextBackoff(backoff, maxBackoff)
				continue
			}
			switch status.Code(err) {
			case codes.PermissionDenied:
				ok, rerr := refused(err)
				if !ok {
					return rerr
				}
				continue
			case codes.FailedPrecondition:
				// The hub says this server belongs elsewhere (or it is
				// draining): ask now, at most once per askFloor. Not a
				// denial.
				if floorLeft() <= 0 && ask("hub refused: "+err.Error()) {
					backoff = baseBackoff
					continue
				}
			default:
				if unreachable() {
					backoff = baseBackoff
					continue
				}
			}
			a.log.Warn("agent connection attempt failed, will retry",
				"error", err, "retry_in", backoff.String())
			if !sleep(ctx, backoff) {
				return nil
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		upAt := now()
		// Connected and streaming: wait for the stream to end, for shutdown,
		// or for the hub to tell this agent to move.
		// A hub.move inside askFloor of the last ask waits for the floor
		// (pending), then asks.
		moving := false
		var pending <-chan time.Time
		// refresh fires when the pin set is due for a refresh (only with an
		// API to ask).
		var refresh <-chan time.Time
		var refreshTimer *time.Timer
		armRefresh := func() {
			if refreshTimer != nil {
				refreshTimer.Stop()
			}
			if a.apiURL == "" {
				return
			}
			refreshTimer = time.NewTimer(max(nextRefresh.Sub(now()), 0))
			refresh = refreshTimer.C
		}
		armRefresh()
	wait:
		for {
			select {
			case <-ctx.Done():
				if refreshTimer != nil {
					refreshTimer.Stop()
				}
				return nil
			case <-done:
				break wait
			case <-refresh:
				if reconnect, _ := askAs("refreshing the trusted hub certificates", true); reconnect {
					moving = true
					break wait
				}
				armRefresh()
			case <-a.moveCh:
				if pending != nil {
					continue
				}
				if left := floorLeft(); left > 0 {
					pending = time.After(left)
					continue
				}
				if reconnect, _ := askAs("hub asked to move", true); reconnect {
					moving = true
					break wait
				}
				armRefresh()
			case <-pending:
				pending = nil
				if reconnect, _ := askAs("hub asked to move", true); reconnect {
					moving = true
					break wait
				}
				armRefresh()
			}
		}
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
		if moving {
			a.teardownConn()
			denials = 0
			backoff = baseBackoff
			downSince = now()
			continue
		}
		var endErr error
		if streamErr != nil {
			endErr = streamErr()
		}
		a.teardownConn()
		downSince = now()
		if now().Sub(upAt) >= settle {
			// A stream that stayed up was a real connection: it resets the
			// count even when it ends in a denial (a later revoke).
			denials = 0
		}
		if status.Code(endErr) == codes.PermissionDenied {
			// The hub cut or refused the stream (a revoke): a denial, and
			// the connection is not a success that resets the count.
			ok, rerr := refused(endErr)
			if !ok {
				return rerr
			}
			continue
		}
		denials = 0
		backoff = baseBackoff
		if status.Code(endErr) == codes.FailedPrecondition && floorLeft() <= 0 && ask("hub ended the stream: "+endErr.Error()) {
			continue
		}
		a.log.Warn("agent stream ended, reconnecting", "agent_id", a.agentID, "error", endErr)
		if !sleep(ctx, backoff) {
			return nil
		}
		backoff = nextBackoff(backoff, maxBackoff)
	}
}

// connectRegisterStream performs one full bring-up and returns the stream's
// done channel and the func reporting how it ended. On any step failure it
// returns the error (the caller backs off).
func (a *Agent) connectRegisterStream(ctx context.Context) (<-chan struct{}, func() error, error) {
	a.takePinError() // a refusal from an earlier attempt is not this one's
	if err := a.Connect(ctx); err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	if err := a.Register(ctx); err != nil {
		return nil, nil, a.withPinError(fmt.Errorf("register: %w", err))
	}
	if err := a.StartStream(ctx); err != nil {
		return nil, nil, a.withPinError(fmt.Errorf("start stream: %w", err))
	}
	a.streamMutex.RLock()
	done := a.streamDone
	a.streamMutex.RUnlock()
	return done, func() error {
		a.streamMutex.RLock()
		defer a.streamMutex.RUnlock()
		return a.streamErr
	}, nil
}

// withPinError marks err as ErrHubNotTrusted when the hub's certificate was
// refused during the attempt (gRPC reports the handshake failure as a plain
// Unavailable status).
func (a *Agent) withPinError(err error) error {
	if perr := a.takePinError(); perr != nil {
		return fmt.Errorf("%w (%w)", perr, err)
	}
	return err
}

// teardownConn closes the connection and clears per-connection state so the next
// attempt starts clean.
func (a *Agent) teardownConn() {
	a.streamMutex.Lock()
	if a.streamCancel != nil {
		a.streamCancel()
		a.streamCancel = nil
	}
	a.markDownLocked()
	a.streamMutex.Unlock()

	a.registeredMutex.Lock()
	a.registered = false
	a.registeredMutex.Unlock()

	if a.conn != nil {
		_ = a.conn.Close()
		a.conn = nil
		a.client = nil
	}
}

// nextBackoff doubles cur, capped at max.
func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

func (a *Agent) Stop(ctx context.Context) error {
	a.log.Info("Stopping agent", "agent_id", a.agentID)

	a.streamMutex.Lock()
	if a.streamActive && a.streamCancel != nil {
		a.streamCancel()
		a.markDownLocked()
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
