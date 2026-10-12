package dockercompose

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
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
	// Values change after the draft was saved (and rendered): stale.
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	names, err := r.ReloadSharedVars(context.Background(), "app-1")
	if !errors.Is(err, ErrDraftPending) {
		t.Fatalf("err = %v, want ErrDraftPending", err)
	}
	if len(names) == 0 {
		t.Fatal("the stale names must still be reported for the error message")
	}
}

func TestReloadRendersBeforeComposeUp(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	syncVars(t, r, testSnapshot(pub, t, 1, "relay.example", "s3cret"))
	// No docker in unit tests: put a fake one first on PATH that succeeds.
	stubDocker(t, 0)
	names, err := r.ReloadSharedVars(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
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
	if _, err := r.ReloadSharedVars(context.Background(), "missing"); err == nil {
		t.Fatal("reloading a missing app succeeded")
	}
}

func TestReloadReportsComposeFailure(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	syncVars(t, r, testSnapshot(pub, t, 1, "relay.example", "s3cret"))
	stubDocker(t, 1)
	names, err := r.ReloadSharedVars(context.Background(), "app-1")
	if err == nil || len(names) == 0 {
		t.Fatalf("names = %v, err = %v; want the stale names and a compose error", names, err)
	}
	if _, err := r.ReloadCandidates(context.Background(), command.ReloadVarsRequest{}); err == nil {
		t.Fatal("candidates without a working docker ps succeeded")
	}
}
