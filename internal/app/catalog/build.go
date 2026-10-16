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

// logoExts are the extensions a published logo may have: images only, so a
// metadata.json cannot make the feed's host serve anything else.
var logoExts = []string{".png", ".svg", ".jpg", ".jpeg", ".webp", ".gif", ".avif"}

// logo is a logo's bytes, read at validation time, and its extension.
type logo struct {
	data []byte
	ext  string
}

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
	if base.User != nil || strings.ContainsAny(o.BaseURL, "?#") {
		return Report{}, fmt.Errorf("--base-url %q must not carry credentials, a query or a fragment", o.BaseURL)
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
		logos    = map[string][]byte{} // output name -> bytes
	)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		slug := d.Name()
		e, lg, skip, err := readApp(o.Apps, slug)
		switch {
		case err != nil:
			problems = append(problems, slug+": "+err.Error())
			continue
		case skip != "":
			rep.Skipped = append(rep.Skipped, Skip{Slug: slug, Reason: skip})
			continue
		}
		logoName := ""
		if lg != nil {
			logoName = slug + lg.ext
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
			logos[logoName] = lg.data
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
// and its logo (nil when it has none), or why it is skipped. Every file is
// read through an os.Root, so a symlink or a ".." cannot reach outside the
// compose folder (or the assets folder, for the logo), and must be a regular
// file.
func readApp(root, slug string) (e feed.Entry, lg *logo, skip string, err error) {
	dir := filepath.Join(root, "apps", slug)
	metaPath := filepath.Join(dir, "metadata.json")
	info, err := os.Lstat(metaPath)
	if errors.Is(err, fs.ErrNotExist) {
		return feed.Entry{}, nil, "no metadata.json", nil
	}
	if err != nil {
		return feed.Entry{}, nil, "", err
	}
	if !info.Mode().IsRegular() {
		return feed.Entry{}, nil, "", errors.New("metadata.json: not a regular file")
	}
	rawMeta, err := os.ReadFile(metaPath)
	if err != nil {
		return feed.Entry{}, nil, "", err
	}
	composeRoot, err := openDir(filepath.Join(dir, "compose"))
	if errors.Is(err, fs.ErrNotExist) {
		return feed.Entry{}, nil, "no compose/compose.yml", nil
	}
	if err != nil {
		return feed.Entry{}, nil, "", fmt.Errorf("compose: %v", err)
	}
	defer composeRoot.Close()
	compose, err := readRegular(composeRoot, "compose.yml")
	if errors.Is(err, fs.ErrNotExist) {
		return feed.Entry{}, nil, "no compose/compose.yml", nil
	}
	if err != nil {
		return feed.Entry{}, nil, "", fmt.Errorf("compose/compose.yml: %v", err)
	}
	var m metadata
	if err := json.Unmarshal(rawMeta, &m); err != nil {
		return feed.Entry{}, nil, "", fmt.Errorf("metadata.json: %v", err)
	}
	switch {
	case m.WaitingList:
		return feed.Entry{}, nil, "on the waiting list", nil
	case m.Extension != "":
		return feed.Entry{}, nil, fmt.Sprintf("an agent extension (%s), not an app", m.Extension), nil
	case m.Slug != "" && m.Slug != slug:
		return feed.Entry{}, nil, "", fmt.Errorf("metadata.json: slug %q is not the folder name", m.Slug)
	}
	files := map[string]string{"compose.yml": string(compose)}
	spec, err := xwinterflow.Parse(xwinterflow.ModeTemplate, compose)
	if err != nil {
		return feed.Entry{}, nil, "", err
	}
	for _, f := range spec.Files {
		b, err := readRegular(composeRoot, filepath.FromSlash(f))
		if err != nil {
			return feed.Entry{}, nil, "", fmt.Errorf("x-winterflow.files: %s: %v", f, err)
		}
		files[f] = string(b)
	}
	if lg, err = readLogo(root, m.Logo); err != nil {
		return feed.Entry{}, nil, "", fmt.Errorf("logo %q: %v", m.Logo, err)
	}
	return feed.Entry{
		Slug: slug, Name: m.Name, Summary: m.Summary, Description: m.Description,
		Keywords: m.Keywords, Tags: m.Tags, AlternativeTo: m.AlternativeTo, License: m.License,
		HomepageURL: m.HomepageURL, RepositoryURL: m.RepositoryURL, DocumentationURL: m.DocumentationURL,
		Files: files,
	}, lg, "", nil
}

// readLogo reads the logo metadata.json names, relative to <root>/assets. No
// logo, a missing file or an extension that is not an image's is (nil, nil);
// a path that leaves assets/, a link out of it or a file that is not a
// regular one is an error.
func readLogo(root, name string) (*logo, error) {
	ext := strings.ToLower(path.Ext(name))
	if name == "" || !slices.Contains(logoExts, ext) {
		return nil, nil
	}
	assets, err := openDir(filepath.Join(root, "assets"))
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNotDir) {
		return nil, nil // no assets folder, or a link standing in for it
	}
	if err != nil {
		return nil, err
	}
	defer assets.Close()
	b, err := readRegular(assets, filepath.FromSlash(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &logo{data: b, ext: ext}, nil
}

var errNotDir = errors.New("not a real directory")

// openDir opens name as a Root, but only when it is a real directory:
// os.OpenRoot itself follows a link at its own path.
func openDir(name string) (*os.Root, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errNotDir
	}
	return os.OpenRoot(name)
}

// readRegular reads a regular file under r. A link that leaves r, a
// directory or a device is refused (not opened: a FIFO would block). The
// error is the bare cause, without the path.
func readRegular(r *os.Root, name string) ([]byte, error) {
	cause := func(err error) error {
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			return pe.Err
		}
		return err
	}
	info, err := r.Stat(name)
	if err != nil {
		return nil, cause(err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	b, err := r.ReadFile(name)
	return b, cause(err)
}

// write puts the logos, the signature and then the feed under o.Out, each
// file by a rename of a temp file beside it, so a reader never sees a half
// written file and the feed, which names the logos and is what the signature
// covers, lands last. Logos the feed no longer lists are removed after it.
func write(o Options, raw []byte, logos map[string][]byte) error {
	logoDir := filepath.Join(o.Out, LogoDir)
	if err := os.MkdirAll(logoDir, 0o755); err != nil {
		return err
	}
	for name, data := range logos {
		if err := writeFile(filepath.Join(logoDir, name), data); err != nil {
			return err
		}
	}
	sigPath := filepath.Join(o.Out, SigFile)
	if o.SigningKey != nil {
		if err := writeFile(sigPath, feed.Sign(o.SigningKey, raw)); err != nil {
			return err
		}
	} else if err := os.Remove(sigPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := writeFile(filepath.Join(o.Out, FeedFile), raw); err != nil {
		return err
	}
	old, err := os.ReadDir(logoDir)
	if err != nil {
		return err
	}
	for _, d := range old {
		if _, keep := logos[d.Name()]; !keep {
			if err := os.RemoveAll(filepath.Join(logoDir, d.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeFile replaces name with data (0644) by renaming a temp file in its
// folder.
func writeFile(name string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Chmod(0o644); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}
