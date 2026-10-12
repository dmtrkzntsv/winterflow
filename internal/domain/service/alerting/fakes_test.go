package alerting

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/metrics"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})

// t0 is a minute-aligned instant.
var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

const (
	org = "org-1"
	srv = "srv-1"
	app = "app-1"
)

// fakeStore is an in-memory alert repository with the real
// compare-and-set semantics of Fire and Resolve.
type fakeStore struct {
	mu              sync.Mutex
	rules           []alert.Rule
	overrides       []alert.Override
	states          map[alert.Key]alert.State
	events          []alert.Event
	cleared         []alert.Key
	channels        []alert.Channel
	delivered       map[string][]alert.Delivery
	records         []string // "channel:error"
	dir             alert.Directory
	fail            error // returned by ListRules/ListOverrides
	failTransitions error // returned by Fire and Resolve
}

func newStore() *fakeStore {
	return &fakeStore{
		states:    map[alert.Key]alert.State{},
		delivered: map[string][]alert.Delivery{},
		dir: alert.Directory{
			Servers: map[string]alert.ServerRef{srv: {ID: srv, OrgID: org, Name: "prod-1"}},
			Apps:    map[string]alert.AppRef{app: {ID: app, ServerID: srv, Name: "nextcloud"}},
		},
	}
}

func (s *fakeStore) addRule(r alert.Rule) alert.Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.OrgID, r.Enabled = org, true
	if r.ID == "" {
		r.ID = fmt.Sprintf("rule-%d", len(s.rules)+1)
	}
	s.rules = append(s.rules, r)
	return r
}

func (s *fakeStore) ListRules(_ context.Context, orgID string) ([]alert.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []alert.Rule
	for _, r := range s.rules {
		if r.OrgID == orgID {
			out = append(out, r)
		}
	}
	return out, s.fail
}

func (s *fakeStore) ListOverrides(context.Context, string) ([]alert.Override, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]alert.Override(nil), s.overrides...), nil
}

func (s *fakeStore) FiringStates(context.Context) ([]alert.StateRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []alert.StateRow
	for k, st := range s.states {
		if st != alert.StateFiring {
			continue
		}
		for _, r := range s.rules {
			if r.ID == k.RuleID {
				out = append(out, alert.StateRow{Key: k, TargetKind: r.TargetKind, Metric: r.Metric, State: st})
			}
		}
	}
	return out, nil
}

func (s *fakeStore) Fire(_ context.Context, k alert.Key, _, _ float64, ev alert.Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failTransitions != nil {
		return false, s.failTransitions
	}
	if s.states[k] == alert.StateFiring {
		return false, nil
	}
	s.states[k] = alert.StateFiring
	s.events = append(s.events, ev)
	return true, nil
}

func (s *fakeStore) Resolve(_ context.Context, k alert.Key, _ float64, ev alert.Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failTransitions != nil {
		return false, s.failTransitions
	}
	if s.states[k] != alert.StateFiring {
		return false, nil
	}
	s.states[k] = alert.StateOK
	s.events = append(s.events, ev)
	return true, nil
}

func (s *fakeStore) ClearState(_ context.Context, k alert.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, k)
	s.cleared = append(s.cleared, k)
	return nil
}

func (s *fakeStore) Directory(context.Context) (alert.Directory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir, nil
}

func (s *fakeStore) ListChannels(_ context.Context, orgID string) ([]alert.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []alert.Channel
	for _, c := range s.channels {
		if c.OrgID == orgID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *fakeStore) RecordDelivery(_ context.Context, channelID string, _ time.Time, sendErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, channelID+":"+sendErr)
	return nil
}

func (s *fakeStore) SetDeliveries(_ context.Context, eventID string, ds []alert.Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivered[eventID] = ds
	return nil
}

func (s *fakeStore) eventKinds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, ev := range s.events {
		out = append(out, string(ev.Kind)+":"+string(ev.Metric)+":"+ev.AppID)
	}
	return out
}

// fakeQueue records jobs.
type fakeQueue struct {
	mu   sync.Mutex
	jobs []Job
}

func (q *fakeQueue) Enqueue(j Job) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, j)
	return true
}

func (q *fakeQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

// fakeHistory serves Tier1Range from a fixed slice.
type fakeHistory []metrics.TargetBucket

func (h fakeHistory) Tier1Range(context.Context, time.Time, time.Time) ([]metrics.TargetBucket, error) {
	return h, nil
}

// clock is a settable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// harness is an evaluator over a fake store, queue and SSE recorder.
type harness struct {
	store   *fakeStore
	queue   *fakeQueue
	clock   *clock
	notices []alert.Notice
	ev      *Evaluator
}

func newHarness(store *fakeStore) *harness {
	h := &harness{store: store, queue: &fakeQueue{}, clock: &clock{t: t0}}
	n := 0
	h.ev = NewEvaluator(Config{
		Store: store, Queue: h.queue, WebURL: "https://wf.example", DownGrace: 90 * time.Second, Log: testLog,
		Now:     h.clock.now,
		NewID:   func() string { n++; return fmt.Sprintf("ev-%p-%d", h, n) },
		Publish: func(_ context.Context, _ string, notice alert.Notice) { h.notices = append(h.notices, notice) },
	})
	return h
}

// sample feeds one minute of host CPU (and optionally app values) at the
// clock's time, then advances the clock a minute.
func (h *harness) sample(cpu float64, apps map[string]metrics.Values) {
	h.ev.ObserveMetrics(context.Background(), srv, h.clock.t, metrics.Values{"cpu": cpu, "mem_used": 1e9, "mem_total": 8e9}, apps)
	h.clock.t = h.clock.t.Add(time.Minute)
}

var errStore = errors.New("db down")
