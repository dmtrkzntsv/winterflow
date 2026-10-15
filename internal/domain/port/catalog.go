package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// CatalogRepository is what the catalog sync needs of the templates
// repository. Every write names the generated_at of the feed it comes from
// (feedAt) and is refused for a template a newer feed already wrote, so an
// older feed, on another replica or after a restart, never rolls a
// template back.
type CatalogRepository interface {
	// ListCatalog returns every catalog template's sync state, by slug.
	ListCatalog(ctx context.Context) ([]model.CatalogState, error)
	// ApplyCatalog brings the catalog template of e.Slug to e in one
	// transaction, from base, the state the sync read (nil: the slug is
	// new): a new template with revision 1; a new revision when the digest
	// differs (latest_revision bumped from base's); else the name and
	// metadata in place. Every path clears withdrawn_at. Losing a race (the
	// slug inserted, the revision bumped, or the row written by a newer
	// feed meanwhile) is model.CatalogSkipped, not an error.
	ApplyCatalog(ctx context.Context, base *model.CatalogState, e model.CatalogEntry) (model.CatalogOutcome, error)
	// WithdrawCatalog sets the template's withdrawn_at to at, unless it is
	// withdrawn already or a feed newer than feedAt wrote it, and reports
	// whether it did.
	WithdrawCatalog(ctx context.Context, id string, at, feedAt time.Time) (bool, error)
	// StampCatalog records feedAt as the feed every catalog template
	// reflects (only ever moving it forward).
	StampCatalog(ctx context.Context, feedAt time.Time) error
}
