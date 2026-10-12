package recipe

import (
	"fmt"
	"slices"
)

// Field names of FromRecipe.
const (
	FieldEvery   = "every"
	FieldKeep    = "keep"
	FieldVolume  = "volume"
	FieldService = "service"
	FieldPath    = "path"
	FieldPaths   = "paths"
	FieldExclude = "exclude"
)

// Effective layers the overrides on the compose file's recipe and applies
// the defaults: every recipe backup (off unless its override enables it),
// then the backups added in the UI, in override order. An override naming
// a backup the recipe no longer has and carrying no type is stale and
// skipped (Clean drops it at the next save). The result is validated; the
// error names the override's field when the entry has one, else the
// recipe's.
func Effective(comp Compose, overrides []BackupConfig) ([]EffectiveBackup, error) {
	if err := Validate(comp); err != nil {
		return nil, err
	}
	byName, err := indexOverrides(overrides)
	if err != nil {
		return nil, err
	}
	out := make([]EffectiveBackup, 0, len(comp.Recipe)+len(overrides))
	inRecipe := map[string]bool{}
	for i, r := range comp.Recipe {
		inRecipe[r.Name] = true
		field := fmt.Sprintf("x-winterflow.backups[%d]", i)
		c := r
		if j, ok := byName[r.Name]; ok {
			field = fmt.Sprintf("backups[%d]", j)
			o := overrides[j]
			if o.Type != "" && o.Type != r.Type {
				return nil, invalid(field+".type", "%s is a %s backup in the compose file: its type can't change", r.Name, r.Type)
			}
			c = merge(r, o)
		}
		if err := validate(c, comp, field); err != nil {
			return nil, err
		}
		eb := finish(c, true, fromRecipe(r, c))
		if r.Type != TypeCustom {
			eb.Template = templateOf(r)
		}
		out = append(out, eb)
	}
	for j, o := range overrides {
		if inRecipe[o.Name] || o.Type == "" {
			continue
		}
		if err := validate(o, comp, fmt.Sprintf("backups[%d]", j)); err != nil {
			return nil, err
		}
		out = append(out, finish(o, false, map[string]bool{}))
	}
	return out, nil
}

