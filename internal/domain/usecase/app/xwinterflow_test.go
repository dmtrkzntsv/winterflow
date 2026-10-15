package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

const declaredCompose = `services:
  app:
    image: nginx
    environment:
      BASE_URL: http://localhost
  db:
    image: postgres
x-winterflow:
  expose:
    - { container: app, port: 8080 }
    - { container: db, port: 5432, protocol: tcp }
  variables:
    BASE_URL: { required: true, pattern: "https?://.+" }
    TOKEN: { encrypted: true }
`

// The x-winterflow checks the API can make: the block, the declared
// variables' plain values, the pins and the ingress targets. Each refusal
// is a 400 before anything is dispatched.
func TestSaveAppChecksXWinterflow(t *testing.T) {
	ctx := context.Background()
	save := func(compose, config string, vars ...command.ContentItem) (*recordingDispatcher, error) {
		disp := &recordingDispatcher{}
		p := command.AppPayload{Config: []byte(config), Variables: vars,
			Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}}}
		_, err := newStoragesUseCase(disp, nil).SaveApp(ctx, "u", "srv-1", model.App{Name: "demo"}, p, false)
		return disp, err
	}
	plain := func(name, value string) command.ContentItem {
		return command.ContentItem{Name: name, Content: []byte(value)}
	}
	ok := plain("BASE_URL", "https://notes.example.com")
	secret := command.ContentItem{Name: "TOKEN", Encrypted: true, Content: []byte("CIPHERTEXT")}

	if disp, err := save(declaredCompose, `{"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}}`, ok, secret); err != nil || len(disp.all) != 1 {
		t.Fatalf("valid save: %v, %d dispatches", err, len(disp.all))
	}
	// A pin of an entry the compose file no longer has (the editor still
	// sends what app.get returned) is dropped by the agent, not refused.
	if disp, err := save(declaredCompose, `{"ports":{"web:80/http":{"host":80,"pinned":true},"db:5432/tcp":{"host":80,"pinned":true}}}`, ok); err != nil || len(disp.all) != 1 {
		t.Fatalf("stale pin: %v, %d dispatches", err, len(disp.all))
	}
	cases := []struct {
		name, compose, config string
		vars                  []command.ContentItem
		want                  string
	}{
		{"invalid block", "services: {}\nx-winterflow:\n  volumes: {}\n", `{}`, nil, "x-winterflow.volumes: unknown key"},
		{"required missing", declaredCompose, `{}`, nil, "variable BASE_URL: a value is required"},
		{"pattern", declaredCompose, `{}`, []command.ContentItem{plain("BASE_URL", "notes")}, `variable BASE_URL: "notes" does not match`},
		{"dollar", declaredCompose, `{}`, []command.ContentItem{plain("BASE_URL", "https://x/$y")}, "variable BASE_URL: a plain value can't contain $"},
		{"secret sent plain", declaredCompose, `{}`, []command.ContentItem{ok, plain("TOKEN", "t")}, "variable TOKEN: it is a secret"},
		{"malformed pin", declaredCompose, `{"ports":{"db:5432/tcp":{"host":0,"pinned":true}}}`, []command.ContentItem{ok}, "0 is not a port"},
		{"tcp ingress target", declaredCompose, `{"ingress":{"domains":[{"domain":"db.example.com","expose":"db:5432","ssl":true}],"redirects":[]}}`,
			[]command.ContentItem{ok}, "db:5432 is not an http expose entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			disp, err := save(tc.compose, tc.config, tc.vars...)
			if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(disp.all) != 0 {
				t.Fatal("a refused save reached the agent")
			}
		})
	}
}

// A draft may leave a required declared variable empty (the ruling at
// Task 6); its pattern, $ and secret checks still apply.
func TestSaveAppDraftSkipsOnlyTheRequiredRule(t *testing.T) {
	ctx := context.Background()
	save := func(draft bool, vars ...command.ContentItem) (*recordingDispatcher, error) {
		disp := &recordingDispatcher{}
		p := command.AppPayload{Config: []byte(`{}`), Variables: vars,
			Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(declaredCompose)}}}
		_, err := newStoragesUseCase(disp, nil).SaveApp(ctx, "u", "srv-1", model.App{Name: "demo"}, p, draft)
		return disp, err
	}
	if disp, err := save(true); err != nil || len(disp.all) != 1 {
		t.Fatalf("draft without the required value: %v, %d dispatches", err, len(disp.all))
	}
	if _, err := save(false); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "a value is required") {
		t.Fatalf("deploy without the required value: %v", err)
	}
	if _, err := save(true, command.ContentItem{Name: "BASE_URL", Content: []byte("notes")}); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("draft with a pattern mismatch: %v", err)
	}
	if _, err := save(true, command.ContentItem{Name: "TOKEN", Content: []byte("t")}); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "it is a secret") {
		t.Fatalf("draft with a secret sent plain: %v", err)
	}
}

// A git-sourced app's compose file is upstream: the agent checks it; the
// pins' syntax is still checked here.
func TestSaveAppLeavesAGitSourcedBlockToTheAgent(t *testing.T) {
	src := &command.SourcePayload{RepoURL: "https://x", Branch: "main"}
	disp := &recordingDispatcher{}
	p := command.AppPayload{Config: []byte(`{"ingress":{"domains":[{"domain":"a.example.com","expose":"web:80","ssl":true}],"redirects":[]}}`), Source: src}
	if _, err := newStoragesUseCase(disp, nil).SaveApp(context.Background(), "u", "srv-1", model.App{Name: "demo"}, p, false); err != nil || len(disp.all) != 1 {
		t.Fatalf("err = %v, dispatches %d", err, len(disp.all))
	}
	p.Config = []byte(`{"ports":{"web:80/http":{"host":70000,"pinned":true}}}`)
	if _, err := newStoragesUseCase(disp, nil).SaveApp(context.Background(), "u", "srv-1", model.App{Name: "demo"}, p, false); !errors.Is(err, model.ErrPortsInvalid) {
		t.Fatalf("err = %v", err)
	}
}
