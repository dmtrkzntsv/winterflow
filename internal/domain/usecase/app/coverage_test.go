package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
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
		model.RegisteredDomain{Name: "*.twillingate.app", TLS: model.DomainTLSCertificate},
		model.RegisteredDomain{Name: "twillingate.app", TLS: model.DomainTLSAuto},
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
	if !strings.Contains(err.Error(), "add shop.example.com or *.example.com") {
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
		model.RegisteredDomain{Name: "*.twillingate.app", TLS: model.DomainTLSCertificate},
		model.RegisteredDomain{Name: "app.twillingate.app", TLS: model.DomainTLSOff},
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

// errServerDomains fails every ListForServer.
type errServerDomains struct{ *fakeServerDomains }

func (errServerDomains) ListForServer(context.Context, string) ([]model.RegisteredDomain, error) {
	return nil, errors.New("db down")
}

func TestSaveAppGrandfathersHeldHostnames(t *testing.T) {
	ctx := context.Background()
	// Right after an upgrade the registration index is still empty, but the
	// app_domains index already lists what existing apps serve.
	held := &fakeDomainRepo{held: map[string][]model.AppDomainInfo{
		"app-1": {{Domain: "shop.example.com", Kind: "route"}},
	}}
	newUC := func(disp *recordingDispatcher, claims []model.DomainClaim) *UseCase {
		held.claims = claims
		return NewUseCase(&Deps{
			CommandDispatcher:      disp,
			AppRepository:          noopAppRepo{},
			AppDomainRepository:    held,
			ServerDomainRepository: newFakeServerDomains(),
			Log:                    logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		})
	}
	save := func(uc *UseCase, appID string, cfg []byte) error {
		_, err := uc.SaveApp(ctx, "u", "srv-1", model.App{ID: appID, Name: "demo"}, command.AppPayload{Config: cfg}, false)
		return err
	}

	// The unchanged hostname passes.
	disp := &recordingDispatcher{}
	if err := save(newUC(disp, nil), "app-1", ingressWith("shop.example.com", "")); err != nil || len(disp.all) != 1 {
		t.Fatalf("unchanged host: err %v, dispatches %d", err, len(disp.all))
	}
	// Held hostnames are looked up only for an uncovered host: a covered
	// save never queries them.
	held.forApp = 0
	covered := NewUseCase(&Deps{
		CommandDispatcher:      &recordingDispatcher{},
		AppRepository:          noopAppRepo{},
		AppDomainRepository:    held,
		ServerDomainRepository: newFakeServerDomains(model.RegisteredDomain{Name: "*.example.com", TLS: model.DomainTLSOff}),
		Log:                    logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
	if err := save(covered, "app-1", ingressWith("shop.example.com", "www.example.com")); err != nil || held.forApp != 0 {
		t.Fatalf("covered save: err %v, held lookups %d", err, held.forApp)
	}
	// A new hostname on the same app still needs coverage.
	disp = &recordingDispatcher{}
	err := save(newUC(disp, nil), "app-1", ingressWith("shop.example.com", "www.example.com"))
	if !errors.Is(err, model.ErrDomainNotCovered) || len(disp.all) != 0 {
		t.Fatalf("new host: err %v, dispatches %d", err, len(disp.all))
	}
	// Another app cannot take the held hostname this way: it holds nothing,
	// so the hostname still needs coverage.
	disp = &recordingDispatcher{}
	claim := []model.DomainClaim{{Domain: "shop.example.com", AppName: "Shop", ServerID: "srv-1"}}
	err = save(newUC(disp, claim), "app-2", ingressWith("shop.example.com", ""))
	if !errors.Is(err, model.ErrDomainNotCovered) || len(disp.all) != 0 {
		t.Fatalf("other app: err %v", err)
	}
	// Even a holder is still refused when someone else claims the hostname.
	err = save(newUC(disp, claim), "app-1", ingressWith("shop.example.com", ""))
	if !errors.Is(err, model.ErrDomainTaken) || len(disp.all) != 0 {
		t.Fatalf("claimed host: err %v", err)
	}
}

func TestSaveAppSurfacesIndexErrors(t *testing.T) {
	ctx := context.Background()
	cfg := command.AppPayload{Config: ingressWith("shop.example.com", "")}
	base := Deps{
		AppRepository: noopAppRepo{},
		Log:           logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	}
	cases := map[string]Deps{
		"registrations": func() Deps {
			d := base
			d.ServerDomainRepository = errServerDomains{newFakeServerDomains()}
			return d
		}(),
		"held hostnames": func() Deps {
			d := base
			d.ServerDomainRepository = newFakeServerDomains()
			d.AppDomainRepository = &fakeDomainRepo{heldErr: errors.New("db down")}
			return d
		}(),
	}
	for name, d := range cases {
		disp := &recordingDispatcher{}
		d.CommandDispatcher = disp
		_, err := NewUseCase(&d).SaveApp(ctx, "u", "srv-1", model.App{ID: "app-1", Name: "demo"}, cfg, false)
		if err == nil || errors.Is(err, model.ErrDomainNotCovered) || len(disp.all) != 0 {
			t.Errorf("%s: err %v, dispatches %d", name, err, len(disp.all))
		}
	}
}

func TestCheckDomainSurfacesIndexErrors(t *testing.T) {
	ctx := context.Background()
	logr := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	regErr := NewUseCase(&Deps{AppRepository: noopAppRepo{}, Log: logr,
		ServerDomainRepository: errServerDomains{newFakeServerDomains()}})
	if _, err := regErr.CheckDomain(ctx, "srv-1", "shop.example.com", ""); err == nil {
		t.Error("registration list error swallowed")
	}
	claimErr := NewUseCase(&Deps{AppRepository: noopAppRepo{}, Log: logr,
		AppDomainRepository: &fakeDomainRepo{claimErr: errors.New("db down")}})
	if _, err := claimErr.CheckDomain(ctx, "srv-1", "shop.example.com", ""); err == nil {
		t.Error("claim lookup error swallowed")
	}
}
