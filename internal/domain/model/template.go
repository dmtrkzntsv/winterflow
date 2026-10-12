package model

import (
	"errors"
	"time"
)

// ErrNotFound marks an unknown template, revision or instance key, or one
// of another org: the API answers 404. ErrConflict marks a request the
// current state refuses: the API answers 409 with the message.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// classified is a sentinel error that also matches its class.
type classified struct {
	msg   string
	class error
}

func (e *classified) Error() string        { return e.msg }
func (e *classified) Is(target error) bool { return target == e.class }

// Template errors. ErrTemplateNameTaken matches ErrRejected (400 naming
// the field).
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
)

// Template is an org's stack definition; its body lives in revisions.
type Template struct {
	ID             string
	OrgID          string
	Name           string
	LatestRevision int64
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

// TemplateRevision is one immutable revision: Body is the JSON body.
type TemplateRevision struct {
	TemplateID string
	Revision   int64
	Body       []byte
	Note       string
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
	// Params are the plain parameter values; SecretNames the secret
	// parameters a value was given for (never a value).
	Params      map[string]string
	SecretNames []string
	RequestHash string
	State       InstanceState
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
