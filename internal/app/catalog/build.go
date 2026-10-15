// Package catalog builds the catalog feed from a winterflow-apps checkout
// (cmd/catalog): catalog.json, its signature and the logos. Every entry
// passes the same check the API's sync applies (feed.Entry.Check), so a
// feed that builds installs.
package catalog

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	feed "winterflow/internal/domain/catalog"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/template"
)

// Output file names under Options.Out.
const (
	FeedFile = "catalog.json"
	SigFile  = "catalog.json.sig"
	LogoDir  = "logos"
)

// Options is one build.
type Options struct {
	// Apps is the winterflow-apps checkout: apps/<slug>/ and assets/.
	Apps string
	// Out receives catalog.json, catalog.json.sig and logos/.
	Out string
	// BaseURL is where Out is published (https); logo URLs are under it.
	BaseURL string
	// SigningKey signs the feed; nil writes no .sig (a CI check).
	SigningKey ed25519.PrivateKey
	// SourceCommit is the apps checkout's commit ("" when unknown).
	SourceCommit string
	// Now stamps generated_at (nil: time.Now).
	Now func() time.Time
}

// Skip is an apps folder the feed leaves out, and why.
type Skip struct {
	Slug   string
	Reason string
}

// Report is what a build found.
type Report struct {
	// Apps are the slugs in the feed, sorted.
	Apps []string
	// Skipped are the folders that are not installable apps.
	Skipped []Skip
	// Warnings are the template warnings and the missing logos, each
	// prefixed with its slug: printed, never fatal.
	Warnings []string
}

// BuildError lists every app that failed the check: the build writes
// nothing then.
type BuildError struct {
	Problems []string
}

func (e *BuildError) Error() string {
	return fmt.Sprintf("%d apps are invalid:\n  %s", len(e.Problems), strings.Join(e.Problems, "\n  "))
}

// metadata is what the builder reads of apps/<slug>/metadata.json.
type metadata struct {
	Slug             string   `json:"slug"`
	Name             string   `json:"name"`
	Summary          string   `json:"summary"`
	Description      string   `json:"description"`
	Keywords         []string `json:"keywords"`
	Tags             []string `json:"tags"`
	AlternativeTo    []string `json:"alternative_to"`
	License          string   `json:"license"`
	HomepageURL      string   `json:"homepage_url"`
	RepositoryURL    string   `json:"repository_url"`
	DocumentationURL string   `json:"documentation_url"`
	Logo             string   `json:"logo"`
	WaitingList      bool     `json:"waiting_list"`
	Extension        string   `json:"extension"`
}

// logoExtRE is the extension a copied logo keeps.
var logoExtRE = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

// Build reads every apps/<slug>/ of o.Apps and writes the feed. An
// installable app has metadata.json and compose/compose.yml, is not on the
// waiting list and is not an agent extension; any other folder is skipped
// with its reason. Any invalid app fails the build (a *BuildError naming
// every one) before anything is written.
func Build(o Options) (Report, error) {
	base, err := url.Parse(o.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return Report{}, fmt.Errorf("--base-url %q is not an https URL", o.BaseURL)
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	dirs, err := os.ReadDir(filepath.Join(o.Apps, "apps"))
	if err != nil {
		return Report{}, err
	}
	var (
		rep      Report
		problems []string
		entries  []feed.Entry
		logos    = map[string]string{} // output name -> source path
	)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		slug := d.Name()
		e, logo, skip, err := readApp(o.Apps, slug)
		switch {
		case err != nil:
			problems = append(problems, slug+": "+err.Error())
			continue
		case skip != "":
			rep.Skipped = append(rep.Skipped, Skip{Slug: slug, Reason: skip})
			continue
		}
		logoName := ""
		if logo != "" {
			logoName = slug + strings.ToLower(filepath.Ext(logo))
			e.LogoURL = base.JoinPath(LogoDir, logoName).String()
		}
		e.Digest = feed.Digest(e.Files)
		body, _, err := e.Check()
		if err != nil {
			problems = append(problems, slug+": "+err.Error())
			continue
		}
		if logoName == "" {
			rep.Warnings = append(rep.Warnings, slug+": no logo")
		} else {
			logos[logoName] = logo
		}
		spec, _ := template.Parse(body) // Check validated it
		for _, w := range template.Warnings(body, spec, nil) {
			rep.Warnings = append(rep.Warnings, slug+": "+w)
		}
		entries = append(entries, e)
		rep.Apps = append(rep.Apps, slug)
	}
	if len(problems) > 0 {
		return rep, &BuildError{Problems: problems}
	}
	if len(entries) == 0 {
		return rep, errors.New("no installable app")
	}
	slices.SortFunc(entries, func(a, b feed.Entry) int { return strings.Compare(a.Slug, b.Slug) })
	slices.Sort(rep.Apps)
	raw, err := feed.Encode(feed.Feed{
		Format: feed.FormatVersion, SourceCommit: o.SourceCommit,
		GeneratedAt: now().UTC().Truncate(time.Second), Apps: entries,
	})
	if err != nil {
		return rep, err
	}
	return rep, write(o, raw, logos)
}

