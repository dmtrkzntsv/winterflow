package dockercompose

import (
	"os"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/logger"
)

func testLog() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}

const volumeLSOut = `{"Driver":"local","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.volume=data","Name":"wf-app-1_data"}
{"Driver":"local","Labels":"com.docker.compose.project=wf-gone,com.docker.compose.volume=db","Name":"wf-gone_db"}
{"Driver":"local","Labels":"com.docker.compose.project=twillingate-cloud,com.docker.compose.volume=pgdata","Name":"twillingate-cloud_pgdata"}
{"Driver":"local","Labels":"","Name":"plain"}
{"Driver":"local","Labels":"com.docker.volume.anonymous=","Name":"0f9c6dc6d5ad"}
not json
`

const psOut = `{"Names":"a","Mounts":"wf-app-1_data,/srv/host"}
{"Names":"b","Mounts":"plain"}
{"Names":"c","Mounts":""}
`

func TestParseVolumeLS(t *testing.T) {
	vols := parseVolumeLS([]byte(volumeLSOut), testLog())
	if len(vols) != 5 {
		t.Fatalf("want 5 volumes (malformed line skipped), got %d", len(vols))
	}
	by := map[string]command.Volume{}
	for _, v := range vols {
		by[v.Name] = v
	}
	if v := by["wf-app-1_data"]; v.AppID != "app-1" || v.Project != "wf-app-1" || v.Driver != "local" {
		t.Errorf("wf volume: %+v", v)
	}
	if v := by["twillingate-cloud_pgdata"]; v.AppID != "" || v.Project != "twillingate-cloud" {
		t.Errorf("foreign compose volume: %+v", v)
	}
	if v := by["0f9c6dc6d5ad"]; !v.Anonymous {
		t.Errorf("anonymous: %+v", v)
	}
	if v := by["plain"]; v.AppID != "" || v.Project != "" || v.Anonymous {
		t.Errorf("plain: %+v", v)
	}
}

func TestParseMountedVolumes(t *testing.T) {
	m := parseMountedVolumes([]byte(psOut))
	if !m["wf-app-1_data"] || !m["plain"] {
		t.Fatalf("mounted = %v", m)
	}
	if m[""] {
		t.Fatal("empty Mounts must not produce an empty name")
	}
}

func TestAnnotateVolumes(t *testing.T) {
	vols := parseVolumeLS([]byte(volumeLSOut), testLog())
	annotateVolumes(vols, parseMountedVolumes([]byte(psOut)), func(id string) bool { return id == "app-1" })
	by := map[string]command.Volume{}
	for _, v := range vols {
		by[v.Name] = v
	}
	if v := by["wf-app-1_data"]; !v.InUse || v.Orphan {
		t.Errorf("app-1 data: %+v", v)
	}
	if v := by["wf-gone_db"]; v.InUse || !v.Orphan {
		t.Errorf("gone db must be orphan: %+v", v)
	}
	if v := by["twillingate-cloud_pgdata"]; v.Orphan {
		t.Errorf("non-winterflow project is never orphan: %+v", v)
	}
	if v := by["plain"]; !v.InUse {
		t.Errorf("plain is mounted by a non-compose container: %+v", v)
	}
}

func TestSortVolumes(t *testing.T) {
	vols := []command.Volume{
		{Name: "z-anon", Anonymous: true},
		{Name: "b-plain"},
		{Name: "x", AppID: "b"},
		{Name: "a-plain"},
		{Name: "y", AppID: "a"},
	}
	sortVolumes(vols)
	got := []string{}
	for _, v := range vols {
		got = append(got, v.Name)
	}
	want := []string{"y", "x", "a-plain", "b-plain", "z-anon"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestCreateDeleteVolumeRejectBadNames(t *testing.T) {
	r := newTestRepo(t)
	for _, n := range []string{"", "-rf", "../x"} {
		if err := r.CreateVolume(t.Context(), n); err == nil {
			t.Errorf("create %q must fail validation", n)
		}
		if err := r.DeleteVolume(t.Context(), n); err == nil {
			t.Errorf("delete %q must fail validation", n)
		}
	}
}

func TestAppExistsUsesAppDir(t *testing.T) {
	r := newTestRepo(t)
	if err := os.MkdirAll(r.appDataDir("app-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !r.appExists("app-1") || r.appExists("nope") {
		t.Fatal("appExists must reflect apps-data dirs")
	}
}
