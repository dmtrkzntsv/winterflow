// Package hubs keeps the list of live hubs in Redis: each hub writes
// hub:status:<hub_id> (JSON) with an expiry and renews it; a hub is
// available while its key exists. A hub's drain flag is
// hub:draining:<hub_id>, with no expiry.
package hubs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"

	"github.com/redis/go-redis/v9"
)

const (
	keyPrefix = "hub:status:"
	// drainPrefix keys a hub's drain flag; set and cleared by the
	// operator (winterflow-hub drain / undrain), never expires.
	drainPrefix = "hub:draining:"
	// callTimeout bounds every Redis call the registry makes.
	callTimeout = 3 * time.Second
	scanCount   = 100
)

// Registry is the Redis-backed port.HubRegistry.
type Registry struct {
	rc *redis.Client
}

var _ port.HubRegistry = (*Registry)(nil)

// NewRegistry uses rc, the client the bus already holds (no second
// connection).
func NewRegistry(rc *redis.Client) *Registry {
	return &Registry{rc: rc}
}

func key(hubID string) string { return keyPrefix + hubID }

// Announce writes the hub's status, live for ttl.
func (r *Registry) Announce(ctx context.Context, hub model.Hub, ttl time.Duration) error {
	if hub.ID == "" {
		return errors.New("hub registry: empty hub id")
	}
	body, err := json.Marshal(hub)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := r.rc.Set(ctx, key(hub.ID), body, ttl).Err(); err != nil {
		return fmt.Errorf("hub registry: announce %s: %w", hub.ID, err)
	}
	return nil
}

// Withdraw deletes the hub's status so it leaves the list at once.
func (r *Registry) Withdraw(ctx context.Context, hubID string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := r.rc.Del(ctx, key(hubID)).Err(); err != nil {
		return fmt.Errorf("hub registry: withdraw %s: %w", hubID, err)
	}
	return nil
}

// List returns every live hub: SCAN for the status keys, then one MGET. A
// key that expired between the two, or whose value does not parse, is
// skipped.
func (r *Registry) List(ctx context.Context) ([]model.Hub, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var keys []string
	iter := r.rc.Scan(ctx, 0, keyPrefix+"*", scanCount).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("hub registry: scan: %w", err)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	vals, err := r.rc.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("hub registry: mget: %w", err)
	}
	out := make([]model.Hub, 0, len(vals))
	seen := make(map[string]bool, len(vals))
	for _, v := range vals {
		s, ok := v.(string)
		if !ok {
			continue // expired since the scan
		}
		var h model.Hub
		if json.Unmarshal([]byte(s), &h) != nil || h.ID == "" || seen[h.ID] {
			continue
		}
		seen[h.ID] = true
		out = append(out, h)
	}
	return out, nil
}

// SetDraining sets (on) or deletes (off) the hub's drain flag. The flag has
// no expiry: a drained hub stays drained across restarts until undrained.
func (r *Registry) SetDraining(ctx context.Context, hubID string, on bool) error {
	if hubID == "" {
		return errors.New("hub registry: empty hub id")
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var err error
	if on {
		err = r.rc.Set(ctx, drainPrefix+hubID, "1", 0).Err()
	} else {
		err = r.rc.Del(ctx, drainPrefix+hubID).Err()
	}
	if err != nil {
		return fmt.Errorf("hub registry: set draining %s: %w", hubID, err)
	}
	return nil
}

// IsDraining reports whether the hub's drain flag is set.
func (r *Registry) IsDraining(ctx context.Context, hubID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	n, err := r.rc.Exists(ctx, drainPrefix+hubID).Result()
	if err != nil {
		return false, fmt.Errorf("hub registry: is draining %s: %w", hubID, err)
	}
	return n > 0, nil
}
