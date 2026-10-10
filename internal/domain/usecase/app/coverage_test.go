package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// ingressWith is an app config whose ingress routes host and redirects
// redirect (a domain-level redirect source) when non-empty.
func ingressWith(host, redirect string) []byte {
	cfg := `{"name":"demo","ingress":{"domains":[{"domain":"` + host + `","upstream_port":8088}],"redirects":[`
	if redirect != "" {
		cfg += `{"domain":"` + redirect + `","to":"https://` + host + `","code":301}`
	}
	return []byte(cfg + `]}}`)
}

func TestSaveAppRequiresACoveringRegistration(t *testing.T) {
	sd := newFakeServerDomains(
		model.RegisteredDomain{Name: "*.twillingate.app", TLS: "certificate"},
		model.RegisteredDomain{Name: "twillingate.app", TLS: "auto"},
	)
	ctx := context.Background()
	save := func(cfg []byte) (*recordingDispatcher, error) {
		disp := &recordingDispatcher{}
		_, err := newDomainsUseCase(disp, sd).SaveApp(ctx, "u", "srv-1", model.App{Name: "demo"}, command.AppPayload{Config: cfg}, false)
		return disp, err
	}

	for _, ok := range [][]byte{
		ingressWith("shop.twillingate.app", ""),
		ingressWith("shop.twillingate.app", "twillingate.app"),
		[]byte(`{"name":"no ingress"}`),
	} {
		if disp, err := save(ok); err != nil || len(disp.all) != 1 {
			t.Errorf("%s: err %v, dispatches %d", ok, err, len(disp.all))
		}
	}

	cases := map[string]struct {
		cfg  []byte
		host string
	}{
		"two labels deep":    {ingressWith("a.shop.twillingate.app", ""), "a.shop.twillingate.app"},
		"other domain":       {ingressWith("shop.example.com", ""), "shop.example.com"},
		"uncovered redirect": {ingressWith("shop.twillingate.app", "www.example.com"), "www.example.com"},
	}
	for name, tc := range cases {
		disp, err := save(tc.cfg)
		if !errors.Is(err, model.ErrDomainNotCovered) || !strings.Contains(err.Error(), tc.host+" is not covered") {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(disp.all) != 0 {
			t.Errorf("%s: an uncovered save reached the agent", name)
		}
	}
	// The hint names the wildcard for the host's parent.
	_, err := save(ingressWith("shop.example.com", ""))
	if !strings.Contains(err.Error(), "register shop.example.com or *.example.com") {
		t.Errorf("hint = %v", err)
	}
	// Another server's registrations do not count.
	disp := &recordingDispatcher{}
	_, err = newDomainsUseCase(disp, sd).SaveApp(ctx, "u", "srv-2", model.App{Name: "demo"},
		command.AppPayload{Config: ingressWith("shop.twillingate.app", "")}, false)
	if !errors.Is(err, model.ErrDomainNotCovered) {
		t.Errorf("srv-2 save = %v, want not covered", err)
	}
}

func TestCheckDomainReportsTheWinningRegistration(t *testing.T) {
	sd := newFakeServerDomains(
		model.RegisteredDomain{Name: "*.twillingate.app", TLS: "certificate"},
		model.RegisteredDomain{Name: "app.twillingate.app", TLS: "off"},
	)
	uc := newDomainsUseCase(&recordingDispatcher{}, sd)
	ctx := context.Background()
	for host, want := range map[string]string{
		"shop.twillingate.app": "*.twillingate.app",
		"app.twillingate.app":  "app.twillingate.app", // exact wins
		"a.b.twillingate.app":  "",
	} {
		check, err := uc.CheckDomain(ctx, "srv-1", host, "")
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if check.Registration != nil {
			got = check.Registration.Name
		}
		if got != want {
			t.Errorf("%s: registration %q, want %q", host, got, want)
		}
	}
}
