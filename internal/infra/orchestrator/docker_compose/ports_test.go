package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

const exposeCompose = `services:
  app:
    image: nginx
  db:
    image: postgres
x-winterflow:
  expose:
    - { container: app, port: 8080 }
    - { container: db, port: 5432, protocol: tcp }
`

// exposePayload is an app with exposeCompose (or compose) and extra config
// keys (a JSON object's inner part, e.g. `"ports":{}`).
func exposePayload(appID, compose, extra string) command.AppPayload {
	cfg := `{"name":"` + appID + `","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[]`
	if extra != "" {
		cfg += "," + extra
	}
	return command.AppPayload{
		AppID:  appID,
		Config: []byte(cfg + "}"),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
	}
}

// portRepo is a test repository allocating from 31000-31009 with a probe
// that refuses the ports in busy (and records what it was asked).
func portRepo(t *testing.T, busy ...int) (*Repository, *[]int) {
	t.Helper()
	t.Setenv("EXPOSE_PORT_RANGE", "31000-31009")
	r := newTestRepo(t)
	var mu sync.Mutex
	var probed []int
	r.probePort = func(_ string, port int, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		probed = append(probed, port)
		for _, b := range busy {
			if b == port {
				return errors.New("address already in use")
			}
		}
		return nil
	}
	return r, &probed
}

func portsOf(t *testing.T, r *Repository, appID string) map[string]model.PortAllocation {
	t.Helper()
	ports, err := r.readPorts(r.appDataDir(appID))
	if err != nil {
		t.Fatal(err)
	}
	return ports
}

func TestSavedExposeEntriesGetPortsFromTheRange(t *testing.T) {
	r, _ := portRepo(t, 31000)
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	want := map[string]model.PortAllocation{"app:8080/http": {Host: 31001}, "db:5432/tcp": {Host: 31002}}
	if got := portsOf(t, r, "app-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ports = %+v", got)
	}
	// Committed: redeploys and rollbacks keep them.
	head, _ := gitHead(r.appDataDir("app-1"))
	if err := gitRestore(r.appDataDir("app-1"), head); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("committed ports = %+v", got)
	}
}

// A redeploy keeps the app's ports and never probes them: its own
// containers hold them.
func TestSaveKeepsPortsWithoutProbingThem(t *testing.T) {
	r, probed := portRepo(t)
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	first := portsOf(t, r, "app-1")
	*probed = nil
	r.probePort = func(string, int, string) error { return errors.New("held by the app's own container") }
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1"); !reflect.DeepEqual(got, first) {
		t.Fatalf("ports moved: %+v -> %+v", first, got)
	}
}

func TestPortsDoNotCollideBetweenApps(t *testing.T) {
	r, _ := portRepo(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.saveWithoutDeploy(exposePayload(fmt.Sprintf("app-%d", i), exposeCompose, ""))
		}()
	}
	wg.Wait()
	seen := map[int]string{}
	for i := range 4 {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		for id, p := range portsOf(t, r, fmt.Sprintf("app-%d", i)) {
			if other, dup := seen[p.Host]; dup {
				t.Fatalf("port %d given to %s and app-%d %s", p.Host, other, i, id)
			}
			seen[p.Host] = fmt.Sprintf("app-%d %s", i, id)
		}
	}
	// A restarted agent rebuilds the index from disk.
	fresh := NewRepository(r.cfg, r.log)
	fresh.probePort = r.probePort
	if _, err := fresh.saveWithoutDeploy(exposePayload("app-9", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	for _, p := range portsOf(t, fresh, "app-9") {
		if owner, dup := seen[p.Host]; dup {
			t.Fatalf("port %d reused after a restart, held by %s", p.Host, owner)
		}
	}
}

func TestRangeExhausted(t *testing.T) {
	r, _ := portRepo(t, 31000, 31001, 31002, 31003, 31004, 31005, 31006, 31007, 31008)
	_, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ""))
	if err == nil || !strings.Contains(err.Error(), "db:5432/tcp: no free port left in EXPOSE_PORT_RANGE 31000-31009") {
		t.Fatalf("err = %v", err)
	}
	if r.appExists("app-1") {
		t.Fatal("a refused first save left the app behind")
	}
}

