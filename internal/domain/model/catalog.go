package model

import (
	"slices"
	"time"
)

// CatalogMeta is what the catalog says about a catalog template besides
// its name and files: the gallery's text, search words and links. The
// sync updates it in place (a fixed description never makes a revision).
type CatalogMeta struct {
	Stars            *int     `json:"stars,omitempty"`
	Summary          string   `json:"summary"`
	Description      string   `json:"description"`
	Keywords         []string `json:"keywords"`
	Tags             []string `json:"tags"`
	AlternativeTo    []string `json:"alternative_to" description:"Apps this one replaces, as search words."`
	License          string   `json:"license"`
	LicenseURL       string   `json:"license_url"`
	SponsorshipURL   string   `json:"sponsorship_url,omitempty"`
	HomepageURL      string   `json:"homepage_url"`
	RepositoryURL    string   `json:"repository_url"`
	DocumentationURL string   `json:"documentation_url"`
	LogoURL          string   `json:"logo_url"`
}

// Equal reports whether m and o say the same (a nil list equals an empty
// one).
func (m CatalogMeta) Equal(o CatalogMeta) bool {
	return equalInt(m.Stars, o.Stars) && m.Summary == o.Summary && m.Description == o.Description &&
		slices.Equal(m.Keywords, o.Keywords) && slices.Equal(m.Tags, o.Tags) &&
		slices.Equal(m.AlternativeTo, o.AlternativeTo) && m.License == o.License &&
		m.HomepageURL == o.HomepageURL && m.RepositoryURL == o.RepositoryURL &&
		m.DocumentationURL == o.DocumentationURL && m.LogoURL == o.LogoURL && m.LicenseURL == o.LicenseURL && m.SponsorshipURL == o.SponsorshipURL
}

func equalInt(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// Normalized is m with every nil list empty, as the API answers it.
func (m CatalogMeta) Normalized() CatalogMeta {
	for _, l := range []*[]string{&m.Keywords, &m.Tags, &m.AlternativeTo} {
		if *l == nil {
			*l = []string{}
		}
	}
	return m
}

// CatalogState is what the catalog sync reads of one catalog template.
type CatalogState struct {
	ID             string
	Slug           string
	Name           string
	Meta           CatalogMeta
	LatestRevision int64
	// Digest is the latest revision's.
	Digest      string
	WithdrawnAt *time.Time
	// FeedAt is the generated_at of the newest feed the sync applied to
	// the template; nil when no sync wrote it.
	FeedAt *time.Time
}

// CatalogEntry is one valid feed entry as the sync applies it.
type CatalogEntry struct {
	Slug string
	Name string
	Meta CatalogMeta
	// Body is the encoded revision body (files, summary, description).
	Body   []byte
	Digest string
	// FeedAt is the feed's generated_at.
	FeedAt time.Time
}

// CatalogOutcome is what applying one entry did.
type CatalogOutcome string

const (
	// CatalogInserted: a new catalog template, revision 1.
	CatalogInserted CatalogOutcome = "inserted"
	// CatalogRevised: the files changed, a new revision.
	CatalogRevised CatalogOutcome = "revised"
	// CatalogUpdated: the name or metadata changed in place, or a
	// withdrawn template came back.
	CatalogUpdated CatalogOutcome = "updated"
	// CatalogSkipped: another replica, or a newer feed, wrote it first.
	CatalogSkipped CatalogOutcome = "skipped"
)

// CatalogFetch is one fetch of the feed and its signature.
type CatalogFetch struct {
	// NotModified: the feed still has the ETag asked with (a 304); Feed
	// and Sig are nil then.
	NotModified bool
	// Feed is catalog.json's exact bytes, Sig catalog.json.sig's.
	Feed, Sig []byte
	// ETag is the feed's, to ask with next time ("" when it sent none).
	ETag string
}
