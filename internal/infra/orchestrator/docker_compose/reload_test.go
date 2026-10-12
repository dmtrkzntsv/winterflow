package dockercompose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

func TestPickCandidates(t *testing.T) {
	running := map[string]bool{"a": true, "b": true, "c": true}
	stale := map[string][]string{"a": {"SMTP_PASS"}, "b": {"S3_KEY"}, "d": {"SMTP_PASS"}} // d is stopped
	exists := func(id string) bool { return id != "gone" }
	cases := map[string]struct {
		req  command.ReloadVarsRequest
		want []string
	}{
		"every stale running app":     {command.ReloadVarsRequest{}, []string{"a", "b"}},
		"stale apps using a name":     {command.ReloadVarsRequest{Names: []string{"SMTP_PASS"}}, []string{"a"}},
		"explicit apps, running only": {command.ReloadVarsRequest{AppIDs: []string{"c", "d", "c", "gone"}}, []string{"c"}},
		"a name nobody is stale on":   {command.ReloadVarsRequest{Names: []string{"NOPE"}}, nil},
	}
	for name, tc := range cases {
		if got := pickCandidates(tc.req, running, stale, exists); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestReloadRefusesUndeployedDraft(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	// A draft: a newer commit than the deployed mark.
	if _, err := r.SaveAppDraft(context.Background(), command.AppPayload{
		AppID: "app-1", Config: []byte(`{"name":"Shop App","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[]}`),
		Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(shopCompose + "# draft\n")}},
	}); err != nil {
		t.Fatal(err)
	}
	// Values change after the draft was saved (a draft renders nothing, P6): stale.
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	stubDockerApp(t, "app-1", "running", 0)
	names, skipped, err := r.ReloadSharedVars(context.Background(), "app-1")
	if !errors.Is(err, ErrDraftPending) {
		t.Fatalf("err = %v, want ErrDraftPending", err)
	}
	if len(names) == 0 || skipped {
		t.Fatalf("names = %v, skipped = %v: the stale names must still be reported for the error message", names, skipped)
	}
}

// A HEAD that cannot be read must not bypass the draft check.
func TestReloadFailsClosedWithoutHead(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	syncVars(t, r, testSnapshot(pub, t, 1, "relay.example", "s3cret"))
	if err := os.RemoveAll(filepath.Join(r.appDataDir("app-1"), ".git")); err != nil {
		t.Fatal(err)
	}
	stubDockerApp(t, "app-1", "running", 0)
	before, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), envSharedRel))
	if _, _, err := r.ReloadSharedVars(context.Background(), "app-1"); err == nil || !strings.Contains(err.Error(), "read HEAD") {
		t.Fatalf("err = %v, want a read HEAD failure", err)
	}
	after, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), envSharedRel))
	if string(before) != string(after) {
		t.Fatal("rendered despite the failed draft check")
	}
}

// An app stopped after the candidates were picked must not be started by
// the reload's compose up.
func TestReloadSkipsAnAppThatIsNoLongerRunning(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	syncVars(t, r, testSnapshot(pub, t, 1, "relay.example", "s3cret"))
	stubDockerApp(t, "app-1", "exited", 1) // compose up would fail the test
	names, skipped, err := r.ReloadSharedVars(context.Background(), "app-1")
	if err != nil || !skipped {
		t.Fatalf("skipped = %v, err = %v; want a skip", skipped, err)
	}
	if !reflect.DeepEqual(names, []string{"SMTP_HOST", "SMTP_PASS"}) {
		t.Fatalf("names = %v", names)
	}
	plain, _ := readEnvFile(t, filepath.Join(r.appDataDir("app-1"), envSharedRel))
	if plain["SMTP_HOST"] == "relay.example" {
		t.Fatal("a skipped app was re-rendered")
	}
	if len(r.StaleApps()["app-1"]) == 0 {
		t.Fatal("a skipped app lost its stale flag")
	}
}

func TestReloadRendersBeforeComposeUp(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	syncVars(t, r, testSnapshot(pub, t, 1, "relay.example", "s3cret"))
	// No docker in unit tests: a stub reports the app running and succeeds.
	stubDockerApp(t, "app-1", "running", 0)
	names, skipped, err := r.ReloadSharedVars(context.Background(), "app-1")
	if err != nil || skipped {
		t.Fatalf("skipped = %v, err = %v", skipped, err)
	}
	if !reflect.DeepEqual(names, []string{"SMTP_HOST", "SMTP_PASS"}) {
		t.Fatalf("names = %v", names)
	}
	plain, _ := readEnvFile(t, filepath.Join(r.appDataDir("app-1"), envSharedRel))
	if plain["SMTP_HOST"] != "relay.example" {
		t.Fatalf("not re-rendered: %v", plain)
	}
	if got := r.StaleApps(); len(got) != 0 {
		t.Fatalf("still stale after reload: %v", got)
	}
	if _, _, err := r.ReloadSharedVars(context.Background(), "missing"); err == nil {
		t.Fatal("reloading a missing app succeeded")
	}
}

func TestReloadReportsComposeFailure(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	syncVars(t, r, testSnapshot(pub, t, 1, "relay.example", "s3cret"))
	stubDockerApp(t, "app-1", "running", 1)
	names, _, err := r.ReloadSharedVars(context.Background(), "app-1")
	if err == nil || len(names) == 0 {
		t.Fatalf("names = %v, err = %v; want the stale names and a compose error", names, err)
	}
	// docker ps itself failing is an error too, not a skip.
	stubDocker(t, 1)
	if _, skipped, err := r.ReloadSharedVars(context.Background(), "app-1"); err == nil || skipped {
		t.Fatalf("skipped = %v, err = %v; want a docker ps error", skipped, err)
	}
	if _, err := r.ReloadCandidates(context.Background(), command.ReloadVarsRequest{}); err == nil {
		t.Fatal("candidates without a working docker ps succeeded")
	}
}