// readApp reads one apps folder: its entry (without digest and logo URL)
// and its logo's path ("" when it has none), or why it is skipped.
func readApp(root, slug string) (e feed.Entry, logo, skip string, err error) {
	dir := filepath.Join(root, "apps", slug)
	rawMeta, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return feed.Entry{}, "", "no metadata.json", nil
	}
	if err != nil {
		return feed.Entry{}, "", "", err
	}
	composeDir := filepath.Join(dir, "compose")
	compose, err := os.ReadFile(filepath.Join(composeDir, "compose.yml"))
	if errors.Is(err, fs.ErrNotExist) {
		return feed.Entry{}, "", "no compose/compose.yml", nil
	}
	if err != nil {
		return feed.Entry{}, "", "", err
	}
	var m metadata
	if err := json.Unmarshal(rawMeta, &m); err != nil {
		return feed.Entry{}, "", "", fmt.Errorf("metadata.json: %v", err)
	}
	switch {
	case m.WaitingList:
		return feed.Entry{}, "", "on the waiting list", nil
	case m.Extension != "":
		return feed.Entry{}, "", fmt.Sprintf("an agent extension (%s), not an app", m.Extension), nil
	case m.Slug != "" && m.Slug != slug:
		return feed.Entry{}, "", "", fmt.Errorf("metadata.json: slug %q is not the folder name", m.Slug)
	}
	files := map[string]string{"compose.yml": string(compose)}
	spec, err := xwinterflow.Parse(xwinterflow.ModeTemplate, compose)
	if err != nil {
		return feed.Entry{}, "", "", err
	}
	for _, f := range spec.Files {
		b, err := os.ReadFile(filepath.Join(composeDir, filepath.FromSlash(f)))
		if err != nil {
			return feed.Entry{}, "", "", fmt.Errorf("x-winterflow.files: %s: %v", f, errors.Unwrap(err))
		}
		files[f] = string(b)
	}
	if m.Logo != "" && logoExtRE.MatchString(strings.ToLower(path.Ext(m.Logo))) {
		p := filepath.Join(root, "assets", filepath.FromSlash(path.Clean("/"+m.Logo)))
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			logo = p
		}
	}
	return feed.Entry{
		Slug: slug, Name: m.Name, Summary: m.Summary, Description: m.Description,
		Keywords: m.Keywords, Tags: m.Tags, AlternativeTo: m.AlternativeTo, License: m.License,
		HomepageURL: m.HomepageURL, RepositoryURL: m.RepositoryURL, DocumentationURL: m.DocumentationURL,
		Files: files,
	}, logo, "", nil
}

// write puts the feed, its signature and the logos under o.Out; logos/ is
// replaced as a whole, so a dropped app's logo goes with it.
func write(o Options, raw []byte, logos map[string]string) error {
	logoDir := filepath.Join(o.Out, LogoDir)
	if err := os.RemoveAll(logoDir); err != nil {
		return err
	}
	if err := os.MkdirAll(logoDir, 0o755); err != nil {
		return err
	}
	for name, src := range logos {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(logoDir, name), b, 0o644); err != nil {
			return err
		}
	}
	sigPath := filepath.Join(o.Out, SigFile)
	if o.SigningKey != nil {
		if err := os.WriteFile(sigPath, feed.Sign(o.SigningKey, raw), 0o644); err != nil {
			return err
		}
	} else if err := os.Remove(sigPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(filepath.Join(o.Out, FeedFile), raw, 0o644)
}
