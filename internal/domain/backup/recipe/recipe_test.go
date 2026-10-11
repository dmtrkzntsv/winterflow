package recipe

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
)

const compose = `
services:
  web:
    image: example/web
    volumes: [data:/data, ./media:/app/media]
  litestream:
    image: litestream/litestream
    profiles: [wf-backup-replica]
volumes:
  data:
  cache: {}
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: data
      path: twillingate.db
      every: 1h
      keep:
        recent: 24h
        daily: 14
        monthly: 6
    - name: media
      type: files
      service: web
      paths: [/app/media, /data/config]
      exclude: [/app/media/cache/, "*.tmp"]
      every: 24h
    - name: replica
      type: custom
      service: litestream
`

func parse(t *testing.T, files ...string) Compose {
	t.Helper()
	raw := make([][]byte, len(files))
	for i, f := range files {
		raw[i] = []byte(f)
	}
	c, err := ParseCompose(raw...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ptr(n int) *int { return &n }

func TestParseCompose(t *testing.T) {
	c := parse(t, compose)
	if !c.Volumes["data"] || !c.Volumes["cache"] || len(c.Volumes) != 2 {
		t.Fatalf("volumes = %v", c.Volumes)
	}
	if !reflect.DeepEqual(c.Services["litestream"], []string{"wf-backup-replica"}) || c.Services["web"] != nil {
		t.Fatalf("services = %v", c.Services)
	}
	want := []BackupConfig{
		{Name: "database", Type: TypeSQLite, Volume: "data", Path: "twillingate.db", Every: "1h",
			Keep: &Keep{Recent: "24h", Daily: ptr(14), Monthly: ptr(6)}},
		{Name: "media", Type: TypeFiles, Service: "web", Paths: strs("/app/media", "/data/config"),
			Exclude: strs("/app/media/cache/", "*.tmp"), Every: "24h"},
		{Name: "replica", Type: TypeCustom, Service: "litestream"},
	}
	if !reflect.DeepEqual(c.Recipe, want) {
		t.Fatalf("recipe = %+v", c.Recipe)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	// No x-winterflow, a null one, a null list, invalid YAML: no recipe.
	for _, f := range []string{"services: {a: {}}", "x-winterflow:\n", "x-winterflow:\n  backups:\n", "x-winterflow: {other: 1}"} {
		if c := parse(t, f); len(c.Recipe) != 0 {
			t.Errorf("%q: recipe = %+v", f, c.Recipe)
		}
	}
}

// The override file adds services and volumes and replaces recipe entries
// by name.
func TestParseComposeOverrideFile(t *testing.T) {
	override := `
services:
  worker: {}
volumes:
  extra:
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: extra
      path: other.db
    - name: added
      type: files
      volume: extra
`
	c := parse(t, compose, override)
	if !c.Volumes["extra"] || !c.Volumes["data"] {
		t.Fatalf("volumes = %v", c.Volumes)
	}
	if _, ok := c.Services["worker"]; !ok {
		t.Fatal("override service missing")
	}
	if len(c.Recipe) != 4 || c.Recipe[0].Volume != "extra" || c.Recipe[3].Name != "added" {
		t.Fatalf("recipe = %+v", c.Recipe)
	}
}

func TestParseComposeErrors(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"x-winterflow: [1]", "x-winterflow: must be a mapping"},
		{"x-winterflow:\n  backups: {name: a}", "x-winterflow.backups: must be a list of backups"},
		{"x-winterflow:\n  backups: [db]", "x-winterflow.backups[0]: must be a mapping"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, colour: red}", "x-winterflow.backups[0].colour: unknown key"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, enabled: true}", "x-winterflow.backups[0].enabled: a recipe never turns a backup on"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, storage: s3}", "x-winterflow.backups[0].storage: a recipe never turns a backup on"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, keep: {weekly: 2}}", "x-winterflow.backups[0].keep.weekly: unknown key"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, keep: 3}", "x-winterflow.backups[0]: line 3: cannot unmarshal"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: files, paths: /a}", "x-winterflow.backups[0]: line 3: cannot unmarshal !!str `/a` into []string"},
		{"x-winterflow:\n  backups:\n    - {name: DB, type: sqlite}", `x-winterflow.backups[0].name: "DB" is not a valid backup name`},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite}\n    - {name: db, type: files}", `x-winterflow.backups[1].name: "db" is used by another backup`},
	} {
		_, err := ParseCompose([]byte(tc.yaml))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.yaml, err, tc.want)
		}
	}
}

