package cmdresult_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/cmdresult"
	"winterflow/internal/infra/db/dbtest"
	cmdresultrepo "winterflow/internal/infra/db/repository/cmdresult"
)

var errDB = errors.New("database is gone")

// flaky fails the named methods with errDB; Get fails only after its
// first getsOK calls when getsOK > 0.
type flaky struct {
	port.CommandResultRepository
	mu     sync.Mutex
	fail   map[string]bool
	getsOK int
	gets   int
}

func (f *flaky) failing(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fail[method]
}

func (f *flaky) set(method string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[method] = on
}

func (f *flaky) Create(ctx context.Context, rec model.CommandRecord) error {
	if f.failing("Create") {
		return errDB
	}
	return f.CommandResultRepository.Create(ctx, rec)
}

func (f *flaky) Finish(ctx context.Context, id string, st model.CommandStatus, res []byte, e string, at time.Time) (bool, error) {
	if f.failing("Finish") {
		return false, errDB
	}
	return f.CommandResultRepository.Finish(ctx, id, st, res, e, at)
}

func (f *flaky) Get(ctx context.Context, orgID, id string) (model.CommandRecord, error) {
	f.mu.Lock()
	f.gets++
	fail := f.getsOK > 0 && f.gets > f.getsOK
	f.mu.Unlock()
	if fail {
		return model.CommandRecord{}, errDB
	}
	return f.CommandResultRepository.Get(ctx, orgID, id)
}

func (f *flaky) MarkStale(ctx context.Context, cutoff, now time.Time) (int64, error) {
	if f.failing("MarkStale") {
		return 0, errDB
	}
	return f.CommandResultRepository.MarkStale(ctx, cutoff, now)
}

func (f *flaky) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	if f.failing("Prune") {
		return 0, errDB
	}
	return f.CommandResultRepository.Prune(ctx, cutoff)
}

func newFlakyStore(t *testing.T) (*cmdresult.Store, *flaky, context.Context) {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := cmdresultrepo.New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	f := &flaky{CommandResultRepository: repo, fail: map[string]bool{}}
	return cmdresult.New(f, dbtest.Log, nil), f, dbtest.Ctx(t)
}

func TestRepoErrorsSurface(t *testing.T) {
	s, f, ctx := newFlakyStore(t)
	f.set("Create", true)
	if err := s.Track("org-1", "srv-1", "app.save").Begin(ctx, "req-1"); !errors.Is(err, errDB) {
		t.Fatalf("begin with a failing insert = %v", err)
	}
	f.set("Create", false)
	for _, m := range []string{"MarkStale", "Prune"} {
		f.set(m, true)
		if err := s.Sweep(ctx); !errors.Is(err, errDB) {
			t.Errorf("sweep with %s failing = %v", m, err)
		}
		f.set(m, false)
	}
	if err := s.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestObserverDeliversWhenRecordFails: the waiter on this replica gets the
// outcome even when recording it failed.
func TestObserverDeliversWhenRecordFails(t *testing.T) {
	s, f, ctx := newFlakyStore(t)
	tr := s.Track("org-1", "srv-1", "app.save")
	if err := tr.Begin(ctx, "req-1"); err != nil {
		t.Fatal(err)
	}
	f.set("Finish", true)
	tr.Observer()(port.CommandResult{Success: true, Payload: []byte(`{"ok":true}`)})
	rec, err := tr.Await(ctx, time.Second)
	if err != nil || rec.Status != cmdresult.Succeeded || string(rec.Result) != `{"ok":true}` {
		t.Fatalf("await = %+v, %v", rec, err)
	}
	if got := get(t, ctx, s, "req-1"); got.Status != cmdresult.Running {
		t.Fatalf("stored = %+v, want still running (the write failed)", got)
	}
}

// TestAwaitPollEndsWithContext: a poll ends with its context, answering
// the record read last.
func TestAwaitPollEndsWithContext(t *testing.T) {
	s, _, ctx := newFlakyStore(t)
	begin(t, ctx, s, "req-1")
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	rec, err := s.Await(cctx, "org-1", "req-1", nil, 10*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) || rec.RequestID != "req-1" || rec.Status != cmdresult.Running {
		t.Fatalf("await = %+v, %v", rec, err)
	}
}

// TestAwaitPollReadError: a failed reread ends the poll with the error and
// the record read before it.
func TestAwaitPollReadError(t *testing.T) {
	s, f, ctx := newFlakyStore(t)
	begin(t, ctx, s, "req-1")
	f.mu.Lock()
	f.getsOK = 1
	f.mu.Unlock()
	rec, err := s.Await(ctx, "org-1", "req-1", nil, 10*time.Second)
	if !errors.Is(err, errDB) || rec.Status != cmdresult.Running {
		t.Fatalf("await = %+v, %v", rec, err)
	}
}

// TestFinishCapsErrorsAndOddResults: an error text is cut to
// MaxResultBytes on a rune boundary; a large result that is not an object
// is stored as the marker; an array whose newest item alone is over the
// cap is emptied.
func TestFinishCapsErrorsAndOddResults(t *testing.T) {
	s, _, ctx := newFlakyStore(t)

	begin(t, ctx, s, "err")
	// "é" is two bytes: a cut at an odd byte would split one.
	long := "x" + strings.Repeat("é", cmdresult.MaxResultBytes/2+10)
	if err := s.Finish(ctx, "err", port.CommandResult{Success: false, Error: long}); err != nil {
		t.Fatal(err)
	}
	got := get(t, ctx, s, "err")
	if got.Status != cmdresult.Failed || len(got.Error) > cmdresult.MaxResultBytes || len(got.Error) < cmdresult.MaxResultBytes-1 ||
		!utf8.ValidString(got.Error) {
		t.Fatalf("stored error: %d bytes, valid %v", len(got.Error), utf8.ValidString(got.Error))
	}

	big := strings.Repeat("y", cmdresult.MaxResultBytes+10)
	arr, _ := json.Marshal([]string{big})
	begin(t, ctx, s, "array")
	if err := s.Finish(ctx, "array", port.CommandResult{Success: true, Payload: arr}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, ctx, s, "array"); string(got.Result) != `{"truncated":true}` {
		t.Fatalf("a top-level array = %.80s", got.Result)
	}

	obj, _ := json.Marshal(map[string]any{"app_id": "a1", "logs": []string{big}})
	begin(t, ctx, s, "object")
	if err := s.Finish(ctx, "object", port.CommandResult{Success: true, Payload: obj}); err != nil {
		t.Fatal(err)
	}
	var cut map[string]any
	if err := json.Unmarshal(get(t, ctx, s, "object").Result, &cut); err != nil {
		t.Fatal(err)
	}
	if cut["app_id"] != "a1" || cut["truncated"] != true || len(cut["logs"].([]any)) != 0 {
		t.Fatalf("an object whose one item is over the cap = %v", cut)
	}
}
