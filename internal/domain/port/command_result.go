package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// CommandResultRepository stores the results of MCP-dispatched commands.
type CommandResultRepository interface {
	// Create inserts a running record.
	Create(ctx context.Context, rec model.CommandRecord) error
	// Finish records an outcome on a running or unknown record (a
	// succeeded or failed one keeps its first outcome); reports whether a
	// row changed.
	Finish(ctx context.Context, requestID string, status model.CommandStatus, result []byte, errText string, finishedAt time.Time) (bool, error)
	// Get reads the org's record; another org's, or none, is
	// model.ErrNotFound.
	Get(ctx context.Context, orgID, requestID string) (model.CommandRecord, error)
	// MarkStale turns running records created before cutoff into unknown
	// ones ended at now.
	MarkStale(ctx context.Context, cutoff, now time.Time) (int64, error)
	// Prune deletes records that ended before cutoff.
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}
