package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// systemDFOut is trimmed `docker system df -v --format json` output in the
// shape Docker 29 prints (sizes are human strings).
const systemDFOut = `{
 "Images":[
  {"ID":"sha256:aaa","Repository":"nginx","Tag":"latest","Containers":"1","Size":"200MB","SharedSize":"50MB","UniqueSize":"150MB"},
  {"ID":"sha256:bbb","Repository":"postgres","Tag":"16","Containers":"1","Size":"1.2GB","SharedSize":"50MB","UniqueSize":"1.15GB"},
  {"ID":"sha256:ccc","Repository":"<none>","Tag":"<none>","Containers":"0","Size":"N/A","SharedSize":"0B","UniqueSize":"300MB"}
 ],
 "Containers":[
  {"ID":"c1","Image":"nginx","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.service=web","Mounts":"wf-app-1_data,/srv/bind","Size":"4.1kB"},
  {"ID":"c2","Image":"postgres:16","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.service=db","Mounts":"shared_ext","Size":"0B"},
  {"ID":"c3","Image":"postgres:16","Labels":"com.docker.compose.project=wf-app-2","Mounts":"shared_ext,app2_ext","Size":"1kB"},
  {"ID":"c4","Image":"nginx","Labels":"com.docker.compose.project=other","Mounts":"other_vol","Size":"9GB"}
 ],
 "Volumes":[
  {"Name":"wf-app-1_data","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.volume=data","Links":"1","Size":"26.59MB"},
  {"Name":"shared_ext","Labels":"","Links":"2","Size":"1GB"},
  {"Name":"app2_ext","Labels":"","Links":"1","Size":"2kB"},
  {"Name":"orphan","Labels":"","Links":"0","Size":"5MB"},
  {"Name":"remote","Labels":"","Links":"0","Size":"N/A"}
 ],
 "BuildCache":[
  {"Size":"10MB","Shared":"false","InUse":"false"},
  {"Size":"20MB","Shared":"true","InUse":"false"},
  {"Size":"30MB","Shared":"false","InUse":"true"}
 ]
}`

func TestParseHumanSize(t *testing.T) {
	cases := map[string]float64{
		"0B": 0, "127B": 127, "4.1kB": 4100, "26.59MB": 26.59e6, "1.2GB": 1.2e9, "2TB": 2e12,
		"26.38GB (74%)": 26.38e9, " 12.29kB ": 12290, "1KiB": 1024, "3": 3,
	}
	for in, want := range cases {
		got, ok := parseHumanSize(in)
		if !ok || got != want {
			t.Errorf("%q = %v (%v), want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"N/A", "", "GB", "1.2XB", "-5MB"} {
		if got, ok := parseHumanSize(bad); ok || got != 0 {
			t.Errorf("%q = %v, %v; want 0, false", bad, got, ok)
		}
	}
}

func TestBuildDiskScan(t *testing.T) {
	var df systemDF
	if err := jsonUnmarshal(systemDFOut, &df); err != nil {
		t.Fatal(err)
	}
	scan := buildDiskScan(df, map[string]float64{"app-1": 1000, "app-3": 7})

	// app-1: dir 1000 + its compose volume 26.59MB + writable 4.1kB; the
	// shared external volume is mounted by two apps, so nobody owns it.
	if got, want := scan.Apps["app-1"].Disk, 1000+26.59e6+4100; got != want {
		t.Errorf("app-1 disk = %v, want %v", got, want)
	}
	// Images: nginx (200MB) + postgres (1.2GB), each counted once.
	if got := scan.Apps["app-1"].Image; got != 1.4e9 {
		t.Errorf("app-1 image = %v", got)
	}
	// app-2: the external volume only it mounts + writable 1kB.
	if got := scan.Apps["app-2"].Disk; got != 2000+1000 {
		t.Errorf("app-2 disk = %v", got)
	}
	if got := scan.Apps["app-3"].Disk; got != 7 {
		t.Errorf("app without containers = %v", got)
	}
	if _, ok := scan.Apps["other"]; ok {
		t.Error("a non-winterflow compose project became an app")
	}
	// Host totals: unique 1.6GB + shared 50MB once; volumes; all build cache.
	if got := scan.Docker.Images; got != 150e6+1.15e9+300e6+50e6 {
		t.Errorf("images = %v", got)
	}
	if got := scan.Docker.Volumes; got != 26.59e6+1e9+2000+5e6 {
		t.Errorf("volumes = %v", got)
	}
	if got := scan.Docker.BuildCache; got != 60e6 {
		t.Errorf("build cache = %v", got)
	}
	// Reclaimable: the unused image's unique bytes, the unlinked volume,
	// unshared idle build cache.
	if got := scan.Docker.Reclaimable; got != 300e6+5e6+10e6 {
		t.Errorf("reclaimable = %v", got)
	}
}

func TestDirSizeSkipsSymlinksAndUnreadable(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "a"), make([]byte, 100), 0o644))
	must(os.MkdirAll(filepath.Join(dir, "source", ".git"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "source", ".git", "pack"), make([]byte, 50), 0o644))
	big := filepath.Join(t.TempDir(), "big")
	must(os.WriteFile(big, make([]byte, 10000), 0o644))
	must(os.Symlink(big, filepath.Join(dir, "link")))
	locked := filepath.Join(dir, "locked")
	must(os.MkdirAll(locked, 0o755))
	must(os.WriteFile(filepath.Join(locked, "secret"), make([]byte, 7), 0o644))
	must(os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got := dirSize(context.Background(), dir)
	if os.Geteuid() == 0 {
		got -= 7 // root reads the locked dir anyway
	}
	if got != 150 {
		t.Fatalf("dirSize = %v, want 150", got)
	}
}

func TestScanDiskRunsOneSystemDF(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	out := filepath.Join(bin, "df.json")
	if err := os.WriteFile(out, []byte(systemDFOut), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := exec.LookPath("cat") // resolved before PATH is replaced
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" + cat + " " + out + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	r := newTestRepo(t)
	if err := os.MkdirAll(filepath.Join(r.cfg.GetAppsDataDir(), "app-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.cfg.GetAppsDataDir(), "app-1", "compose.yml"), make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	scan, err := r.ScanDisk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := scan.Apps["app-1"].Disk; got != 64+26.59e6+4100 {
		t.Fatalf("app-1 disk = %v", got)
	}
	if calls := dockerCalls(t, log); len(calls) != 1 || calls[0] != "system df -v --format json" {
		t.Fatalf("docker calls = %q", calls)
	}
}

func TestScanDiskTimesOut(t *testing.T) {
	sleep, err := exec.LookPath("sleep") // resolved before PATH is replaced
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexec "+sleep+" 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	old := diskScanTimeout
	diskScanTimeout = 200 * time.Millisecond
	t.Cleanup(func() { diskScanTimeout = old })

	start := time.Now()
	_, err = newTestRepo(t).ScanDisk(context.Background())
	if err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("a hung docker must fail the scan by the deadline, got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("scan took %v despite the deadline", time.Since(start))
	}
}

func TestScanDiskBadJSON(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho 'not json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if _, err := newTestRepo(t).ScanDisk(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("bad json err = %v", err)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
