package cmdresult_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/cmdresult"
	"winterflow/internal/infra/db/dbtest"
	cmdresultrepo "winterflow/internal/infra/db/repository/cmdresult"
)

// clock is a settable time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newStore(t *testing.T) (*cmdresult.Store, *clock, context.Context) {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := cmdresultrepo.New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	return cmdresult.New(repo, dbtest.Log, c.Now), c, dbtest.Ctx(t)
}

func begin(t *testing.T, ctx context.Context, s *cmdresult.Store, requestID string) {
	t.Helper()
	if err := s.Begin(ctx, "org-1", "srv-1", "app.save", requestID); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, ctx context.Context, s *cmdresult.Store, requestID string) cmdresult.Record {
	t.Helper()
	rec, err := s.Get(ctx, "org-1", requestID)
	if err != nil {
		t.Fatalf("get %s: %v", requestID, err)
	}
	return rec
}

func TestAwaitLocal(t *testing.T) {
	s, _, ctx := newStore(t)
	tr := s.Track("org-1", "srv-1", "app.save")
	if err := tr.Begin(ctx, "req-1"); err != nil {
		t.Fatal(err)
	}
	observe := tr.Observer()
	go observe(port.CommandResult{Success: true, Payload: []byte(`{"app_id":"a1"}`)})

	start := time.Now()
	rec, err := tr.Await(ctx, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 900*time.Millisecond {
		t.Fatalf("local result took %v: it was polled, not delivered", time.Since(start))
	}
	if rec.Status != cmdresult.Succeeded || string(rec.Result) != `{"app_id":"a1"}` || rec.FinishedAt == nil {
		t.Fatalf("record = %+v", rec)
	}
	// The observer recorded the outcome for other replicas too.
	if got := get(t, ctx, s, "req-1"); got.Status != cmdresult.Succeeded || string(got.Result) != `{"app_id":"a1"}` {
		t.Fatalf("stored = %+v", got)
	}
}

func TestAwaitLocalFailure(t *testing.T) {
	s, _, ctx := newStore(t)
	tr := s.Track("org-1", "srv-1", "app.control")
	if err := tr.Begin(ctx, "req-1"); err != nil {
		t.Fatal(err)
	}
	tr.Observer()(port.CommandResult{Success: false, Error: "compose up failed"})
	rec, err := tr.Await(ctx, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != cmdresult.Failed || rec.Error != "compose up failed" || rec.Result != nil {
		t.Fatalf("record = %+v", rec)
	}
	if got := get(t, ctx, s, "req-1"); got.Status != cmdresult.Failed || got.Error != "compose up failed" {
		t.Fatalf("stored = %+v", got)
	}
}

// A result that arrives before Begin recorded the row (the in-process
// bridge can answer before Dispatch returns) is recorded by Begin.
func TestTrackResultBeforeBegin(t *testing.T) {
	s, _, ctx := newStore(t)
	tr := s.Track("org-1", "srv-1", "app.get")
	tr.Observer()(port.CommandResult{Success: true, Payload: []byte(`{}`)})
	if err := tr.Begin(ctx, "req-1"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, ctx, s, "req-1"); got.Status != cmdresult.Succeeded {
		t.Fatalf("stored = %+v", got)
	}
	rec, err := tr.Await(ctx, time.Second)
	if err != nil || rec.Status != cmdresult.Succeeded {
		t.Fatalf("await = %+v, %v", rec, err)
	}
}

func TestAwaitPollsRow(t *testing.T) {
	s, _, ctx := newStore(t)
	begin(t, ctx, s, "req-1")
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if err := s.Finish(context.Background(), "req-1", port.CommandResult{Success: true, Payload: []byte(`{"ok":true}`)}); err != nil {
			t.Error(err)
		}
	}()
	start := time.Now()
	rec, err := s.Await(ctx, "org-1", "req-1", nil, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != cmdresult.Succeeded || string(rec.Result) != `{"ok":true}` {
		t.Fatalf("record = %+v", rec)
	}
	if el := time.Since(start); el < 1500*time.Millisecond || el > 5*time.Second {
		t.Fatalf("returned after %v", el)
	}
}

func TestAwaitBoundReturnsRunning(t *testing.T) {
	s, _, ctx := newStore(t)
	begin(t, ctx, s, "req-1")

	start := time.Now()
	rec, err := s.Await(ctx, "org-1", "req-1", nil, 1200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != cmdresult.Running || rec.RequestID != "req-1" || rec.Kind != "app.save" {
		t.Fatalf("record = %+v", rec)
	}
	if el := time.Since(start); el < 1200*time.Millisecond || el > 4*time.Second {
		t.Fatalf("polled wait returned after %v", el)
	}

	// With a local channel that never delivers, the bound holds too.
	tr := s.Track("org-1", "srv-1", "app.logs")
	if err := tr.Begin(ctx, "req-2"); err != nil {
		t.Fatal(err)
	}
	rec, err = tr.Await(ctx, 300*time.Millisecond)
	if err != nil || rec.Status != cmdresult.Running {
		t.Fatalf("local wait = %+v, %v", rec, err)
	}
}

func TestGetOtherOrgNotFound(t *testing.T) {
	s, _, ctx := newStore(t)
	begin(t, ctx, s, "req-1")
	if _, err := s.Get(ctx, "org-2", "req-1"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("other org's get = %v, want ErrNotFound", err)
	}
	if _, err := s.Await(ctx, "org-2", "req-1", nil, time.Second); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("other org's await = %v, want ErrNotFound", err)
	}
	if _, err := s.Get(ctx, "org-1", "nope"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing = %v, want ErrNotFound", err)
	}
}

func TestSweepMarksUnknownAndPrunes(t *testing.T) {
	s, c, ctx := newStore(t)
	begin(t, ctx, s, "stale")
	c.Advance(16 * time.Minute)
	begin(t, ctx, s, "fresh")
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := get(t, ctx, s, "stale"); got.Status != cmdresult.Unknown || got.FinishedAt == nil {
		t.Fatalf("stale = %+v, want unknown", got)
	}
	if got := get(t, ctx, s, "fresh"); got.Status != cmdresult.Running {
		t.Fatalf("fresh = %+v, want running", got)
	}
	// A late result still lands on an unknown row.
	if err := s.Finish(ctx, "stale", port.CommandResult{Success: true}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, ctx, s, "stale"); got.Status != cmdresult.Succeeded {
		t.Fatalf("stale after its result = %+v", got)
	}
	if err := s.Finish(ctx, "fresh", port.CommandResult{Success: true}); err != nil {
		t.Fatal(err)
	}
	// A second outcome never replaces the first.
	if err := s.Finish(ctx, "fresh", port.CommandResult{Success: false, Error: "late"}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, ctx, s, "fresh"); got.Status != cmdresult.Succeeded || got.Error != "" {
		t.Fatalf("fresh after a second outcome = %+v", got)
	}

	c.Advance(23 * time.Hour)
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	get(t, ctx, s, "stale") // ended 23 h ago: kept
	get(t, ctx, s, "fresh")

	c.Advance(2 * time.Hour)
	begin(t, ctx, s, "running")
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"stale", "fresh"} {
		if _, err := s.Get(ctx, "org-1", id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("%s ended 25 h ago: get = %v, want pruned", id, err)
		}
	}
	if got := get(t, ctx, s, "running"); got.Status != cmdresult.Running {
		t.Fatalf("running = %+v", got)
	}
}

func TestFinishTruncates(t *testing.T) {
	s, _, ctx := newStore(t)
	begin(t, ctx, s, "logs")
	var resp command.GetLogsResponse
	resp.AppID = "a1"
	line := strings.Repeat("x", 1000)
	for i := range 3000 { // ~3 MiB
		resp.Logs = append(resp.Logs, command.LogEntry{Timestamp: int64(i), Message: line})
	}
	raw, _ := json.Marshal(resp)
	if err := s.Finish(ctx, "logs", port.CommandResult{Success: true, Payload: raw}); err != nil {
		t.Fatal(err)
	}
	got := get(t, ctx, s, "logs")
	if len(got.Result) > cmdresult.MaxResultBytes {
		t.Fatalf("stored %d bytes, cap %d", len(got.Result), cmdresult.MaxResultBytes)
	}
	var cut struct {
		command.GetLogsResponse
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(got.Result, &cut); err != nil {
		t.Fatalf("truncated result is not JSON: %v", err)
	}
	if !cut.Truncated || cut.AppID != "a1" || len(cut.Logs) == 0 || len(cut.Logs) >= len(resp.Logs) {
		t.Fatalf("truncated: %v, app %q, %d of %d logs", cut.Truncated, cut.AppID, len(cut.Logs), len(resp.Logs))
	}
	// The tail is kept: the newest lines matter.
	if last := cut.Logs[len(cut.Logs)-1]; last.Timestamp != int64(len(resp.Logs)-1) {
		t.Fatalf("last kept line %d", last.Timestamp)
	}
	// Close to the cap, not a token remnant.
	if len(got.Result) < cmdresult.MaxResultBytes-4096 {
		t.Fatalf("kept only %d bytes", len(got.Result))
	}

	// Raw '<' (an agent's encoder need not escape it) stays one byte.
	begin(t, ctx, s, "html")
	var b strings.Builder
	b.WriteString(`{"logs":[`)
	for i := range 3000 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + strings.Repeat("<", 1000) + `"`)
	}
	b.WriteString(`]}`)
	if err := s.Finish(ctx, "html", port.CommandResult{Success: true, Payload: []byte(b.String())}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, ctx, s, "html"); len(got.Result) > cmdresult.MaxResultBytes || len(got.Result) < cmdresult.MaxResultBytes-4096 || !bytes.Contains(got.Result, []byte("<<")) {
		t.Fatalf("html result: %d bytes, escaped %v", len(got.Result), bytes.Contains(got.Result, []byte(`\u003c`)))
	}

	// A big result with no array to shorten keeps only the marker.
	begin(t, ctx, s, "blob")
	blob, _ := json.Marshal(map[string]string{"data": strings.Repeat("y", 2<<20)})
	if err := s.Finish(ctx, "blob", port.CommandResult{Success: true, Payload: blob}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, ctx, s, "blob"); !bytes.Equal(got.Result, []byte(`{"truncated":true}`)) {
		t.Fatalf("blob stored as %.100s", got.Result)
	}

	// A small result is stored as sent.
	begin(t, ctx, s, "small")
	if err := s.Finish(ctx, "small", port.CommandResult{Success: true, Payload: []byte(`{"a": [1, 2]}`)}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, ctx, s, "small"); string(got.Result) != `{"a": [1, 2]}` {
		t.Fatalf("small stored as %s", got.Result)
	}
}
