package xwinterflow

import (
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func TestCheckTargets(t *testing.T) {
	s := parse(t, ModeApp, notes)
	ok := &model.Ingress{Domains: []model.IngressDomain{{Domain: "notes.example.com", Expose: "app:8080"}, {Domain: "old.example.com", UpstreamPort: 9000}}}
	if err := s.CheckTargets(ok); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckTargets(nil); err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]string{
		"db:5432":  "db:5432 is not an http expose entry",
		"app:9999": "app:9999 is not an http expose entry",
	} {
		ing := &model.Ingress{Domains: []model.IngressDomain{{Domain: "notes.example.com", Expose: target}}}
		if err := s.CheckTargets(ing); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v", target, err)
		}
	}
}

func TestCheckPins(t *testing.T) {
	s := parse(t, ModeApp, notes)
	if err := s.CheckPins(map[string]model.PortAllocation{"db:5432/tcp": {Host: 5432, Pinned: true}, "gone:1/http": {Host: 1}}); err != nil {
		t.Fatal(err)
	}
	// A pin for an entry the compose file no longer has is dropped by the
	// save, never refused: not even when it shares a live pin's port.
	stale := map[string]model.PortAllocation{"web:80/http": {Host: 5432, Pinned: true}, "db:5432/tcp": {Host: 5432, Pinned: true}}
	if err := s.CheckPins(stale); err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]model.PortAllocation{
		"app:8080/http and db:5432/tcp are both pinned to port": {"app:8080/http": {Host: 5432, Pinned: true}, "db:5432/tcp": {Host: 5432, Pinned: true}},
	}
	for want, pins := range cases {
		if err := s.CheckPins(pins); !errors.Is(err, model.ErrPortsInvalid) || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q", err, want)
		}
	}
}
