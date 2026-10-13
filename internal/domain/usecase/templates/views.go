package templates

import (
	"time"

	"winterflow/internal/domain/template"
)

// TemplateSummary is one row of GET get-templates.
type TemplateSummary struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	LatestRevision int64     `json:"latest_revision"`
	Instances      int64     `json:"instances" description:"Pending and created instances."`
	Behind         int64     `json:"behind" description:"Instances on a revision older than the latest."`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TemplateList is the data of GET get-templates.
type TemplateList struct {
	Templates []TemplateSummary `json:"templates"`
}

// TemplateRevisionInfo is one entry of a template's revision list.
type TemplateRevisionInfo struct {
	Revision  int64     `json:"revision"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// TemplateView is the data of GET get-template: one revision's body and
// the revision list, newest first.
type TemplateView struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	LatestRevision int64                  `json:"latest_revision"`
	Revision       int64                  `json:"revision"`
	Body           template.Body          `json:"body"`
	Note           string                 `json:"note"`
	CreatedAt      time.Time              `json:"created_at"`
	CreatedBy      string                 `json:"created_by"`
	Revisions      []TemplateRevisionInfo `json:"revisions"`
}

// SavedTemplate is the data of POST save-template.
type SavedTemplate struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	// Warnings name file references nothing provides (a typo, or a
	// shared variable the servers must have).
	Warnings []string `json:"warnings"`
}
