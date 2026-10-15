package template

import (
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
)

func TestCheckName(t *testing.T) {
	if err := CheckName("My notes"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "  ", " padded", strings.Repeat("n", 101)} {
		wantInvalid(t, CheckName(bad), "name")
	}
}

func TestCheckHosts(t *testing.T) {
	s := spec(t)
	if err := CheckHosts(s, []model.InstanceHost{{Host: "notes.example.test", Expose: "app:8080"}}); err != nil {
		t.Fatal(err)
	}
	if err := CheckHosts(s, nil); err != nil {
		t.Fatal(err)
	}
	wantInvalid(t, CheckHosts(s, []model.InstanceHost{{Host: "db.example.test", Expose: "db:5432"}}), "hosts[0].expose")
	wantInvalid(t, CheckHosts(s, []model.InstanceHost{{Host: "a.test", Expose: "app:9090"}}), `"app:9090" is not an http expose entry`)
	wantInvalid(t, CheckHosts(s, []model.InstanceHost{{Host: "Not A Host", Expose: "app:8080"}}), "hosts")
	twice := []model.InstanceHost{{Host: "a.test", Expose: "app:8080"}, {Host: "a.test", Expose: "app:8080"}}
	wantInvalid(t, CheckHosts(s, twice), "hosts")
	many := make([]model.InstanceHost, 51)
	wantInvalid(t, CheckHosts(s, many), "at most 50")
}

func TestCreateBackups(t *testing.T) {
	s := spec(t)
	got, err := CreateBackups(s, map[string]BackupChoice{"db": {Enabled: true, Storage: "nas"}})
	if err != nil || !reflect.DeepEqual(got, []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "nas"}}) {
		t.Fatalf("backups = %+v, %v", got, err)
	}
	if got, err := CreateBackups(s, nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no choice = %#v, %v", got, err)
	}
	_, err = CreateBackups(s, map[string]BackupChoice{"media": {Enabled: true, Storage: "nas"}})
	wantInvalid(t, err, "backups.media")
	_, err = CreateBackups(s, map[string]BackupChoice{"db": {Enabled: true}})
	wantInvalid(t, err, "backups.db.storage: an enabled backup needs a storage")
	_, err = CreateBackups(s, map[string]BackupChoice{"db": {Storage: "../x"}})
	wantInvalid(t, err, "backups.db.storage")
}
