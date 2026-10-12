package port

import (
	"context"
	"time"

	"winterflow/internal/domain/alert"
)

// AlertRepository persists alerting (migration 0013): an org's
// notification channels (URL stored encrypted by the caller), its rules
// and per-target overrides, the state of each (rule, target) and the event
// history. Lookups scoped by org return alert.ErrNotFound for another
// org's ids.
type AlertRepository interface {
	ListChannels(ctx context.Context, orgID string) ([]alert.Channel, error)
	GetChannel(ctx context.Context, orgID, id string) (alert.Channel, error)
	// CreateChannel mints the id and creation time.
	CreateChannel(ctx context.Context, ch alert.Channel) (alert.Channel, error)
	UpdateChannel(ctx context.Context, ch alert.Channel) error
	DeleteChannel(ctx context.Context, orgID, id string) error
	// RecordDelivery stores a send outcome on the channel ("" = success).
	RecordDelivery(ctx context.Context, channelID string, at time.Time, sendErr string) error

	ListRules(ctx context.Context, orgID string) ([]alert.Rule, error)
	GetRule(ctx context.Context, orgID, id string) (alert.Rule, error)
	// CreateRule mints the id and creation time.
	CreateRule(ctx context.Context, r alert.Rule) (alert.Rule, error)
	// UpdateRule rewrites threshold, duration, channels and enabled.
	UpdateRule(ctx context.Context, r alert.Rule) error
	DeleteRule(ctx context.Context, orgID, id string) error
	// SeedDefaults gives every not-yet-seeded org the default rules once.
	SeedDefaults(ctx context.Context) (int, error)

	ListOverrides(ctx context.Context, orgID string) ([]alert.Override, error)
	TargetOverrides(ctx context.Context, t alert.Target) ([]alert.Override, error)
	SaveOverride(ctx context.Context, o alert.Override) error
	DeleteOverride(ctx context.Context, k alert.Key) error
	// ServerOrgID is the org owning a server (alert.ErrNotFound if none).
	ServerOrgID(ctx context.Context, serverID string) (string, error)

	// Fire and Resolve are compare-and-set transitions; won reports
	// whether this call made it (and appended ev).
	Fire(ctx context.Context, k alert.Key, value, threshold float64, ev alert.Event) (won bool, err error)
	Resolve(ctx context.Context, k alert.Key, value float64, ev alert.Event) (won bool, err error)
	ClearState(ctx context.Context, k alert.Key) error
	FiringStates(ctx context.Context) ([]alert.StateRow, error)
	ListActive(ctx context.Context, orgID string) ([]alert.Active, error)
	// CountFiring is the number of firing alerts per server id.
	CountFiring(ctx context.Context) (map[string]int, error)

	ListEvents(ctx context.Context, q alert.EventQuery) ([]alert.Event, error)
	SetDeliveries(ctx context.Context, eventID string, ds []alert.Delivery) error
	// PruneEvents applies the 90-day / 2,000-per-org history bounds.
	PruneEvents(ctx context.Context, now time.Time) (int64, error)

	// Directory lists every server and app with names and owners.
	Directory(ctx context.Context) (alert.Directory, error)
}
