package templates

import (
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

// TemplateSummary is one row of GET get-templates.
type TemplateSummary struct {
	ID             string     `json:"id"`
	Source         string     `json:"source" enum:"org,catalog" description:"org: the organization's own (editable); catalog: the same for every organization (read-only)."`
	Slug           string     `json:"slug" description:"A catalog template's id in the catalog; empty for an org template."`
	Name           string     `json:"name"`
	LatestRevision int64      `json:"latest_revision"`
	Instances      int64      `json:"instances" description:"The org's pending and created instances."`
	Behind         int64      `json:"behind" description:"Of those, the ones on a revision older than the latest."`
	WithdrawnAt    *time.Time `json:"withdrawn_at" description:"When the catalog dropped the template (listed only while the org has instances of it); null otherwise."`
	// Catalog is what the catalog says about a catalog template; null for
	// an org template.
	Catalog   *model.CatalogMeta `json:"catalog" description:"What the catalog says about a catalog template (summary, search words, links, logo); null for an org template."`
	UpdatedAt time.Time          `json:"updated_at"`
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

// TemplateView is the data of GET get-template: one revision's body, what
// its compose files declare, and the revision list, newest first.
type TemplateView struct {
	ID             string     `json:"id"`
	Source         string     `json:"source" enum:"org,catalog"`
	Slug           string     `json:"slug"`
	Name           string     `json:"name"`
	LatestRevision int64      `json:"latest_revision"`
	WithdrawnAt    *time.Time `json:"withdrawn_at" description:"When the catalog dropped the template (it takes no new installs); null otherwise."`
	// Catalog is what the catalog says about a catalog template; null for
	// an org template. A catalog template's current summary and description
	// are catalog.*; body.* are frozen at the revision.
	Catalog   *model.CatalogMeta     `json:"catalog" description:"What the catalog says about a catalog template; null for an org template."`
	Revision  int64                  `json:"revision"`
	Body      template.Body          `json:"body"`
	Detected  template.Detected      `json:"detected" description:"What the revision's compose files declare under x-winterflow."`
	Note      string                 `json:"note"`
	CreatedAt time.Time              `json:"created_at"`
	CreatedBy string                 `json:"created_by"`
	Revisions []TemplateRevisionInfo `json:"revisions"`
}

// SavedTemplate is the data of POST save-template.
type SavedTemplate struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	// Warnings are what the save points out: a variable nothing uses, a
	// file reference nothing provides (a typo, or a shared variable the
	// servers must have).
	Warnings []string `json:"warnings"`
}

// Inspection is the data of POST inspect-template.
type Inspection struct {
	Detected template.Detected `json:"detected" description:"What the compose files declare, as far as they parse."`
	Problem  string            `json:"problem" description:"The first thing save-template would refuse; empty when none."`
}
