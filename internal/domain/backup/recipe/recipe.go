// Package recipe parses and validates backups: the recipe a compose file
// declares under x-winterflow.backups (what can be backed up), the overrides
// an app keeps in .winterflow/config.json under "backups" (per-deployment
// settings and backups added in the UI), and the effective backups the two
// make together. Both the API (400 at save when the compose file is in the
// request) and the agent (at save and deploy) use it; resolving paths to
// Docker mounts is the agent's job.
package recipe

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"winterflow/internal/domain/model"
)

// Type is a backup type.
type Type string

const (
	TypeSQLite Type = "sqlite" // one SQLite database file, copied with VACUUM INTO
	TypeFiles  Type = "files"  // folders and files, archived with tar
	TypeCustom Type = "custom" // the app's own tool, run as a service behind a profile
)

// Manual is the every value of a backup that is never scheduled: it runs
// only through Back up now.
const Manual = "manual"

// Defaults and limits of the schedule and the retention.
const (
	DefaultEvery   = "1h"
	MinEvery       = 5 * time.Minute
	DefaultRecent  = 24 * time.Hour
	DefaultDaily   = 14
	DefaultMonthly = 6
	maxTier        = 3650
	maxPathLen     = 1024
	maxListLen     = 64
)

// ProfilePrefix starts the compose profile of a custom backup:
// wf-backup-<backup name>. Enabling the backup activates it.
const ProfilePrefix = "wf-backup-"

// Profile is the compose profile a custom backup's service must list.
func Profile(name string) string { return ProfilePrefix + name }

// ErrInvalid marks a recipe or override that can't be used; the message
// names the field (x-winterflow.backups[0].volume, backups[1].every). It
// matches model.ErrRejected: the API answers it with a 400.
var ErrInvalid = model.Rejected("invalid backup")

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Keep is a retention: every copy younger than Recent, then the newest copy
// of each of the last Daily UTC days and of the last Monthly UTC months. A
// field left out takes its default.
type Keep struct {
	Recent  string `json:"recent,omitempty" yaml:"recent"`
	Daily   *int   `json:"daily,omitempty" yaml:"daily"`
	Monthly *int   `json:"monthly,omitempty" yaml:"monthly"`
}

// Retention is a Keep with its defaults applied.
type Retention struct {
	Recent  time.Duration
	Daily   int
	Monthly int
}

// Retention resolves k (nil = every default). Callers validate k first;
// an unparsable Recent counts as the default.
func (k *Keep) Retention() Retention {
	r := Retention{Recent: DefaultRecent, Daily: DefaultDaily, Monthly: DefaultMonthly}
	if k == nil {
		return r
	}
	if d, err := time.ParseDuration(k.Recent); err == nil && k.Recent != "" {
		r.Recent = d
	}
	if k.Daily != nil {
		r.Daily = *k.Daily
	}
	if k.Monthly != nil {
		r.Monthly = *k.Monthly
	}
	return r
}

// BackupConfig is one entry of a backups list: a recipe entry (compose
// file) or an override (config.json, which alone may carry Enabled and
// Storage). An override of a recipe backup sets only the fields it changes;
// a backup added in the UI carries Type and every field its type needs.
type BackupConfig struct {
	Name    string   `json:"name" yaml:"name"`
	Type    Type     `json:"type,omitempty" yaml:"type"`
	Enabled bool     `json:"enabled,omitempty" yaml:"-"`
	Storage string   `json:"storage,omitempty" yaml:"-"`
	Every   string   `json:"every,omitempty" yaml:"every"`
	Keep    *Keep    `json:"keep,omitempty" yaml:"keep"`
	Volume  string   `json:"volume,omitempty" yaml:"volume"`
	Service string   `json:"service,omitempty" yaml:"service"`
	Path    string   `json:"path,omitempty" yaml:"path"`
	Paths   []string `json:"paths,omitempty" yaml:"paths"`
	Exclude []string `json:"exclude,omitempty" yaml:"exclude"`
}