func TestPinnedPorts(t *testing.T) {
	r, _ := portRepo(t, 6000)
	pinned := `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, pinned)); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1")["db:5432/tcp"]; got != (model.PortAllocation{Host: 5432, Pinned: true}) {
		t.Fatalf("pin = %+v", got)
	}
	// A save without a ports key (Settings saving a colour) keeps the pin.
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if !portsOf(t, r, "app-1")["db:5432/tcp"].Pinned {
		t.Fatal("a save without ports dropped the pin")
	}
	// Another app can't pin it, nor take it from the range.
	_, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, pinned))
	if !errors.Is(err, ErrPortTaken) || !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "port 5432 is used by the app app-1") {
		t.Fatalf("pin held by another app: %v", err)
	}
	// A pin the probe finds busy refuses the save and keeps the last commit.
	head, _ := gitHead(r.appDataDir("app-1"))
	_, err = r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{"db:5432/tcp":{"host":6000,"pinned":true}}`))
	if !errors.Is(err, ErrPortTaken) || !strings.Contains(err.Error(), "db:5432/tcp: port 6000 is busy on this server") {
		t.Fatalf("busy pin: %v", err)
	}
	if now, _ := gitHead(r.appDataDir("app-1")); now != head || portsOf(t, r, "app-1")["db:5432/tcp"].Host != 5432 {
		t.Fatal("a refused save changed the app")
	}
	// Unpinning keeps the port.
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{}`)); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1")["db:5432/tcp"]; got != (model.PortAllocation{Host: 5432}) {
		t.Fatalf("unpinned = %+v", got)
	}
	// Two entries can't share a pin.
	_, err = r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{"app:8080/http":{"host":7000,"pinned":true},"db:5432/tcp":{"host":7000,"pinned":true}}`))
	if !errors.Is(err, model.ErrPortsInvalid) {
		t.Fatalf("shared pin: %v", err)
	}
	// A malformed ports key is refused before anything is written.
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{"db:5432/tcp":{"host":0,"pinned":true}}`)); !errors.Is(err, model.ErrPortsInvalid) {
		t.Fatalf("malformed: %v", err)
	}
}

// Entries the compose file drops lose their port; a rollback brings the
// revision's ports back, or new ones where another app took them since.
func TestRemovedEntriesAndRollback(t *testing.T) {
	r, _ := portRepo(t)
	v1, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1"); len(got) != 0 {
		t.Fatalf("after removing the entries: %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), configRel))
	if strings.Contains(string(raw), `"ports"`) {
		t.Fatalf("config keeps an empty ports key: %s", raw)
	}
	if _, err := r.rollbackWithoutDeploy("app-1", v1); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1"); got["app:8080/http"].Host != 31000 || got["db:5432/tcp"].Host != 31001 {
		t.Fatalf("after rollback: %+v", got)
	}
	// Another app takes 31000 while app-1 is rolled forward and back again.
	v3, _ := gitHead(r.appDataDir("app-1"))
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.rollbackWithoutDeploy("app-1", v3); err != nil {
		t.Fatal(err)
	}
	mine, theirs := portsOf(t, r, "app-1"), portsOf(t, r, "app-2")
	if mine["app:8080/http"].Host == theirs["app:8080/http"].Host || mine["app:8080/http"].Host == 0 {
		t.Fatalf("rollback took app-2's port: %+v vs %+v", mine, theirs)
	}
}

// A rollback to a revision whose pin another app holds is refused and
// leaves the app as it was.
func TestRollbackRefusedByAPinAnotherAppHolds(t *testing.T) {
	r, _ := portRepo(t)
	v1, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`)); err != nil {
		t.Fatal(err)
	}
	head, _ := gitHead(r.appDataDir("app-1"))
	if _, err := r.rollbackWithoutDeploy("app-1", v1); !errors.Is(err, ErrPortTaken) {
		t.Fatalf("err = %v", err)
	}
	if now, _ := gitHead(r.appDataDir("app-1")); now != head {
		t.Fatal("a refused rollback committed")
	}
	raw, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), "compose.yml"))
	if strings.Contains(string(raw), "x-winterflow") {
		t.Fatalf("worktree left at the refused revision:\n%s", raw)
	}
}

func TestForgetAppFreesItsPorts(t *testing.T) {
	r, _ := portRepo(t)
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(r.appDataDir("app-1")); err != nil {
		t.Fatal(err)
	}
	r.forgetApp("app-1")
	if _, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-2")["app:8080/http"].Host; got != 31000 {
		t.Fatalf("app-2 got %d, want the freed 31000", got)
	}
}

// An ingress hostname may only point at an http expose entry of the app.
func TestSaveRefusesANonHTTPExposeTarget(t *testing.T) {
	r, _ := portRepo(t)
	ing := `"ingress":{"domains":[{"domain":"db.example.com","expose":"db:5432","ssl":true}],"redirects":[]}`
	_, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ing))
	if !errors.Is(err, model.ErrIngressInvalid) || !strings.Contains(err.Error(), "db:5432 is not an http expose entry") {
		t.Fatalf("err = %v", err)
	}
	ok := `"ingress":{"domains":[{"domain":"app.example.com","expose":"app:8080","ssl":true}],"redirects":[]}`
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ok)); err != nil {
		t.Fatal(err)
	}
}

// An x-winterflow block the format refuses refuses the save.
func TestSaveRefusesAnInvalidBlock(t *testing.T) {
	r, _ := portRepo(t)
	_, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: a }\nx-winterflow:\n  volumes: {}\n", ""))
	if err == nil || !strings.Contains(err.Error(), "x-winterflow.volumes: unknown key") || r.appExists("app-1") {
		t.Fatalf("err = %v", err)
	}
}

