// Package catalog is the catalog feed: the signed JSON document the
// catalog builder (cmd/catalog) publishes and the API's sync reads. It
// holds the format, the files digest, the ed25519 signature and the check
// every entry must pass, shared by the builder and the sync, so the build
// refuses exactly what an install would skip. No I/O.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
)

// FormatVersion is the feed format this build reads and writes.
const FormatVersion = 1

// ErrInvalid marks a feed or an entry that can't be used; the message
// names the field.
var ErrInvalid = errors.New("invalid catalog")

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Feed is catalog.json.
type Feed struct {
	Format       int       `json:"format"`
	SourceCommit string    `json:"source_commit"`
	GeneratedAt  time.Time `json:"generated_at"`
	Apps         []Entry   `json:"apps"`
}

// Entry is one app of the feed: a catalog template's name, metadata and
// files, with the digest of the files.
type Entry struct {
	Stars            *int                `json:"stars,omitempty"`
	Slug             string              `json:"slug"`
	Name             string              `json:"name"`
	Summary          string              `json:"summary"`
	Description      string              `json:"description"`
	Keywords         []string            `json:"keywords"`
	Tags             []string            `json:"tags"`
	AlternativeTo    []string            `json:"alternative_to"`
	License          string              `json:"license"`
	LicenseURL       string              `json:"license_url,omitempty"`
	Sponsorship      []model.CatalogLink `json:"sponsorship"`
	HomepageURL      string              `json:"homepage_url"`
	RepositoryURL    string              `json:"repository_url"`
	DocumentationURL string              `json:"documentation_url"`
	LogoURL          string              `json:"logo_url"`
	Files            map[string]string   `json:"files"`
	Digest           string              `json:"digest"`
}

// Decode reads catalog.json. Unknown fields are ignored (a later format
// may add some); an unknown format, a missing generated_at, no apps at all
// (a signed empty feed would withdraw every catalog template) or a slug
// listed twice refuse the whole feed. Entries are checked one by one
// (Entry.Check): one bad entry does not refuse the others.
func Decode(data []byte) (Feed, error) {
	var f Feed
	if err := json.Unmarshal(data, &f); err != nil {
		return Feed{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if f.Format != FormatVersion {
		return Feed{}, invalid("format", "%d is not a format this build reads (%d)", f.Format, FormatVersion)
	}
	if f.GeneratedAt.IsZero() {
		return Feed{}, invalid("generated_at", "missing")
	}
	if len(f.Apps) == 0 {
		return Feed{}, invalid("apps", "the feed lists no app")
	}
	seen := make(map[string]bool, len(f.Apps))
	for _, e := range f.Apps {
		if seen[e.Slug] {
			return Feed{}, invalid("apps", "%q is listed twice", e.Slug)
		}
		seen[e.Slug] = true
	}
	return f, nil
}

// Encode is catalog.json as the builder writes it: compact, apps in the
// order given (the builder sorts them by slug), so one apps tree always
// builds the same bytes apart from generated_at.
func Encode(f Feed) ([]byte, error) {
	return json.Marshal(f)
}