// Every value error, through Validate (the recipe alone).
func TestValidateErrors(t *testing.T) {
	head := "services:\n  web: {}\n  tool: {profiles: [other]}\nvolumes:\n  data:\nx-winterflow:\n  backups:\n    - "
	for _, tc := range []struct{ entry, want string }{
		{"{name: a}", "x-winterflow.backups[0].type: a backup needs a type"},
		{"{name: a, type: postgres}", `x-winterflow.backups[0].type: unknown type "postgres"`},
		{"{name: a, type: sqlite, volume: data, service: web, path: a.db}", "set either volume or service, not both"},
		{"{name: a, type: sqlite, path: a.db}", "set volume (a volume of the compose file) or service"},
		{"{name: a, type: sqlite, volume: dat, path: a.db}", `x-winterflow.backups[0].volume: "dat" is not a volume of this compose file`},
		{"{name: a, type: sqlite, service: api, path: /a.db}", `x-winterflow.backups[0].service: "api" is not a service of this compose file`},
		{"{name: a, type: sqlite, volume: data}", "x-winterflow.backups[0].path: a sqlite backup needs path"},
		{"{name: a, type: sqlite, volume: data, path: a.db, paths: [b]}", "a sqlite backup takes path"},
		{"{name: a, type: sqlite, volume: data, path: /a.db}", `x-winterflow.backups[0].path: "/a.db" is not a path inside the volume`},
		{"{name: a, type: sqlite, volume: data, path: ../a.db}", "is not a path inside the volume"},
		{"{name: a, type: sqlite, volume: data, path: a/../b.db}", "is not a path inside the volume"},
		{"{name: a, type: sqlite, service: web, path: a.db}", `x-winterflow.backups[0].path: "a.db" is not a path inside the service's container`},
		{"{name: a, type: sqlite, service: web, path: /}", "is not a path inside the service's container"},
		{"{name: a, type: sqlite, volume: data, path: a.db, every: 1m}", "x-winterflow.backups[0].every: 1m is too often: at least 5m0s"},
		{"{name: a, type: sqlite, volume: data, path: a.db, every: hourly}", `"hourly" is neither a duration like 1h or 30m nor manual`},
		{"{name: a, type: sqlite, volume: data, path: a.db, keep: {recent: soon}}", `x-winterflow.backups[0].keep.recent: "soon" is not a duration`},
		{"{name: a, type: sqlite, volume: data, path: a.db, keep: {daily: -1}}", "x-winterflow.backups[0].keep.daily: -1 is not a count"},
		{"{name: a, type: sqlite, volume: data, path: a.db, keep: {monthly: 9999}}", "x-winterflow.backups[0].keep.monthly: 9999 is not a count"},
		{"{name: a, type: files, volume: data, path: a}", "x-winterflow.backups[0].path: a files backup takes paths"},
		{"{name: a, type: files, service: web}", "x-winterflow.backups[0].paths: a files backup addressed by service needs paths"},
		{"{name: a, type: files, service: web, paths: [/a, /a]}", `x-winterflow.backups[0].paths[1]: "/a" is listed twice`},
		{"{name: a, type: files, service: web, paths: [/app/media, /app/media/cache]}", `x-winterflow.backups[0].paths[1]: "/app/media/cache" is inside "/app/media"`},
		{"{name: a, type: files, service: web, paths: [/app/media/cache, /app/media]}", `x-winterflow.backups[0].paths[0]: "/app/media/cache" is inside "/app/media"`},
		{"{name: a, type: files, volume: data, paths: [uploads, uploads/tmp]}", `paths[1]: "uploads/tmp" is inside "uploads"`},
		{"{name: a, type: files, volume: data, exclude: ['[']}", `x-winterflow.backups[0].exclude[0]: "[" is not a valid pattern`},
		{"{name: a, type: files, volume: data, exclude: ['']}", `x-winterflow.backups[0].exclude[0]: "" is not a pattern`},
		{"{name: a, type: custom}", "x-winterflow.backups[0].service: a custom backup needs service"},
		{"{name: a, type: custom, service: web, every: 1h}", "a custom backup takes only service"},
		{"{name: a, type: custom, service: api}", `"api" is not a service of this compose file`},
		{"{name: a, type: custom, service: tool}", `service "tool" must list the profile "wf-backup-a" in its profiles`},
	} {
		c, err := ParseCompose([]byte(head + tc.entry))
		if err == nil {
			err = Validate(c)
		}
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.entry, err, tc.want)
		}
	}
	// Too many paths.
	c := parse(t, "volumes: {data: }")
	many := make([]string, maxListLen+1)
	for i := range many {
		many[i] = "p" + strings.Repeat("x", i)
	}
	c.Recipe = []BackupConfig{{Name: "a", Type: TypeFiles, Volume: "data", Paths: &many}}
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "at most 64 paths") {
		t.Fatalf("too many paths: %v", err)
	}
	// Control characters never pass.
	c.Recipe = []BackupConfig{{Name: "a", Type: TypeFiles, Volume: "data", Paths: strs("a\nb")}}
	if err := Validate(c); err == nil {
		t.Fatal("a newline in a path passed")
	}
}

