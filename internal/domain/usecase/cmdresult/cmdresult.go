// Package cmdresult records the results of the commands MCP tools dispatch,
// so a tool can wait for its command's outcome and any API replica can
// answer get_command_result later. Only MCP-dispatched commands are
// recorded; REST keeps its SSE path.
//
// The replica that dispatched a command records its outcome (the app
// usecase's observer runs there) and wakes its own waiter through a local
// channel; another replica polls the row once a second.
package cmdresult

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/logger"
)

// Status is where a recorded command stands.
type Status = model.CommandStatus

const (
	Running   = model.CommandRunning
	Succeeded = model.CommandSucceeded
	Failed    = model.CommandFailed
	Unknown   = model.CommandUnknown
)

// Record is a recorded command and its outcome.
type Record = model.CommandRecord

const (
	// MaxResultBytes caps a stored result's JSON (and its error text).
	MaxResultBytes = 1 << 20
	// StaleAfter is how long a command may run before Sweep marks it
	// unknown: past the dispatcher's wait for an observed result
	// (appuc.ObservedResultTTL), no outcome can be recorded any more.
	StaleAfter = 15 * time.Minute
	// Retention is how long an ended record is kept.
	Retention = 24 * time.Hour

	// pollInterval is how often Await rereads the row without a local
	// channel.
	pollInterval = time.Second
	// recordTimeout bounds the DB write a command's result makes.
	recordTimeout = 30 * time.Second
)

// truncatedMarker is what a result too big to shorten is stored as.
var truncatedMarker = []byte(`{"truncated":true}`)

// Store records MCP-dispatched commands and their outcomes.
type Store struct {
	repo port.CommandResultRepository
	log  *logger.Logger
	now  func() time.Time
}

// New builds the store; now defaults to time.Now.
func New(repo port.CommandResultRepository, log *logger.Logger, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{repo: repo, log: log, now: now}
}

// Begin records a dispatched command as running.
func (s *Store) Begin(ctx context.Context, orgID, serverID, kind, requestID string) error {
	return s.repo.Create(ctx, Record{
		RequestID: requestID,
		OrgID:     orgID,
		ServerID:  serverID,
		Kind:      kind,
		Status:    Running,
		CreatedAt: s.now(),
	})
}

// Finish records res as the command's outcome, its result JSON cut to
// MaxResultBytes. A command that already has an outcome keeps it; one
// with no record is left alone.
func (s *Store) Finish(ctx context.Context, requestID string, res port.CommandResult) error {
	rec := outcome(Record{}, res, s.now())
	_, err := s.repo.Finish(ctx, requestID, rec.Status, rec.Result, rec.Error, *rec.FinishedAt)
	return err
}

// Get reads the org's record of a command; another org's, or none, is
// model.ErrNotFound. A record running longer than StaleAfter reads
// unknown at once (Sweep stores that within the hour).
func (s *Store) Get(ctx context.Context, orgID, requestID string) (Record, error) {
	rec, err := s.repo.Get(ctx, orgID, requestID)
	if err == nil && rec.Status == Running && rec.CreatedAt.Before(s.now().Add(-StaleAfter)) {
		rec.Status = Unknown
	}
	return rec, err
}

// Await waits at most bound for the command's outcome. With local (the
// dispatching replica's channel) it waits on that; without, it rereads the
// row every second. At the bound it returns the record still running; when
// ctx ends first, the record read last and ctx's error.
func (s *Store) Await(ctx context.Context, orgID, requestID string, local <-chan port.CommandResult, bound time.Duration) (Record, error) {
	rec, err := s.Get(ctx, orgID, requestID)
	if err != nil || rec.Status != Running {
		return rec, err
	}
	if local != nil {
		res, ok := port.AwaitResult(ctx, local, bound)
		if !ok {
			return rec, ctx.Err()
		}
		return outcome(rec, res, s.now()), nil
	}
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return rec, ctx.Err()
		case <-deadline.C:
			return rec, nil
		case <-tick.C:
		}
		next, err := s.Get(ctx, orgID, requestID)
		if err != nil {
			return rec, err
		}
		if rec = next; rec.Status != Running {
			return rec, nil
		}
	}
}

// Sweep marks commands running longer than StaleAfter unknown and deletes
// records that ended more than Retention ago. It runs hourly.
func (s *Store) Sweep(ctx context.Context) error {
	now := s.now()
	if _, err := s.repo.MarkStale(ctx, now.Add(-StaleAfter), now); err != nil {
		return err
	}
	_, err := s.repo.Prune(ctx, now.Add(-Retention))
	return err
}

// Tracker follows one command an MCP tool dispatches: its Observer goes to
// the app usecase's …Observed method (or a DispatchInput, through
// appuc.Observe), Begin records the request id the dispatch returned, and
// Await waits on the local channel.
//
//	t := store.Track(orgID, serverID, string(command.TypeAppControl))
//	id, err := apps.ControlAppObserved(ctx, userID, serverID, appID, action, t.Observer())
//	// on err: nothing was dispatched
//	err = t.Begin(ctx, id)
//	rec, err := t.Await(ctx, 50*time.Second)
type Tracker struct {
	store                 *Store
	orgID, serverID, kind string
	results               <-chan port.CommandResult
	deliver               func(port.CommandResult)

	mu        sync.Mutex
	requestID string              // set by Begin
	early     *port.CommandResult // a result that came before Begin
}

