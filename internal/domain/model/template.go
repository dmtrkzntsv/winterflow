package model

import "time"

// Template errors. ErrTemplateNameTaken matches ErrRejected (400 naming
// the field); ErrCatalogReadOnly matches ErrForbidden (403).
var (
	ErrTemplateNotFound     = &classified{"template not found", ErrNotFound}
	ErrTemplateRevisionGone = &classified{"template revision not found", ErrNotFound}
	ErrInstanceNotFound     = &classified{"instance not found", ErrNotFound}
	ErrTemplateNameTaken    = rejected("name: a template with this name already exists")
	ErrRevisionConflict     = &classified{"someone saved the template meanwhile: reload it and save again", ErrConflict}
	ErrTemplateInUse        = &classified{"the template has live instances: delete or unlink them first", ErrConflict}
	ErrKeyInUse             = &classified{"key in use with different values: update the instance instead", ErrConflict}
	ErrKeyRetired           = &classified{"this key belonged to a deleted instance", ErrConflict}
	ErrInstanceNotCreated   = &classified{"the instance was never created: retry the create or delete it", ErrConflict}
	ErrCatalogReadOnly      = &classified{"catalog templates are read-only", ErrForbidden}
	ErrTemplateWithdrawn    = &classified{"this template was withdrawn from the catalog: it takes no new installs", ErrConflict}
)

// Template sources: an org's own template, or one of the catalog (the same
// for every org, read-only to all of them).
const (
	TemplateSourceOrg     = "org"
	TemplateSourceCatalog = "catalog"
)

// UpgradeScopeKey is a template_upgrade rollout's scope key: one active
// upgrade per org and template, so two orgs upgrading the same catalog
// template don't block each other.
func UpgradeScopeKey(orgID, templateID string) string { return orgID + ":" + templateID }

// ManagedByTemplate refuses save-app or rollback-app on an app a live
// (pending or created) instance manages: its changes go through the
// instance. name is the template's ("" when it is gone).
func ManagedByTemplate(name string) error {
	by := "a template"
	if name != "" {
		by = "template " + name
	}
	return &classified{"this app is managed by " + by + ": update the instance or unlink it", ErrConflict}
}

// Template is a stack definition; its body lives in revisions. An org
// template (Source TemplateSourceOrg) belongs to OrgID; a catalog template
// (TemplateSourceCatalog) has no OrgID, a Slug, CatalogMeta (what the
// catalog says about it; nil for an org template) and WithdrawnAt once the
// catalog dropped it.
type Template struct {
	ID             string
	Source         string
	OrgID          string
	Slug           string
	Name           string
	LatestRevision int64
	WithdrawnAt    *time.Time
	CatalogMeta    *CatalogMeta
	CreatedAt      time.Time
	CreatedBy      string
	UpdatedAt      time.Time
}

// TemplateSummary is a template with its live instance counts.
type TemplateSummary struct {
	Template
	// Instances counts the pending and created instances; Behind those of
	// them on a revision older than the latest.
	Instances int64
	Behind    int64
}

// Catalog reports whether t is a catalog template.
func (t Template) Catalog() bool { return t.Source == TemplateSourceCatalog }

// TemplateRevision is one immutable revision: Body is the JSON body;
// Digest identifies a catalog revision's files ("" for an org's).
type TemplateRevision struct {
	TemplateID string
	Revision   int64
	Body       []byte
	Note       string
	Digest     string
	CreatedAt  time.Time
	CreatedBy  string
}

// InstanceState is where an instance is in its life.
type InstanceState string

const (
	// InstancePending: the key is claimed, the save not yet confirmed.
	InstancePending InstanceState = "pending"
	// InstanceCreated: the agent confirmed a save.
	InstanceCreated InstanceState = "created"
	// InstanceDeleted: the app was deleted; the key stays retired.
	InstanceDeleted InstanceState = "deleted"
	// InstanceUnlinked: the app became an ordinary app; the key is free.
	InstanceUnlinked InstanceState = "unlinked"
)

// Instance is an app created from a template revision.
type Instance struct {
	ID       string
	OrgID    string
	Key      string
	AppID    string
	ServerID string
	// TemplateID is "" once the template is gone.
	TemplateID string
	Revision   int64
	// Name is the app name the create asked for.
	Name string
	// Params are the plain variable values given; SecretNames the secret
	// variables a value was given for (never a value).
	Params      map[string]string
	SecretNames []string
	// Hosts are the instance's ingress hostnames.
	Hosts []InstanceHost
	// Backups are the backup settings the create (or a later update while
	// pending) asked for, by name: a pending instance's renders send them
	// again. Once the agent confirmed a save the app's own settings rule.
	Backups     map[string]BackupChoice
	RequestHash string
	State       InstanceState
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// BackupChoice is an instance's setting for one backup its compose file
// declares.
type BackupChoice struct {
	Enabled bool   `json:"enabled"`
	Storage string `json:"storage,omitempty" description:"A storage name on the instance's server; required when enabled."`
}

// InstanceHost is one ingress hostname of an instance: Host routed to the
// http expose entry Expose (<service>:<port>) of the template's compose.
type InstanceHost struct {
	Host   string `json:"host"`
	Expose string `json:"expose"`
}