func TestEffective(t *testing.T) {
	c := parse(t, compose)
	got, err := Effective(c, []BackupConfig{
		{Name: "database", Enabled: true, Storage: "hetzner", Every: "15m"},
		{Name: "audit", Type: TypeSQLite, Volume: "data", Path: "audit.db", Enabled: true, Storage: "backup-disk"},
		{Name: "gone", Every: "2h"}, // stale override: skipped
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d backups: %+v", len(got), got)
	}
	db := got[0]
	if db.Name != "database" || !db.Enabled || db.Storage != "hetzner" || db.Every != "15m" || db.Interval() != 15*time.Minute ||
		!db.InRecipe || db.FromRecipe[FieldEvery] || !db.FromRecipe[FieldVolume] || !db.FromRecipe[FieldPath] || !db.FromRecipe[FieldKeep] {
		t.Fatalf("database = %+v", db)
	}
	media := got[1]
	if media.Enabled || media.Storage != "" || media.Every != "24h" || *media.Keep.Daily != DefaultDaily || media.Keep.Recent != "24h0m0s" ||
		!media.FromRecipe[FieldExclude] || !media.FromRecipe[FieldService] || !media.Builtin() {
		t.Fatalf("media = %+v (keep %+v)", media, media.Keep)
	}
	replica := got[2]
	if replica.Type != TypeCustom || replica.Every != "" || replica.Keep != nil || replica.Interval() != 0 || replica.Builtin() {
		t.Fatalf("replica = %+v", replica)
	}
	audit := got[3]
	if audit.InRecipe || len(audit.FromRecipe) != 0 || audit.Every != DefaultEvery || audit.Interval() != time.Hour {
		t.Fatalf("audit = %+v", audit)
	}
	if d := Declared(got); len(d) != 4 || d["audit"].Path != "audit.db" {
		t.Fatalf("declared = %+v", d)
	}
}

func TestEffectiveOverrideRules(t *testing.T) {
	c := parse(t, compose)
	// Moving a backup to a service needs its paths too.
	got, err := Effective(c, []BackupConfig{{Name: "database", Service: "web", Path: "/data/twillingate.db"}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Volume != "" || got[0].Service != "web" || got[0].FromRecipe[FieldVolume] || got[0].FromRecipe[FieldService] {
		t.Fatalf("moved = %+v", got[0])
	}
	if got[0].Every != "1h" || !got[0].FromRecipe[FieldEvery] {
		t.Fatalf("every should follow the recipe: %+v", got[0])
	}
	for _, tc := range []struct {
		o    []BackupConfig
		want string
	}{
		{[]BackupConfig{{Name: "database", Service: "web"}}, `backups[0].path: "twillingate.db" is not a path inside the service's container`},
		{[]BackupConfig{{Name: "database", Type: TypeFiles}}, "backups[0].type: database is a sqlite backup in the compose file: its type can't change"},
		{[]BackupConfig{{Name: "database", Enabled: true}}, "backups[0].storage: an enabled backup needs a storage"},
		{[]BackupConfig{{Name: "database", Storage: "Bad_Name"}}, `backups[0].storage: "Bad_Name" is not a valid storage name`},
		{[]BackupConfig{{Name: "database"}, {Name: "database"}}, `backups[1].name: "database" is listed twice`},
		{[]BackupConfig{{Name: "x_y"}}, `backups[0].name: "x_y" is not a valid backup name`},
		{[]BackupConfig{{Name: "new", Type: TypeFiles, Service: "web"}}, "backups[0].paths: a files backup addressed by service needs paths"},
		{[]BackupConfig{{Name: "database", Every: "1s"}}, "backups[0].every: 1s is too often"},
	} {
		if _, err := Effective(c, tc.o); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.o, err, tc.want)
		}
	}
	// The same type on a recipe entry is accepted (a UI-added backup the
	// template later adopted).
	if _, err := Effective(c, []BackupConfig{{Name: "database", Type: TypeSQLite}}); err != nil {
		t.Fatal(err)
	}
	// An invalid recipe fails whatever the overrides.
	c.Recipe[0].Path = ""
	if _, err := Effective(c, nil); err == nil || !strings.Contains(err.Error(), "x-winterflow.backups[0].path") {
		t.Fatalf("invalid recipe: %v", err)
	}
}

func TestCheckRefusesAnIncompleteNewBackup(t *testing.T) {
	c := parse(t, compose)
	if _, err := Check(c, []BackupConfig{{Name: "gone", Every: "2h"}}); err == nil ||
		!strings.Contains(err.Error(), `backups[0]: the compose file has no backup "gone": a new backup needs its type`) {
		t.Fatalf("err = %v", err)
	}
	got, err := Check(c, []BackupConfig{{Name: "media", Enabled: true, Storage: "disk"}})
	if err != nil || !got[1].Enabled {
		t.Fatalf("check = %+v, %v", got, err)
	}
}

func TestClean(t *testing.T) {
	c := parse(t, compose)
	kept, warnings := Clean(c, []BackupConfig{
		// Same as the recipe except storage and enabled: only those stay.
		{Name: "database", Type: TypeSQLite, Enabled: true, Storage: "hetzner", Every: "1h", Volume: "data",
			Path: "twillingate.db", Keep: &Keep{Recent: "24h", Daily: ptr(14), Monthly: ptr(6)}},
		// Says nothing: dropped.
		{Name: "media", Paths: strs("/app/media", "/data/config")},
		// Changed fields stay.
		{Name: "replica", Enabled: true, Storage: "hetzner"},
		{Name: "gone", Every: "2h"},
		{Name: "audit", Type: TypeSQLite, Volume: "data", Path: "audit.db"},
	})
	want := []BackupConfig{
		{Name: "database", Enabled: true, Storage: "hetzner"},
		{Name: "replica", Enabled: true, Storage: "hetzner"},
		{Name: "audit", Type: TypeSQLite, Volume: "data", Path: "audit.db"},
	}
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept = %+v", kept)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `no longer has the backup "gone"`) {
		t.Fatalf("warnings = %v", warnings)
	}
	kept, _ = Clean(c, []BackupConfig{{Name: "media", Exclude: strs(), Every: "6h", Keep: &Keep{Daily: ptr(3)}, Service: "web", Volume: ""},
		{Name: "database", Volume: "data", Path: "other.db"}})
	if len(kept) != 2 || kept[0].Exclude == nil || kept[0].Every != "6h" || kept[0].Keep == nil || kept[0].Service != "" ||
		kept[1].Volume != "" || kept[1].Path != "other.db" {
		t.Fatalf("kept = %+v", kept)
	}
}

