package recipe

import (
	"reflect"
	"testing"
)

func TestTargets(t *testing.T) {
	c := parse(t, `
services:
  web:
    image: example/web
    volumes:
      - data:/data
      - ./media:/app/media:ro
      - /srv/shared/:/shared/
      - ~/notes:/notes
      - ${HOST_DIR}:/host
      - /anonymous
      - type: tmpfs
        target: /tmp
      - type: volume
        source: cache
        target: /var/cache//app/
      - type: bind
        target: /no-source
      - {type: volume, source: data, target: relative}
  worker:
    image: example/worker
volumes:
  data:
  cache:
`, `
services:
  web:
    volumes:
      - other:/data
volumes:
  other:
`)
	got := c.Targets()
	want := Targets{
		Volumes: []string{"cache", "data", "other"},
		Services: []ServiceMounts{
			{Name: "web", Mounts: []Mount{
				{Type: "volume", Source: "other", Target: "/data"},
				{Type: "bind", Source: "${HOST_DIR}", Target: "/host"},
				{Type: "bind", Source: "./media", Target: "/app/media"},
				{Type: "bind", Source: "~/notes", Target: "/notes"},
				{Type: "bind", Source: "/srv/shared/", Target: "/shared"},
				{Type: "volume", Source: "cache", Target: "/var/cache/app"},
			}},
			{Name: "worker", Mounts: []Mount{}},
		},
	}
	// Sorted by target.
	want.Services[0].Mounts = []Mount{
		{Type: "bind", Source: "./media", Target: "/app/media"},
		{Type: "volume", Source: "other", Target: "/data"},
		{Type: "bind", Source: "${HOST_DIR}", Target: "/host"},
		{Type: "bind", Source: "~/notes", Target: "/notes"},
		{Type: "bind", Source: "/srv/shared/", Target: "/shared"},
		{Type: "volume", Source: "cache", Target: "/var/cache/app"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets =\n%+v\nwant\n%+v", got, want)
	}
	if empty := (Compose{}).Targets(); empty.Volumes == nil || empty.Services == nil {
		t.Fatalf("no compose file: %+v", empty)
	}
}

// A field the recipe leaves to its default is the recipe's value until an
// override sets it.
func TestFromRecipeCountsDefaults(t *testing.T) {
	c := parse(t, `
services:
  web: {image: example/web, volumes: [data:/data]}
volumes: {data: {}}
x-winterflow:
  backups:
    - {name: db, type: sqlite, volume: data, path: app.db}
`)
	got, err := Effective(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{FieldEvery, FieldKeep, FieldVolume, FieldService, FieldPath, FieldPaths, FieldExclude} {
		if !got[0].FromRecipe[f] {
			t.Errorf("%s is the recipe's (a default) but not marked so: %+v", f, got[0].FromRecipe)
		}
	}
	got, err = Effective(c, []BackupConfig{{Name: "db", Every: "1h", Keep: &Keep{Daily: ptr(3)}}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].FromRecipe[FieldEvery] || got[0].FromRecipe[FieldKeep] || !got[0].FromRecipe[FieldPath] {
		t.Fatalf("overridden fields: %+v", got[0].FromRecipe)
	}
}
