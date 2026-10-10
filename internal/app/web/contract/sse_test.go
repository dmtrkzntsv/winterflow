package contract_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
)

// resultTimeout bounds awaitResult (spec: 5 s).
const resultTimeout = 5 * time.Second

// event is one decoded SSE notification plus its raw JSON.
type event struct {
	model.Notification
	Payload json.RawMessage `json:"payload,omitempty"`
	Raw     []byte          `json:"-"`
}

// stream buffers every event of one SSE connection.
type stream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	events []event
	done   bool
}

// subscribe opens GET /api/v1/sse and returns once the server has
// subscribed the client (the handler flushes headers after AddChannel).
func (c *client) subscribe(t *testing.T) {
	t.Helper()
	if c.stream != nil {
		return
	}
	ctx, cancel := context.WithCancel(c.e.ctx)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.e.srv.URL+"/api/v1/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	validateRequest(t, req)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	validateResponse(t, req, resp.StatusCode, resp.Header, nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("SSE status = %d", resp.StatusCode)
	}
	s := &stream{}
	s.cond = sync.NewCond(&s.mu)
	c.stream = s
	// Every event the stream carried, of any type (server_status and
	// apps_status included), must match the Notification schema.
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, ev := range s.events {
			if err := checkSchema("Notification", ev.Raw); err != nil {
				t.Errorf("SSE event violates the spec: %v", err)
			}
		}
	})
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			raw := []byte(strings.TrimPrefix(line, "data: "))
			var ev event
			_ = json.Unmarshal(raw, &ev)
			ev.Raw = raw
			s.mu.Lock()
			s.events = append(s.events, ev)
			s.cond.Broadcast()
			s.mu.Unlock()
		}
		s.mu.Lock()
		s.done = true
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
}

// awaitResult waits for the operation_result correlated to requestID,
// validates the event against the Notification schema and, on success, its
// payload against the dispatching route's x-operation-result.
func awaitResult(t *testing.T, c *client, requestID string) event {
	t.Helper()
	s := c.stream
	if s == nil {
		t.Fatal("awaitResult on a client without an SSE stream")
	}
	deadline := time.Now().Add(resultTimeout)
	timer := time.AfterFunc(resultTimeout, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer timer.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		for _, ev := range s.events {
			if ev.Type == model.NotificationOperationResult && ev.Ref == requestID {
				validateSchema(t, "Notification", ev.Raw)
				if ev.Status == model.NotificationStatusSuccess {
					key := c.e.routeOfRequest(requestID)
					method, path, _ := strings.Cut(key, " ")
					rt, _, ok := routeFor(method, path)
					if !ok {
						t.Fatalf("request %s was not dispatched through client.accepted", requestID)
					}
					validateSchema(t, resultSchema(t, rt), ev.Payload)
				}
				return ev
			}
		}
		if s.done || time.Now().After(deadline) {
			t.Fatalf("no operation_result for %s within %s", requestID, resultTimeout)
		}
		s.cond.Wait()
	}
}

// succeed dispatches k, awaits its result and requires success; the result
// payload is decoded into v when non-nil.
func (c *client) succeed(t *testing.T, k call, v any) {
	t.Helper()
	ev := awaitResult(t, c, c.accepted(t, k))
	if ev.Status != model.NotificationStatusSuccess {
		t.Fatalf("%s %s: result status %d error %q", k.Method, k.Path, ev.Status, ev.Error)
	}
	if v != nil {
		if err := json.Unmarshal(ev.Payload, v); err != nil {
			t.Fatalf("result payload: %v", err)
		}
	}
}

// failWith dispatches k and requires an error result carrying msg.
func (c *client) failWith(t *testing.T, k call, msg string) {
	t.Helper()
	ev := awaitResult(t, c, c.accepted(t, k))
	if ev.Status != model.NotificationStatusError || ev.Error != msg || len(ev.Payload) != 0 {
		t.Fatalf("%s %s: want error %q, got status %d error %q payload %s", k.Method, k.Path, msg, ev.Status, ev.Error, ev.Payload)
	}
}