func TestRetention(t *testing.T) {
	var k *Keep
	if r := k.Retention(); r != (Retention{Recent: 24 * time.Hour, Daily: 14, Monthly: 6}) {
		t.Fatalf("defaults = %+v", r)
	}
	if r := (&Keep{Recent: "2h", Daily: ptr(0)}).Retention(); r != (Retention{Recent: 2 * time.Hour, Daily: 0, Monthly: 6}) {
		t.Fatalf("partial = %+v", r)
	}
	if (EffectiveBackup{Every: Manual}).Interval() != 0 {
		t.Fatal("manual has an interval")
	}
	if Profile("replica") != "wf-backup-replica" {
		t.Fatal(Profile("replica"))
	}
}

func TestErrInvalidIsARejection(t *testing.T) {
	_, err := ParseCompose([]byte("x-winterflow: [1]"))
	if !errors.Is(err, model.ErrRejected) {
		t.Fatalf("err = %v", err)
	}
}

// A file with only type errors keeps what decoded; one that is not YAML is
// skipped and flagged, and Clean then drops nothing.
func TestParseComposeBrokenFiles(t *testing.T) {
	typeErr := "services:\n  web: {profiles: wf-backup-x}\nvolumes:\n  data:\nx-winterflow:\n  backups:\n    - {name: a, type: files, volume: data}\n"
	c := parse(t, typeErr)
	if c.Unparsed || !c.Volumes["data"] || len(c.Recipe) != 1 {
		t.Fatalf("type error: %+v", c)
	}
	if _, ok := c.Services["web"]; !ok {
		t.Fatalf("services = %v", c.Services)
	}

	c = parse(t, ":\n  - [")
	if !c.Unparsed || len(c.Recipe) != 0 {
		t.Fatalf("syntax error: %+v", c)
	}
	in := []BackupConfig{{Name: "a", Enabled: true, Storage: "s"}}
	kept, warnings := Clean(c, in)
	if !reflect.DeepEqual(kept, in) || len(warnings) != 0 {
		t.Fatalf("kept = %+v, warnings = %v", kept, warnings)
	}
	// A readable main file plus a broken override file is still flagged.
	if c := parse(t, compose, ":\n  - ["); !c.Unparsed || len(c.Recipe) != 3 {
		t.Fatalf("broken override: %+v", c)
	}
}