// A git-sourced app's upstream gaining an expose entry gets its port with
// the source update's commit.
func TestSourceUpdateAllocatesPorts(t *testing.T) {
	r, _ := portRepo(t)
	up, commit := newUpstream(t)
	commit("compose.yml", "services:\n  app: { image: nginx }\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	commit("compose.yml", exposeCompose)
	changed, _, err := r.refreshSourceWithoutDeploy("git-app")
	if err != nil || !changed {
		t.Fatalf("refresh = %v %v", changed, err)
	}
	if got := portsOf(t, r, "git-app"); got["app:8080/http"].Host != 31000 {
		t.Fatalf("ports = %+v", got)
	}
	var cfg map[string]json.RawMessage
	raw, _ := os.ReadFile(filepath.Join(r.appDataDir("git-app"), configRel))
	if json.Unmarshal(raw, &cfg) != nil || cfg["ports"] == nil {
		t.Fatalf("config = %s", raw)
	}
}

func TestProbeListen(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := probeListen("127.0.0.1", port, "http"); err == nil {
		t.Fatal("a held tcp port probed free")
	}
	l.Close()
	if err := probeListen("127.0.0.1", port, "tcp"); err != nil {
		t.Fatalf("a free port probed busy: %v", err)
	}
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := probeListen("127.0.0.1", c.LocalAddr().(*net.UDPAddr).Port, "udp"); err == nil {
		t.Fatal("a held udp port probed free")
	}
}

// After an agent restart the index is built from disk before a save or
// rollback replaces the app's config.json: the app's own ports are still
// its own (never probed) and a pin another app holds is still refused.
func TestRestartedAgentKeepsPortsAndPins(t *testing.T) {
	pinned := `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`
	heldByOwnContainers := func(string, int, string) error { return errors.New("held by a running container") }

	t.Run("save without ports keeps them", func(t *testing.T) {
		r, _ := portRepo(t)
		if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
			t.Fatal(err)
		}
		first := portsOf(t, r, "app-1")
		fresh := NewRepository(r.cfg, r.log)
		fresh.probePort = heldByOwnContainers
		if _, err := fresh.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
			t.Fatal(err)
		}
		if got := portsOf(t, fresh, "app-1"); !reflect.DeepEqual(got, first) {
			t.Fatalf("ports moved after a restart: %+v -> %+v", first, got)
		}
	})

	t.Run("colliding pin refused", func(t *testing.T) {
		r, _ := portRepo(t)
		if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, pinned)); err != nil {
			t.Fatal(err)
		}
		fresh := NewRepository(r.cfg, r.log)
		fresh.probePort = heldByOwnContainers
		_, err := fresh.saveWithoutDeploy(exposePayload("app-2", exposeCompose, pinned))
		if !errors.Is(err, ErrPortTaken) || !strings.Contains(err.Error(), "port 5432 is used by the app app-1") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("rollback keeps the revision's ports without probing", func(t *testing.T) {
		r, _ := portRepo(t)
		v1, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ""))
		if err != nil {
			t.Fatal(err)
		}
		first := portsOf(t, r, "app-1")
		if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"note":"v2"`)); err != nil {
			t.Fatal(err)
		}
		fresh := NewRepository(r.cfg, r.log)
		fresh.probePort = heldByOwnContainers
		if _, err := fresh.rollbackWithoutDeploy("app-1", v1); err != nil {
			t.Fatal(err)
		}
		if got := portsOf(t, fresh, "app-1"); !reflect.DeepEqual(got, first) {
			t.Fatalf("ports moved on rollback after a restart: %+v -> %+v", first, got)
		}
	})

	t.Run("rollback refused by another app's pin", func(t *testing.T) {
		r, _ := portRepo(t)
		v1, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, pinned))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
			t.Fatal(err)
		}
		if _, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, pinned)); err != nil {
			t.Fatal(err)
		}
		fresh := NewRepository(r.cfg, r.log)
		fresh.probePort = heldByOwnContainers
		if _, err := fresh.rollbackWithoutDeploy("app-1", v1); !errors.Is(err, ErrPortTaken) {
			t.Fatalf("err = %v", err)
		}
	})
}

// app.get shows the declared variables and each expose entry with its host
// port, bind address and pin.
func TestGetAppShowsExposeAndDeclaredVariables(t *testing.T) {
	r, _ := portRepo(t)
	compose := exposeCompose + "  variables:\n    LONELY: {}\n"
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", compose, `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`)); err != nil {
		t.Fatal(err)
	}
	resp, err := r.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []command.ExposedPort{
		{ID: "app:8080/http", Container: "app", Port: 8080, Protocol: "http", HostPort: 31000, Bind: "127.0.0.1"},
		{ID: "db:5432/tcp", Container: "db", Port: 5432, Protocol: "tcp", HostPort: 5432, Bind: "0.0.0.0", Pinned: true},
	}
	if !reflect.DeepEqual(resp.Expose, want) {
		t.Fatalf("expose = %+v", resp.Expose)
	}
	if len(resp.DeclaredVariables) != 1 || resp.DeclaredVariables[0].Name != "LONELY" || len(resp.XWinterflowWarnings) != 1 {
		t.Fatalf("variables = %+v, warnings = %v", resp.DeclaredVariables, resp.XWinterflowWarnings)
	}
}
