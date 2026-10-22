package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// AuthorizeTimeout is how long the hub waits for the API's answer to an
// agent authentication check.
const AuthorizeTimeout = 5 * time.Second

// Authorizer decides whether an agent presenting a certificate with the given
// fingerprint may connect as serverID. An error means no answer could be had
// (the caller treats it as "try again later").
type Authorizer interface {
	Authorize(ctx context.Context, serverID, fingerprint string) (bus.AgentVerdict, string, error)
}

// authReply is the verdict carried from the reply subscriber to the waiter.
type authReply struct {
	verdict bus.AgentVerdict
	reason  string
}

// BusAuthorizer asks the API over the bus: it publishes a bus.HubRequest on
// the hub request channel and waits for the bus.HubResponse with the same
// request id on this hub's response channel.
type BusAuthorizer struct {
	bus     bus.Bus
	cfg     *config.ServerConfig
	hubID   string
	timeout time.Duration
	log     *logger.Logger

	mu      sync.Mutex
	started bool
	pending map[string]chan authReply
}

// NewBusAuthorizer builds the authorizer; Start must run before Authorize can
// receive an answer.
func NewBusAuthorizer(b bus.Bus, cfg *config.ServerConfig, hubID string, timeout time.Duration, log *logger.Logger) *BusAuthorizer {
	return &BusAuthorizer{
		bus:     b,
		cfg:     cfg,
		hubID:   hubID,
		timeout: timeout,
		log:     log,
		pending: make(map[string]chan authReply),
	}
}

// Start subscribes this hub's response channel once; replies are routed to
// their waiters until ctx is done. Replies nobody waits for (late, duplicate
// or foreign) are dropped.
func (a *BusAuthorizer) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return errors.New("authorizer already started")
	}
	a.started = true
	a.mu.Unlock()

	channel := config.HubResponsesChannel(a.hubID)
	msgs, cancel, err := a.bus.Subscribe(ctx, channel)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", channel, err)
	}
	go func() {
		defer func() { _ = cancel() }()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgs:
				if !ok {
					return
				}
				var resp bus.HubResponse
				if err := json.Unmarshal([]byte(msg.Payload), &resp); err != nil {
					a.log.Error("failed to unmarshal hub response", err)
					continue
				}
				a.deliver(resp)
			}
		}
	}()
	return nil
}

// deliver hands a reply to its waiter without blocking: the channel holds
// one reply, and any further one for the same id is dropped.
func (a *BusAuthorizer) deliver(resp bus.HubResponse) {
	a.mu.Lock()
	ch, ok := a.pending[resp.RequestID]
	a.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- authReply{verdict: resp.Verdict, reason: resp.Reason}:
	default:
	}
}

// Authorize publishes a server.verify request and waits for its reply, at
// most the configured timeout or until ctx ends.
func (a *BusAuthorizer) Authorize(ctx context.Context, serverID, fingerprint string) (bus.AgentVerdict, string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	id := util.GenerateID()
	ch := make(chan authReply, 1)
	a.mu.Lock()
	a.pending[id] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
	}()

	req := bus.HubRequest{
		Type:            bus.HubRequestVerify,
		RequestID:       id,
		HubID:           a.hubID,
		ServerID:        serverID,
		CertFingerprint: fingerprint,
	}
	if err := a.bus.Publish(ctx, a.cfg.GetHubRequestsChannel(), req); err != nil {
		return "", "", fmt.Errorf("publish agent auth request: %w", err)
	}
	select {
	case r := <-ch:
		return r.verdict, r.reason, nil
	case <-ctx.Done():
		return "", "", fmt.Errorf("no agent auth reply: %w", ctx.Err())
	}
}
