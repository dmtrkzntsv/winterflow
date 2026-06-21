// Package status holds the in-memory, TTL'd liveness/container-status cache.
//
// Status is ephemeral and never persisted: a server is "online" only while it
// keeps reporting. An entry past its TTL (or absent) is reported as unknown —
// we cannot distinguish a down server from an unreachable one, so we never
// infer "offline" from silence. The cache is rebuilt as agents report; it is
// lost on restart, which is correct for ephemeral status.
package status

import (
	"sync"
	"time"

	"winterflow/internal/domain/command"
)

// Liveness is a server's current reachability.
type Liveness string

const (
	LivenessOnline  Liveness = "online"
	LivenessUnknown Liveness = "unknown"
)

// ServerStatus is the live status of a server (not persisted).
type ServerStatus struct {
	ServerID string   `json:"server_id"`
	Liveness Liveness `json:"liveness"`
}

// serverEntry / appEntry carry an expiry alongside the value.
type serverEntry struct {
	expires time.Time
}

type appEntry struct {
	apps    []command.AppStatus
	expires time.Time
}

// Cache is the per-API-instance status store. Safe for concurrent use.
type Cache struct {
	ttl time.Duration

	mu      sync.RWMutex
	servers map[string]serverEntry // server_id → liveness expiry
	apps    map[string]appEntry    // server_id → app statuses + expiry
}

// NewCache creates a status cache with the given freshness window. Sized to a
// small multiple of the agent heartbeat interval (e.g. 30s heartbeat → 60s ttl).
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl:     ttl,
		servers: make(map[string]serverEntry),
		apps:    make(map[string]appEntry),
	}
}

// MarkOnline records a liveness pulse for a server.
func (c *Cache) MarkOnline(serverID string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.servers[serverID] = serverEntry{expires: now.Add(c.ttl)}
}

// SetAppStatus records the latest container status for a server's apps.
func (c *Cache) SetAppStatus(serverID string, apps []command.AppStatus, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apps[serverID] = appEntry{apps: apps, expires: now.Add(c.ttl)}
	// A status report is also a liveness signal.
	c.servers[serverID] = serverEntry{expires: now.Add(c.ttl)}
}

// ServerLiveness returns online if the server reported within the TTL, else
// unknown.
func (c *Cache) ServerLiveness(serverID string, now time.Time) Liveness {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.servers[serverID]
	if !ok || now.After(e.expires) {
		return LivenessUnknown
	}
	return LivenessOnline
}

// AppStatuses returns the cached container statuses for a server, or nil if
// absent/expired (caller treats nil as unknown).
func (c *Cache) AppStatuses(serverID string, now time.Time) []command.AppStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.apps[serverID]
	if !ok || now.After(e.expires) {
		return nil
	}
	return e.apps
}
