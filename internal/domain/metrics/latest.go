package metrics

import (
	"sync"
	"time"
)

// Latest is one server's most recent host sample, for the dashboard meters.
type Latest struct {
	At     time.Time    `json:"at"`
	Server ServerSample `json:"server"`
}

// LatestStore keeps the latest host sample per server in memory. Entries are
// replaced on every sample and dropped by Forget or once older than Prune's
// cutoff, so the map is bounded by the servers reporting recently.
type LatestStore struct {
	mu sync.RWMutex
	m  map[string]Latest
}

// NewLatestStore returns an empty store.
func NewLatestStore() *LatestStore { return &LatestStore{m: map[string]Latest{}} }

// Set records serverID's newest sample.
func (s *LatestStore) Set(serverID string, l Latest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[serverID] = l
}

// Get returns serverID's newest sample.
func (s *LatestStore) Get(serverID string) (Latest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.m[serverID]
	return l, ok
}

// Forget drops serverID (its server was deleted).
func (s *LatestStore) Forget(serverID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, serverID)
}

// Prune drops entries taken before cutoff and reports how many it dropped.
func (s *LatestStore) Prune(cutoff time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, l := range s.m {
		if l.At.Before(cutoff) {
			delete(s.m, id)
			n++
		}
	}
	return n
}