// Overrides of keep, paths and exclude layer on the recipe and survive Clean
// only when they differ.
func TestOverrideKeepPathsExclude(t *testing.T) {
	c := parse(t, compose)
	o := []BackupConfig{{Name: "media", Keep: &Keep{Daily: ptr(3)}, Paths: strs("/app/media"), Exclude: strs("*.log")}}
	got, err := Effective(c, o)
	if err != nil {
		t.Fatal(err)
	}
	m := got[1]
	if *m.Keep.Daily != 3 || *m.Keep.Monthly != DefaultMonthly || !reflect.DeepEqual(m.Paths, []string{"/app/media"}) ||
		!reflect.DeepEqual(m.Exclude, []string{"*.log"}) || m.FromRecipe[FieldKeep] || m.FromRecipe[FieldPaths] || m.FromRecipe[FieldExclude] || !m.FromRecipe[FieldService] {
		t.Fatalf("media = %+v (keep %+v)", m, m.Keep)
	}
	kept, _ := Clean(c, o)
	if len(kept) != 1 || kept[0].Keep == nil || kept[0].Paths == nil || kept[0].Exclude == nil {
		t.Fatalf("kept = %+v", kept)
	}
	// Restating the recipe's values changes nothing: from the recipe, and
	// dropped by Clean.
	same := []BackupConfig{{Name: "database", Keep: &Keep{Recent: "24h", Daily: ptr(14), Monthly: ptr(6)}},
		{Name: "media", Paths: strs("/app/media", "/data/config"), Exclude: strs("/app/media/cache/", "*.tmp")}}
	got, err = Effective(c, same)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].FromRecipe[FieldKeep] || !got[1].FromRecipe[FieldPaths] || !got[1].FromRecipe[FieldExclude] {
		t.Fatalf("restated = %+v %+v", got[0].FromRecipe, got[1].FromRecipe)
	}
	if kept, _ := Clean(c, same); len(kept) != 0 {
		t.Fatalf("kept = %+v", kept)
	}
	// An override path replaces the recipe's.
	got, err = Effective(c, []BackupConfig{{Name: "database", Path: "other.db"}})
	if err != nil || got[0].Path != "other.db" || got[0].FromRecipe[FieldPath] {
		t.Fatalf("path = %+v, %v", got, err)
	}
}

