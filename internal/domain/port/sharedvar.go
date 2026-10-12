package port

import (
	"context"

	"winterflow/internal/domain/model"
)

// SharedVariableRepository persists org- and server-level shared variables,
// their per-server secret ciphertexts (the API never holds plaintext) and
// each server's vars_revision.
type SharedVariableRepository interface {
	// ListOrg returns the org-scope variables, sorted by name.
	ListOrg(ctx context.Context, orgID string) ([]model.SharedVariable, error)
	// ListServer returns the server-scope variables of serverID, sorted by name.
	ListServer(ctx context.Context, serverID string) ([]model.SharedVariable, error)
	// Get returns one variable; model.ErrSharedVariableNotFound when unknown.
	Get(ctx context.Context, id string) (model.SharedVariable, error)
	// FindByName looks a name up in one scope (serverID nil = org scope).
	FindByName(ctx context.Context, orgID string, serverID *string, name string) (model.SharedVariable, bool, error)
	// Save writes a variable, its ciphertexts and the revision bumps in one
	// transaction.
	Save(ctx context.Context, s model.SharedVariableSave) error
	// Delete removes a variable (its ciphertexts cascade) and bumps the
	// given servers' revisions, in one transaction.
	Delete(ctx context.Context, id string, bump []string) error
	// CiphertextsForServer maps variable id -> ciphertext for serverID.
	CiphertextsForServer(ctx context.Context, serverID string) (map[string]string, error)
	// DropCiphertexts deletes serverID's copies of the given variables.
	DropCiphertexts(ctx context.Context, serverID string, variableIDs []string) error
	// PurgeServerCiphertexts deletes every ciphertext of serverID (its key
	// changed) and bumps its revision.
	PurgeServerCiphertexts(ctx context.Context, serverID string) error
	// RaiseVarsRevision sets serverID's vars_revision to rev when it is
	// lower (an agent reports a revision the API never issued, e.g. after a
	// database restore); it never lowers it.
	RaiseVarsRevision(ctx context.Context, serverID string, rev int64) error
	// ServerVarsInfo returns the server's org, name and vars_revision;
	// model.ErrServerNotFound when unknown.
	ServerVarsInfo(ctx context.Context, serverID string) (model.ServerVarsInfo, error)
	// OrgServerKeys lists every server of the org in creation order with its
	// public_key capability ("" when it never reported one).
	OrgServerKeys(ctx context.Context, orgID string) ([]model.ServerKey, error)
}
