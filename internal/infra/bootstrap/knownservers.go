package bootstrap

import (
	"context"
	"errors"
	"sync"
	"time"

	"winterflow/internal/domain/model"
)

const (
	knownServersTTL = time.Minute
	// knownServersMax bounds the cache: ids come off the bus, so a flood of
	// forged ones must not grow it without limit.
	knownServersMax = 4096
)

type serverLookup interface {
	ServerHub(ctx context.Context, serverID string) (string, error)
}

// knownServers answers "is this a server of ours" for the events consumer,
// caching positive and negative answers per id for a minute so a metrics tick
// costs no query.
type knownServers struct {
	lookup serverLookup
	ttl    time.Duration
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]knownEntry
}

type knownEntry struct {
	exists  bool
	expires time.Time
}

func newKnownServers(l serverLookup) *knownServers {
	return &knownServers{lookup: l, ttl: knownServersTTL, now: time.Now, entries: map[string]knownEntry{}}
}

// known reports whether serverID exists. A lookup error is not cached and
// lets the event through (a DB blip must not discard good samples).
func (k *knownServers) known(ctx context.Context, serverID string) bool {
	now := k.now()
	k.mu.Lock()
	e, ok := k.entries[serverID]
	k.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.exists
	}
	_, err := k.lookup.ServerHub(ctx, serverID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return true
	}
	exists := err == nil

	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.entries) >= knownServersMax {
		for id, old := range k.entries {
			if !now.Before(old.expires) {
				delete(k.entries, id)
			}
		}
		if len(k.entries) >= knownServersMax {
			clear(k.entries)
		}
	}
	k.entries[serverID] = knownEntry{exists: exists, expires: now.Add(k.ttl)}
	return exists
}
