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
	"slices"
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
	// pinRefresh is how often a connected agent asks the API again for the
	// hub certificates it trusts, so a retired certificate stops being
	// accepted within this long.
	pinRefresh = 6 * time.Hour
)

// hubAddr is the address the next connection dials: the hub in hub.json
// when there is one, else HUB_HOST/HUB_PORT.
func (a *Agent) hubAddr() string {
	if a.hubStore != nil {
		if host, port, _, ok := a.hubStore.Load(); ok {
			return hubAddress(host, port)
		}
	}
	if a.fallbackHub != nil {
		return a.fallbackHub()
	}
	return hubAddress(a.cfg.GetHubHost(), a.cfg.GetHubPort())
}

// pins is the stored pin set: the fingerprints of the hub certificates the
// API trusts (empty when none is stored).
func (a *Agent) pins() []string {
	if a.hubStore == nil {
		return nil
	}
	_, _, fps, _ := a.hubStore.Load()
	return fps
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

// hubAnswer is the API's answer to an ask.
type hubAnswer struct {
	// Addr is host:port; Moved is true when the API just moved this server
	// there.
	Addr  string
	Moved bool
	// Fingerprints is the set of hub certificates the API trusts, nil when
	// the answer carries none (an API older than the pin set: the stored
	// set is kept). An empty, non-nil set means none is trusted yet: the
	// agent checks the CA and the hub marker only.
	Fingerprints *[]string
}

// askWhere asks the API which hub to connect to (POST /api/v1/agent/hub,
// signed with the agent key).
func (a *Agent) askWhere(ctx context.Context) (hubAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	if a.apiURL == "" {
		return hubAnswer{}, errors.New("no API URL configured")
	}
	if a.loadKey == nil {
		return hubAnswer{}, errors.New("no agent key configured")
	}
	key, err := a.loadKey()
	if err != nil {
		return hubAnswer{}, fmt.Errorf("agent key: %w", err)
	}
	var nb [16]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return hubAnswer{}, err
	}
	nonce := hex.EncodeToString(nb[:])
	ts := a.clock().Unix()
	sig, err := agentsig.Sign(key, agentsig.Payload(a.agentID, ts, nonce))
	if err != nil {
		return hubAnswer{}, fmt.Errorf("sign: %w", err)
	}
	body, err := json.Marshal(map[string]any{"server_id": a.agentID, "timestamp": ts, "nonce": nonce})
	if err != nil {
		return hubAnswer{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL+"/api/v1/agent/hub", bytes.NewReader(body))
	if err != nil {
		return hubAnswer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(agentsig.Header, sig)
	client := a.httpClient
	if client == nil {
		client = &http.Client{Timeout: askTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return hubAnswer{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAskBody))
	if err != nil {
		return hubAnswer{}, fmt.Errorf("read answer: %w", err)
	}
	var env struct {
		Message string `json:"message"`
		Data    struct {
			HubAddr         string    `json:"hub_addr"`
			Moved           bool      `json:"moved"`
			HubFingerprints *[]string `json:"hub_fingerprints"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode != http.StatusOK {
		return hubAnswer{}, fmt.Errorf("API answered %d: %s", resp.StatusCode, env.Message)
	}
	if env.Data.HubAddr == "" {
		return hubAnswer{}, errors.New("API answered no hub address")
	}
	return hubAnswer{Addr: env.Data.HubAddr, Moved: env.Data.Moved, Fingerprints: env.Data.HubFingerprints}, nil
}

// migrate asks the API where to connect and stores the answer: a hub other
// than the one the agent dials (a move, or a "stay" on a hub the agent is
// not using: a stale hub.json, an answer lost after the API moved the
// server) and the pin set, which replaces the stored one. It reports whether
// the agent should reconnect at once (err is nil then):
//   - another hub: always;
//   - a changed pin set while disconnected: the next dial may now pass;
//   - while connected (connected true): when the set is not empty and no
//     longer holds the certificate of the hub the agent is connected to.
//
// A stay with nothing to change keeps the connection. err is the failed ask
// (the caller retries later).
func (a *Agent) migrate(ctx context.Context, why string, connected bool) (bool, error) {
	ans, err := a.askWhere(ctx)
	if err != nil {
		a.log.Warn("asking the API which hub to connect to failed", "reason", why, "error", err)
		return false, err
	}
	host, port, err := net.SplitHostPort(ans.Addr)
	if err != nil || host == "" || port == "" {
		a.log.Warn("the API answered an unusable hub address", "reason", why, "hub", ans.Addr)
		return false, fmt.Errorf("unusable hub address %q", ans.Addr)
	}
	oldPins := a.pins()
	fps := oldPins
	if ans.Fingerprints != nil {
		fps = normalizeFingerprints(*ans.Fingerprints)
	}
	setChanged := !slices.Equal(normalizeFingerprints(oldPins), fps)
	sameHub := hubAddress(host, port) == a.hubAddr()
	if sameHub && !setChanged {
		a.log.Info("the API says to stay on the current hub", "reason", why, "hub", ans.Addr, "moved", ans.Moved)
		return false, nil
	}
	if a.hubStore == nil {
		a.log.Warn("no hub store: cannot store the API's answer", "hub", ans.Addr)
		return false, nil
	}
	if err := a.hubStore.Save(host, port, fps); err != nil {
		a.log.Error("failed to store the API's answer; using it until restart", "error", err, "hub", ans.Addr)
	}
	if !sameHub {
		a.log.Info("moved to another hub", "reason", why, "hub", ans.Addr, "moved", ans.Moved)
		return true, nil
	}
	a.log.Info("updated the trusted hub certificates", "reason", why, "hub", ans.Addr, "count", len(fps))
	if !connected {
		return true, nil
	}
	peer := a.peerFingerprint()
	if len(fps) > 0 && peer != "" && !slices.Contains(fps, peer) {
		a.log.Warn("the connected hub's certificate is no longer trusted, reconnecting", "hub", ans.Addr, "fingerprint", peer)
		return true, nil
	}
	return false, nil
}

// floor is the least time between hub-prompted asks (askFloor; tests
// shorten it).
func (a *Agent) floor() time.Duration {
	if a.askFloor > 0 {
		return a.askFloor
	}
	return askFloor
}

// refreshEvery is the gap between pin-set refreshes while connected
// (pinRefresh; tests shorten it).
func (a *Agent) refreshEvery() time.Duration {
	if a.pinRefresh > 0 {
		return a.pinRefresh
	}
	return pinRefresh
}

func (a *Agent) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}