func strs(s ...string) *[]string {
	if s == nil {
		s = []string{}
	}
	return &s
}

// An explicit empty list is an override ("the whole volume", "no excludes"),
// different from an omitted one ("keep the recipe's"), and survives JSON.
func TestExplicitEmptyListsAreOverrides(t *testing.T) {
	c := parse(t, `
services:
  web:
    image: example/web
    volumes: [data:/data]
volumes:
  data:
x-winterflow:
  backups:
    - name: media
      type: files
      volume: data
      paths: [uploads]
      exclude: ["*.tmp"]
`)
	var o BackupConfig
	if err := json.Unmarshal([]byte(`{"name":"media","paths":[],"exclude":[]}`), &o); err != nil {
		t.Fatal(err)
	}
	if o.Paths == nil || o.Exclude == nil {
		t.Fatalf("[] decoded as absent: %+v", o)
	}
	kept, _ := Clean(c, []BackupConfig{o})
	raw, _ := json.Marshal(kept)
	if string(raw) != `[{"name":"media","paths":[],"exclude":[]}]` {
		t.Fatalf("stored %s", raw)
	}
	var back []BackupConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	eff, err := Effective(c, back)
	if err != nil || len(eff[0].Paths) != 0 || len(eff[0].Exclude) != 0 || eff[0].FromRecipe[FieldPaths] || eff[0].FromRecipe[FieldExclude] {
		t.Fatalf("effective = %+v, %v", eff, err)
	}
	// Omitted lists inherit the recipe's.
	eff, _ = Effective(c, []BackupConfig{{Name: "media"}})
	if !reflect.DeepEqual(eff[0].Paths, []string{"uploads"}) || !reflect.DeepEqual(eff[0].Exclude, []string{"*.tmp"}) {
		t.Fatalf("inherited = %+v", eff[0])
	}
}

// A sibling that merely shares a name prefix is not inside the path.
func TestSiblingPathsAreNotNested(t *testing.T) {
	c := parse(t, `
services:
  web:
    image: example/web
    volumes: [data:/data]
volumes:
  data:
x-winterflow:
  backups:
    - {name: a, type: files, volume: data, paths: [media, media2, media-old/x]}
`)
	if _, err := Effective(c, nil); err != nil {
		t.Fatal(err)
	}
}
