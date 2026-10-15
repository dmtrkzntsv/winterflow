package model

import "slices"

// CatalogMeta is what the catalog says about a catalog template besides
// its name and files: the gallery's text, search words and links. The
// sync updates it in place (a fixed description never makes a revision).
type CatalogMeta struct {
	Summary          string   `json:"summary"`
	Description      string   `json:"description"`
	Keywords         []string `json:"keywords"`
	Tags             []string `json:"tags"`
	AlternativeTo    []string `json:"alternative_to" description:"Apps this one replaces, as search words."`
	License          string   `json:"license"`
	HomepageURL      string   `json:"homepage_url"`
	RepositoryURL    string   `json:"repository_url"`
	DocumentationURL string   `json:"documentation_url"`
	LogoURL          string   `json:"logo_url"`
}

// Equal reports whether m and o say the same (a nil list equals an empty
// one).
func (m CatalogMeta) Equal(o CatalogMeta) bool {
	return m.Summary == o.Summary && m.Description == o.Description &&
		slices.Equal(m.Keywords, o.Keywords) && slices.Equal(m.Tags, o.Tags) &&
		slices.Equal(m.AlternativeTo, o.AlternativeTo) && m.License == o.License &&
		m.HomepageURL == o.HomepageURL && m.RepositoryURL == o.RepositoryURL &&
		m.DocumentationURL == o.DocumentationURL && m.LogoURL == o.LogoURL
}

// Normalized is m with every nil list empty, as the API answers it.
func (m CatalogMeta) Normalized() CatalogMeta {
	for _, l := range []*[]string{&m.Keywords, &m.Tags, &m.AlternativeTo} {
		if *l == nil {
			*l = []string{}
		}
	}
	return m
}