// Track starts following a command about to be dispatched.
func (s *Store) Track(orgID, serverID, kind string) *Tracker {
	results, deliver := port.NewResultChan()
	return &Tracker{store: s, orgID: orgID, serverID: serverID, kind: kind, results: results, deliver: deliver}
}

// Observer records the command's outcome (bounded, on a background
// context: the hook may run long after the tool call ended) and then
// delivers it to Await. A result that comes before Begin is kept for
// Begin to record.
func (t *Tracker) Observer() appuc.Observer {
	return func(res port.CommandResult) {
		t.mu.Lock()
		id := t.requestID
		if id == "" {
			r := res
			t.early = &r
		}
		t.mu.Unlock()
		if id != "" {
			ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
			if err := t.store.Finish(ctx, id, res); err != nil {
				t.store.log.Error("command result: record the outcome", "error", err, "request_id", id)
			}
			cancel()
		}
		t.deliver(res)
	}
}

// Begin records the dispatched command (requestID is what the dispatch
// returned) as running, and its outcome if that came already.
func (t *Tracker) Begin(ctx context.Context, requestID string) error {
	if err := t.store.Begin(ctx, t.orgID, t.serverID, t.kind, requestID); err != nil {
		return err
	}
	t.mu.Lock()
	t.requestID = requestID
	early := t.early
	t.early = nil
	t.mu.Unlock()
	if early == nil {
		return nil
	}
	return t.store.Finish(ctx, requestID, *early)
}

// RequestID is the id Begin recorded ("" before).
func (t *Tracker) RequestID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.requestID
}

// Await waits at most bound for the outcome on the local channel (see
// Store.Await).
func (t *Tracker) Await(ctx context.Context, bound time.Duration) (Record, error) {
	return t.store.Await(ctx, t.orgID, t.RequestID(), t.results, bound)
}

// outcome is rec with res as its outcome, ended at now.
func outcome(rec Record, res port.CommandResult, now time.Time) Record {
	rec.Status, rec.Error, rec.Result = Succeeded, "", nil
	if !res.Success {
		rec.Status = Failed
		rec.Error = capText(res.Err().Error())
	}
	if len(res.Payload) > 0 {
		rec.Result = truncate(res.Payload)
	}
	rec.FinishedAt = &now
	return rec
}

// capText cuts s to MaxResultBytes on a UTF-8 boundary.
func capText(s string) string {
	if len(s) <= MaxResultBytes {
		return s
	}
	return strings.ToValidUTF8(s[:MaxResultBytes], "")
}

// truncate fits a result's JSON into MaxResultBytes. A JSON object keeps
// its fields and gains "truncated": true; its arrays are emptied, then
// refilled with the tail of each (a log's newest lines), the smallest
// first, each taking at most an even share of the bytes left. Anything
// else, or an object that does not fit even with its arrays empty, is
// stored as {"truncated":true}.
func truncate(raw []byte) json.RawMessage {
	if len(raw) <= MaxResultBytes {
		return raw
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return truncatedMarker
	}
	obj["truncated"] = json.RawMessage("true")
	arrays := make(map[string][]json.RawMessage)
	sizes := make(map[string]int)
	for k, v := range obj {
		var items []json.RawMessage
		if json.Unmarshal(v, &items) == nil && len(items) > 0 {
			arrays[k] = items
			sizes[k] = len(v)
			obj[k] = json.RawMessage("[]")
		}
	}
	base, err := encode(obj)
	if err != nil || len(base) > MaxResultBytes {
		return truncatedMarker
	}
	keys := make([]string, 0, len(arrays))
	for k := range arrays {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if sizes[keys[i]] != sizes[keys[j]] {
			return sizes[keys[i]] < sizes[keys[j]]
		}
		return keys[i] < keys[j]
	})
	// One byte per element for its comma; a re-encoded element is never
	// longer than its source.
	left := MaxResultBytes - len(base)
	for n, k := range keys {
		items := arrays[k]
		share := left / (len(keys) - n)
		spent, keep := 0, 0
		for i := len(items) - 1; i >= 0; i-- {
			if spent+len(items[i])+1 > share {
				break
			}
			spent += len(items[i]) + 1
			keep++
		}
		left -= spent
		if keep == 0 {
			continue
		}
		tail, err := encode(items[len(items)-keep:])
		if err != nil {
			return truncatedMarker
		}
		obj[k] = tail
	}
	out, err := encode(obj)
	if err != nil || len(out) > MaxResultBytes {
		return truncatedMarker
	}
	return out
}

// encode is compact JSON without HTML escaping (json.Marshal would grow
// every '<' in a log line sixfold).
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
