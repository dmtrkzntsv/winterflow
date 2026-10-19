package catalog

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

// Limits of an entry's fields.
const (
	MaxName     = 255
	MaxListLen  = 50
	MaxListItem = 100
	MaxLicense  = 100
	MaxURL      = 2048
)

// slugRE is a catalog slug (the apps folder name).
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// Check validates e as the builder and the sync both require and returns
// the revision body (files, summary, description) and the metadata a
// catalog template stores. The body must pass the template validation
// (x-winterflow with the template rules, the 1 MiB cap); the digest must
// be the files'.
func (e Entry) Check() (template.Body, model.CatalogMeta, error) {
	if !slugRE.MatchString(e.Slug) {
		return template.Body{}, model.CatalogMeta{}, invalid("slug", "%q is not lowercase letters, digits, - and _, at most 128", e.Slug)
	}
	if e.Name == "" || len(e.Name) > MaxName || strings.TrimSpace(e.Name) != e.Name || strings.ContainsAny(e.Name, "\r\n") {
		return template.Body{}, model.CatalogMeta{}, invalid("name", "one line of 1 to %d characters, no surrounding spaces", MaxName)
	}
	meta := model.CatalogMeta{
		Stars:   e.Stars,
		Summary: e.Summary, Description: e.Description,
		Keywords: e.Keywords, Tags: e.Tags, AlternativeTo: e.AlternativeTo, License: e.License, LicenseURL: e.LicenseURL, Sponsorship: e.Sponsorship,
		HomepageURL: e.HomepageURL, RepositoryURL: e.RepositoryURL, DocumentationURL: e.DocumentationURL, LogoURL: e.LogoURL,
	}.Normalized()
	for _, l := range []struct {
		field string
		list  []string
	}{{"keywords", meta.Keywords}, {"tags", meta.Tags}, {"alternative_to", meta.AlternativeTo}} {
		if err := checkList(l.field, l.list); err != nil {
			return template.Body{}, model.CatalogMeta{}, err
		}
	}
	if len(e.License) > MaxLicense || strings.ContainsAny(e.License, "\r\n") {
		return template.Body{}, model.CatalogMeta{}, invalid("license", "one line of at most %d characters", MaxLicense)
	}
	if e.Stars != nil && (*e.Stars < 0 || *e.Stars > 1_000_000_000) {
		return template.Body{}, model.CatalogMeta{}, invalid("stars", "must be between 0 and 1000000000")
	}
	for _, u := range [][2]string{
		{"homepage_url", e.HomepageURL}, {"repository_url", e.RepositoryURL},
		{"documentation_url", e.DocumentationURL}, {"logo_url", e.LogoURL}, {"license_url", e.LicenseURL},
	} {
		if err := checkURL(u[0], u[1]); err != nil {
			return template.Body{}, model.CatalogMeta{}, err
		}
	}
	if err := checkLinks("sponsorship", meta.Sponsorship); err != nil {
		return template.Body{}, model.CatalogMeta{}, err
	}
	if got := Digest(e.Files); e.Digest != got {
		return template.Body{}, model.CatalogMeta{}, invalid("digest", "%q is not the files' digest (%s)", e.Digest, got)
	}
	body := template.Body{Files: e.Files, Summary: e.Summary, Description: e.Description}
	if _, err := template.Validate(body); err != nil {
		return template.Body{}, model.CatalogMeta{}, err
	}
	if _, err := template.Encode(body); err != nil {
		return template.Body{}, model.CatalogMeta{}, err
	}
	return body, meta, nil
}

func checkList(field string, list []string) error {
	if len(list) > MaxListLen {
		return invalid(field, "at most %d entries", MaxListLen)
	}
	for i, s := range list {
		if s == "" || len(s) > MaxListItem || strings.ContainsAny(s, "\r\n") {
			return invalid(fmt.Sprintf("%s[%d]", field, i), "one line of 1 to %d characters", MaxListItem)
		}
	}
	return nil
}

// checkLinks accepts at most MaxListLen links, each a one-line name of 1 to
// MaxListItem characters and an https URL.
func checkLinks(field string, links []model.CatalogLink) error {
	if len(links) > MaxListLen {
		return invalid(field, "at most %d entries", MaxListLen)
	}
	for i, l := range links {
		f := fmt.Sprintf("%s[%d]", field, i)
		if l.Name == "" || len(l.Name) > MaxListItem || strings.ContainsAny(l.Name, "\r\n") {
			return invalid(f+".name", "one line of 1 to %d characters", MaxListItem)
		}
		if l.URL == "" {
			return invalid(f+".url", "missing")
		}
		if err := checkURL(f+".url", l.URL); err != nil {
			return err
		}
	}
	return nil
}

// checkURL accepts "" or an https URL of at most MaxURL bytes.
func checkURL(field, s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || len(s) > MaxURL || u.Scheme != "https" || u.Host == "" {
		return invalid(field, "%q is not an https URL of at most %d characters", s, MaxURL)
	}
	return nil
}