// EffectiveBackup is one backup as it runs: the recipe with its override
// layered on top and the defaults applied. FromRecipe names the fields whose
// value comes from the recipe (not overridden); InRecipe is false for a
// backup added in the UI.
type EffectiveBackup struct {
	Name       string          `json:"name"`
	Type       Type            `json:"type"`
	Enabled    bool            `json:"enabled"`
	Storage    string          `json:"storage"`
	Every      string          `json:"every,omitempty"`
	Keep       *Keep           `json:"keep,omitempty"`
	Volume     string          `json:"volume,omitempty"`
	Service    string          `json:"service,omitempty"`
	Path       string          `json:"path,omitempty"`
	Paths      []string        `json:"paths,omitempty"`
	Exclude    []string        `json:"exclude,omitempty"`
	InRecipe   bool            `json:"in_recipe"`
	FromRecipe map[string]bool `json:"from_recipe"`
}

// Interval is the schedule: 0 for a manual backup (and a custom one, which
// winterflow does not run).
func (b EffectiveBackup) Interval() time.Duration {
	if b.Every == "" || b.Every == Manual {
		return 0
	}
	d, _ := time.ParseDuration(b.Every)
	return d
}

// Builtin reports whether winterflow itself runs the backup.
func (b EffectiveBackup) Builtin() bool { return b.Type == TypeSQLite || b.Type == TypeFiles }

var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidateName checks a backup name: like a storage name, lowercase letters,
// digits and inner hyphens, at most 32 characters, so WF_BACKUP_<NAME>_
// variables never collide.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%q is not a valid backup name: use 1-32 lowercase letters, digits and inner hyphens, like database", name)
	}
	return nil
}

// validate checks one effective entry (Type set) against the compose file.
// field prefixes the error ("x-winterflow.backups[0]", "backups[1]").
func validate(c BackupConfig, comp Compose, field string) error {
	if err := ValidateName(c.Name); err != nil {
		return invalid(field+".name", "%v", err)
	}
	if c.Storage != "" && !nameRE.MatchString(c.Storage) {
		return invalid(field+".storage", "%q is not a valid storage name", c.Storage)
	}
	if c.Enabled && c.Storage == "" {
		return invalid(field+".storage", "an enabled backup needs a storage")
	}
	switch c.Type {
	case TypeCustom:
		return validateCustom(c, comp, field)
	case TypeSQLite, TypeFiles:
	case "":
		return invalid(field+".type", "a backup needs a type: sqlite, files or custom")
	default:
		return invalid(field+".type", "unknown type %q: use sqlite, files or custom", c.Type)
	}
	if err := validateEvery(c.Every, field); err != nil {
		return err
	}
	if err := validateKeep(c.Keep, field); err != nil {
		return err
	}
	switch {
	case c.Volume != "" && c.Service != "":
		return invalid(field, "set either volume or service, not both")
	case c.Volume == "" && c.Service == "":
		return invalid(field, "set volume (a volume of the compose file) or service (a service whose mounts hold the data)")
	case c.Volume != "" && !comp.Volumes[c.Volume]:
		return invalid(field+".volume", "%q is not a volume of this compose file", c.Volume)
	case c.Service != "":
		if _, ok := comp.Services[c.Service]; !ok {
			return invalid(field+".service", "%q is not a service of this compose file", c.Service)
		}
	}
	inVolume := c.Volume != ""
	checkPath := func(f, p string) error {
		if inVolume {
			return validateRelPath(f, p)
		}
		return validateAbsPath(f, p)
	}
	if c.Type == TypeSQLite {
		if len(c.Paths) > 0 || len(c.Exclude) > 0 {
			return invalid(field, "a sqlite backup takes path (the database file), not paths or exclude")
		}
		if c.Path == "" {
			return invalid(field+".path", "a sqlite backup needs path: the database file")
		}
		return checkPath(field+".path", c.Path)
	}
	if c.Path != "" {
		return invalid(field+".path", "a files backup takes paths, a list of folders and files")
	}
	if !inVolume && len(c.Paths) == 0 {
		return invalid(field+".paths", "a files backup addressed by service needs paths inside the service's container")
	}
	if len(c.Paths) > maxListLen || len(c.Exclude) > maxListLen {
		return invalid(field, "at most %d paths and %d exclude patterns", maxListLen, maxListLen)
	}
	seen := map[string]bool{}
	for i, p := range c.Paths {
		f := fmt.Sprintf("%s.paths[%d]", field, i)
		if err := checkPath(f, p); err != nil {
			return err
		}
		if seen[p] {
			return invalid(f, "%q is listed twice", p)
		}
		seen[p] = true
	}
	for i, p := range c.Exclude {
		if err := validatePattern(fmt.Sprintf("%s.exclude[%d]", field, i), p); err != nil {
			return err
		}
	}
	return nil
}

