package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"winterflow/pkg/agentsig"
)

const (
	// askTimeout bounds one ask of the API.
	askTimeout = 30 * time.Second
	// unreachableAsk is how long the agent goes without its hub before it
	// asks the API where to connect, and the gap between asks after that.
	unreachableAsk = 3 * time.Minute
	// askFloor is the least time between two asks prompted by the hub
	// (FailedPrecondition, hub.move).
	askFloor = 30 * time.Second
	// maxAskBody bounds the API's answer.
	maxAskBody = 64 << 10
)

// hubAddr is the address the next connection dials: the hub in hub.json
// when there is one, else HUB_HOST/HUB_PORT.
func (a *Agent) hubAddr() string {
	if a.hubStore != nil {
		if host, port, ok := a.hubStore.Load(); ok {
			return hubAddress(host, port)
		}
	}
	if a.fallbackHub != nil {
		return a.fallbackHub()
	}
	return hubAddress(a.cfg.GetHubHost(), a.cfg.GetHubPort())
}

// requestMove records that the hub asked this agent to move (hub.move);
// Run asks the API while it waits on the stream. Never blocks: one pending
// request is enough.
func (a *Agent) requestMove() {
	select {
	case a.moveCh <- struct{}{}:
	default:
	}
}

// drainMove drops a move request left over from a connection that is gone.
func (a *Agent) drainMove() {
	select {
	case <-a.moveCh:
	default:
	}
}

// askWhere asks the API which hub to connect to (POST /api/v1/agent/hub,
// signed with the agent key): addr is host:port, moved true when the API
// just moved this server there.
func (a *Agent) askWhere(ctx context.Context) (addr string, moved bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	if a.apiURL == "" {
		return "", false, errors.New("no API URL configured")
	}
	if a.loadKey == nil {
		return "", false, errors.New("no agent key configured")
	}
	key, err := a.loadKey()
	if err != nil {
		return "", false, fmt.Errorf("agent key: %w", err)
	}
	var nb [16]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return "", false, err
	}
	nonce := hex.EncodeToString(nb[:])
	ts := a.clock().Unix()
	sig, err := agentsig.Sign(key, agentsig.Payload(a.agentID, ts, nonce))
	if err != nil {
		return "", false, fmt.Errorf("sign: %w", err)
	}
	body, err := json.Marshal(map[string]any{"server_id": a.agentID, "timestamp": ts, "nonce": nonce})
	if err != nil {
		return "", false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL+"/api/v1/agent/hub", bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(agentsig.Header, sig)
	client := a.httpClient
	if client == nil {
		client = &http.Client{Timeout: askTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAskBody))
	if err != nil {
		return "", false, fmt.Errorf("read answer: %w", err)
	}
	var env struct {
		Message string `json:"message"`
		Data    struct {
			HubAddr string `json:"hub_addr"`
			Moved   bool   `json:"moved"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("API answered %d: %s", resp.StatusCode, env.Message)
	}
	if env.Data.HubAddr == "" {
		return "", false, errors.New("API answered no hub address")
	}
	return env.Data.HubAddr, env.Data.Moved, nil
}

// migrate asks the API where to connect and, when the answer names a hub
// other than the one the agent dials (a move, or a "stay" on a hub the
// agent is not using: a stale hub.json, an answer lost after the API moved
// the server), stores it. It reports whether the agent should reconnect at
// once; a stay on the current hub or a failed ask keeps it (the caller
// retries).
func (a *Agent) migrate(ctx context.Context, why string) bool {
	addr, moved, err := a.askWhere(ctx)
	if err != nil {
		a.log.Warn("asking the API which hub to connect to failed", "reason", why, "error", err)
		return false
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		a.log.Warn("the API answered an unusable hub address", "reason", why, "hub", addr)
		return false
	}
	if hubAddress(host, port) == a.hubAddr() {
		a.log.Info("the API says to stay on the current hub", "reason", why, "hub", addr, "moved", moved)
		return false
	}
	if a.hubStore == nil {
		a.log.Warn("no hub store: cannot move", "hub", addr)
		return false
	}
	if err := a.hubStore.Save(host, port); err != nil {
		a.log.Error("failed to store the new hub; using it until restart", "error", err, "hub", addr)
	}
	a.log.Info("moved to another hub", "reason", why, "hub", addr, "moved", moved)
	return true
}

// floor is the least time between hub-prompted asks (askFloor; tests
// shorten it).
func (a *Agent) floor() time.Duration {
	if a.askFloor > 0 {
		return a.askFloor
	}
	return askFloor
}

func (a *Agent) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}