// Validate checks every recipe entry on its own, defaults applied (off, no
// storage).
func Validate(comp Compose) error {
	for i, r := range comp.Recipe {
		if err := validate(r, comp, fmt.Sprintf("x-winterflow.backups[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// Check is the strict form for a backups list a user submits: on top of
// Effective, an entry naming no recipe backup must carry its type (it is a
// new backup, not an override of one).
func Check(comp Compose, overrides []BackupConfig) ([]EffectiveBackup, error) {
	names := map[string]bool{}
	for _, r := range comp.Recipe {
		names[r.Name] = true
	}
	for j, o := range overrides {
		if !names[o.Name] && o.Type == "" {
			return nil, invalid(fmt.Sprintf("backups[%d]", j), "the compose file has no backup %q: a new backup needs its type", o.Name)
		}
		// A page that was opened before the compose file gained this name
		// would otherwise move the compose file's backup to its own location.
		if names[o.Name] && o.Type != "" {
			return nil, invalid(fmt.Sprintf("backups[%d]", j), "%s is declared by the compose file; edit it instead of adding it", o.Name)
		}
	}
	return Effective(comp, overrides)
}

// Clean is the backups list to store: stale overrides (a name the recipe no
// longer has, without type) are dropped with a warning; an override of a
// recipe backup keeps only the fields that differ from the recipe (and no
// type); a disabled entry left with nothing to say is dropped. When a compose
// file could not be read (comp.Unparsed) the recipe is incomplete, so nothing
// is dropped: the overrides come back unchanged, with no warnings.
func Clean(comp Compose, overrides []BackupConfig) (kept []BackupConfig, warnings []string) {
	if comp.Unparsed {
		return overrides, nil
	}
	recipe := map[string]BackupConfig{}
	for _, r := range comp.Recipe {
		recipe[r.Name] = r
	}
	kept = []BackupConfig{}
	for _, o := range overrides {
		r, ok := recipe[o.Name]
		if !ok {
			if o.Type == "" {
				warnings = append(warnings, fmt.Sprintf("the compose file no longer has the backup %q: its settings were dropped", o.Name))
				continue
			}
			kept = append(kept, o)
			continue
		}
		if m := minimize(r, o); !m.empty() {
			kept = append(kept, m)
		}
	}
	return kept, warnings
}

// indexOverrides maps names to positions, refusing duplicates and bad names.
func indexOverrides(overrides []BackupConfig) (map[string]int, error) {
	out := map[string]int{}
	for j, o := range overrides {
		if err := ValidateName(o.Name); err != nil {
			return nil, invalid(fmt.Sprintf("backups[%d].name", j), "%v", err)
		}
		if _, dup := out[o.Name]; dup {
			return nil, invalid(fmt.Sprintf("backups[%d].name", j), "%q is listed twice", o.Name)
		}
		out[o.Name] = j
	}
	return out, nil
}

// merge is the recipe entry r with o's fields on top. Setting volume or
// service moves the backup to that addressing (the other one is cleared).
func merge(r, o BackupConfig) BackupConfig {
	c := r
	c.Enabled, c.Storage = o.Enabled, o.Storage
	if o.Every != "" {
		c.Every = o.Every
	}
	if o.Keep != nil {
		c.Keep = o.Keep
	}
	if o.Volume != "" || o.Service != "" {
		c.Volume, c.Service = o.Volume, o.Service
	}
	if o.Path != "" {
		c.Path = o.Path
	}
	if o.Paths != nil {
		c.Paths = o.Paths
	}
	if o.Exclude != nil {
		c.Exclude = o.Exclude
	}
	return c
}

// minimize keeps of o what differs from the recipe entry r.
func minimize(r, o BackupConfig) BackupConfig {
	m := BackupConfig{Name: o.Name, Enabled: o.Enabled, Storage: o.Storage}
	if o.Every != "" && o.Every != r.Every {
		m.Every = o.Every
	}
	if o.Keep != nil && !keepEqual(o.Keep, r.Keep) {
		m.Keep = o.Keep
	}
	if (o.Volume != "" || o.Service != "") && (o.Volume != r.Volume || o.Service != r.Service) {
		m.Volume, m.Service = o.Volume, o.Service
	}
	if o.Path != "" && o.Path != r.Path {
		m.Path = o.Path
	}
	if o.Paths != nil && !slices.Equal(*o.Paths, List(r.Paths)) {
		m.Paths = nonNil(o.Paths)
	}
	if o.Exclude != nil && !slices.Equal(*o.Exclude, List(r.Exclude)) {
		m.Exclude = nonNil(o.Exclude)
	}
	return m
}

// nonNil is p with a nil slice made empty: an explicit empty list must be
// stored as [] (a pointer to a nil slice marshals as null, which reads back
// as "inherit the recipe's").
func nonNil(p *[]string) *[]string {
	if *p == nil {
		return &[]string{}
	}
	return p
}

// empty reports whether an override says nothing beyond its name.
func (c BackupConfig) empty() bool {
	return !c.Enabled && c.Storage == "" && c.Type == "" && c.Every == "" && c.Keep == nil &&
		c.Volume == "" && c.Service == "" && c.Path == "" && c.Paths == nil && c.Exclude == nil
}

func keepEqual(a, b *Keep) bool {
	return a.Retention() == b.Retention()
}

// fromRecipe names the fields of c that hold the recipe's value, including
// one the recipe leaves out (its default, or empty) and no override sets:
// the UI badges every other field as changed, with a reset to the recipe.
func fromRecipe(r, c BackupConfig) map[string]bool {
	from := map[string]bool{}
	set := func(name string, same bool) {
		if same {
			from[name] = true
		}
	}
	set(FieldEvery, c.Every == r.Every)
	set(FieldKeep, keepEqual(c.Keep, r.Keep))
	set(FieldVolume, c.Volume == r.Volume)
	set(FieldService, c.Service == r.Service)
	set(FieldPath, c.Path == r.Path)
	set(FieldPaths, slices.Equal(List(c.Paths), List(r.Paths)))
	set(FieldExclude, slices.Equal(List(c.Exclude), List(r.Exclude)))
	return from
}

// finish turns a validated entry into its effective form: every and keep
// defaulted for the built-in types.
func finish(c BackupConfig, inRecipe bool, from map[string]bool) EffectiveBackup {
	b := EffectiveBackup{
		Name: c.Name, Type: c.Type, Enabled: c.Enabled, Storage: c.Storage,
		Volume: c.Volume, Service: c.Service, Path: c.Path, Paths: List(c.Paths), Exclude: List(c.Exclude),
		InRecipe: inRecipe, FromRecipe: from,
	}
	if c.Type != TypeCustom {
		b.Every = c.Every
		if b.Every == "" {
			b.Every = DefaultEvery
		}
		r := c.Keep.Retention()
		daily, monthly := r.Daily, r.Monthly
		b.Keep = &Keep{Recent: r.Recent.String(), Daily: &daily, Monthly: &monthly}
	}
	return b
}

// templateOf is the recipe entry r's own settings with the defaults applied.
func templateOf(r BackupConfig) *Template {
	t := &Template{
		Every: r.Every, Volume: r.Volume, Service: r.Service, Path: r.Path,
		Paths: List(r.Paths), Exclude: List(r.Exclude),
	}
	if t.Every == "" {
		t.Every = DefaultEvery
	}
	ret := r.Keep.Retention()
	daily, monthly := ret.Daily, ret.Monthly
	t.Keep = &Keep{Recent: ret.Recent.String(), Daily: &daily, Monthly: &monthly}
	return t
}

// Declared maps the effective backups by name: what WF_BACKUP_ references
// resolve against.
func Declared(backups []EffectiveBackup) map[string]EffectiveBackup {
	out := make(map[string]EffectiveBackup, len(backups))
	for _, b := range backups {
		out[b.Name] = b
	}
	return out
}