func validateCustom(c BackupConfig, comp Compose, field string) error {
	if c.Volume != "" || c.Path != "" || len(c.Paths) > 0 || len(c.Exclude) > 0 || c.Every != "" || c.Keep != nil {
		return invalid(field, "a custom backup takes only service: the service that runs the backup tool")
	}
	if c.Service == "" {
		return invalid(field+".service", "a custom backup needs service: the service that runs the backup tool")
	}
	profiles, ok := comp.Services[c.Service]
	if !ok {
		return invalid(field+".service", "%q is not a service of this compose file", c.Service)
	}
	for _, p := range profiles {
		if p == Profile(c.Name) {
			return nil
		}
	}
	return invalid(field+".service", "service %q must list the profile %q in its profiles, so it runs only while the backup is enabled", c.Service, Profile(c.Name))
}

func validateEvery(every, field string) error {
	if every == "" || every == Manual {
		return nil
	}
	d, err := time.ParseDuration(every)
	if err != nil {
		return invalid(field+".every", "%q is neither a duration like 1h or 30m nor manual", every)
	}
	if d < MinEvery {
		return invalid(field+".every", "%s is too often: at least %s", every, MinEvery)
	}
	return nil
}

func validateKeep(k *Keep, field string) error {
	if k == nil {
		return nil
	}
	if k.Recent != "" {
		if d, err := time.ParseDuration(k.Recent); err != nil || d < 0 {
			return invalid(field+".keep.recent", "%q is not a duration like 24h", k.Recent)
		}
	}
	for _, t := range []struct {
		name string
		v    *int
	}{{"daily", k.Daily}, {"monthly", k.Monthly}} {
		if t.v != nil && (*t.v < 0 || *t.v > maxTier) {
			return invalid(field+".keep."+t.name, "%d is not a count between 0 and %d", *t.v, maxTier)
		}
	}
	return nil
}

// validateRelPath checks a path inside a volume: relative, clean, no "..".
func validateRelPath(field, p string) error {
	if p == "" || len(p) > maxPathLen || hasControl(p) || strings.HasPrefix(p, "/") || path.Clean(p) != p ||
		p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return invalid(field, "%q is not a path inside the volume: use a relative path like app.db or media/uploads (omit paths for the whole volume)", p)
	}
	return nil
}

// validateAbsPath checks a path inside a service's container: absolute and
// clean (so no ".."), not "/".
func validateAbsPath(field, p string) error {
	if len(p) > maxPathLen || hasControl(p) || !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" {
		return invalid(field, "%q is not a path inside the service's container: use an absolute path like /app/media", p)
	}
	return nil
}

// validatePattern checks an exclude glob (path.Match syntax).
func validatePattern(field, p string) error {
	if p == "" || len(p) > maxPathLen || hasControl(p) {
		return invalid(field, "%q is not a pattern", p)
	}
	if _, err := path.Match(strings.Trim(p, "/"), ""); err != nil {
		return invalid(field, "%q is not a valid pattern: %v", p, err)
	}
	return nil
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
