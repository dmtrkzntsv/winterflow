package caddy

import (
	"encoding/json"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
)

// view is the slice of the emitted config the mode tests read.
type view struct {
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				Routes []struct {
					Match []struct {
						Host []string `json:"host"`
						Not  []struct {
							Host []string `json:"host"`
						} `json:"not"`
					} `json:"match"`
					Handle []map[string]any `json:"handle"`
				} `json:"routes"`
				AutomaticHTTPS *struct {
					SkipCertificates []string `json:"skip_certificates"`
				} `json:"automatic_https"`
			} `json:"servers"`
		} `json:"http"`
		TLS *struct {
			Automation *struct {
				Policies []struct {
					Subjects []string `json:"subjects"`
				} `json:"policies"`
			} `json:"automation"`
			Certificates *struct {
				LoadFiles []struct {
					Certificate string `json:"certificate"`
					Key         string `json:"key"`
				} `json:"load_files"`
			} `json:"certificates"`
		} `json:"tls"`
	} `json:"apps"`
}

func decodeView(t *testing.T, raw []byte) view {
	t.Helper()
	var v view
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return v
}

// routeKinds lists a server's routes as "<handler> <hosts>" in order, e.g.
// "reverse_proxy a.test" or "404 *.x.test !y.x.test".
func routeKinds(v view, server string) []string {
	var out []string
	for _, rt := range v.Apps.HTTP.Servers[server].Routes {
		h := rt.Handle[0]
		kind, _ := h["handler"].(string)
		if kind == "static_response" && h["status_code"] == "404" {
			kind = "404"
		}
		var hosts []string
		for _, m := range rt.Match {
			hosts = append(hosts, m.Host...)
			for _, n := range m.Not {
				for _, x := range n.Host {
					hosts = append(hosts, "!"+x)
				}
			}
		}
		out = append(out, strings.TrimSpace(kind+" "+strings.Join(hosts, " ")))
	}
	return out
}

func modeRegs() []domains.Registration {
	return []domains.Registration{
		{Name: "*.cert.test", TLS: command.DomainTLSCertificate, CertFile: "/d/_wildcard_.cert.test.crt", KeyFile: "/d/_wildcard_.cert.test.key"},
		{Name: "x.cert.test", TLS: command.DomainTLSOff}, // exact wins over the wildcard
		{Name: "auto.test", TLS: command.DomainTLSAuto},
		{Name: "*.auto.test", TLS: command.DomainTLSAuto}, // no app uses it
		{Name: "*.tunnel.test", TLS: command.DomainTLSOff},
	}
}

func modeApps() []AppIngress {
	return []AppIngress{{AppID: "app1", Ingress: model.Ingress{
		Domains: []model.IngressDomain{
			{Domain: "a.cert.test", UpstreamPort: 8081, SSL: false}, // ssl is ignored now
			{Domain: "auto.test", UpstreamPort: 8082},
			{Domain: "b.tunnel.test", UpstreamPort: 8083, SSL: true},
			{Domain: "x.cert.test", UpstreamPort: 8084},
			{Domain: "uncovered.example", UpstreamPort: 8085},
		},
		Redirects: []model.IngressRedirect{
			{Domain: "old.tunnel.test", To: "https://auto.test", Code: 301, SSL: true},
			{Domain: "auto.test", Path: "/legacy/*", To: "https://auto.test/new", Code: 302},
		},
	}}}
}

func TestBuildConfigPlacesHostsByRegistrationMode(t *testing.T) {
	raw, warnings, err := BuildConfig(modeApps(), modeRegs(), Options{RateLimitRPS: 50, RateLimitBurst: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "uncovered.example is not covered") {
		t.Fatalf("warnings = %v, want only the uncovered host", warnings)
	}
	if strings.Contains(string(raw), "uncovered.example") {
		t.Error("uncovered host leaked into the config")
	}
	v := decodeView(t, raw)

	wantHTTPS := []string{
		"winterflow_rate_limit",
		"static_response auto.test", // path rule before its domain's route
		"reverse_proxy a.cert.test",
		"reverse_proxy auto.test",
		"404 *.cert.test !x.cert.test",
		"404 auto.test",
		"404 *.auto.test",
	}
	wantHTTP := []string{
		"winterflow_rate_limit",
		"reverse_proxy b.tunnel.test",
		"reverse_proxy x.cert.test",
		"static_response old.tunnel.test",
		"404 x.cert.test",
		"404 *.tunnel.test",
	}
	if got := routeKinds(v, "https"); strings.Join(got, "\n") != strings.Join(wantHTTPS, "\n") {
		t.Errorf("https routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantHTTPS, "\n"))
	}
	if got := routeKinds(v, "http"); strings.Join(got, "\n") != strings.Join(wantHTTP, "\n") {
		t.Errorf("http routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantHTTP, "\n"))
	}

	// ACME subjects: only hosts under auto registrations.
	if v.Apps.TLS == nil || v.Apps.TLS.Automation == nil {
		t.Fatal("no tls automation for the auto host")
	}
	if subj := v.Apps.TLS.Automation.Policies[0].Subjects; strings.Join(subj, ",") != "auto.test" {
		t.Errorf("ACME subjects = %v, want [auto.test]", subj)
	}
	// The uploaded pair is loaded from disk.
	lf := v.Apps.TLS.Certificates.LoadFiles
	if len(lf) != 1 || lf[0].Certificate != "/d/_wildcard_.cert.test.crt" || lf[0].Key != "/d/_wildcard_.cert.test.key" {
		t.Errorf("load_files = %+v", lf)
	}
	// The unused auto wildcard never gets a certificate order; the served
	// auto host does.
	ah := v.Apps.HTTP.Servers["https"].AutomaticHTTPS
	if ah == nil || strings.Join(ah.SkipCertificates, ",") != "*.auto.test" {
		t.Errorf("automatic_https = %+v, want skip_certificates [*.auto.test]", ah)
	}
}

// TestBuildConfigCertificateHostsAreNotACMESubjects: a certificate-mode
// registration (wildcard or exact) never puts a host in an ACME policy, and
// with no auto host there is no automation at all.
func TestBuildConfigCertificateHostsAreNotACMESubjects(t *testing.T) {
	regs := []domains.Registration{
		{Name: "*.cert.test", TLS: command.DomainTLSCertificate, CertFile: "/c.crt", KeyFile: "/c.key"},
		{Name: "exact.test", TLS: command.DomainTLSCertificate, CertFile: "/e.crt", KeyFile: "/e.key"},
	}
	apps := []AppIngress{{AppID: "a", Ingress: model.Ingress{Domains: []model.IngressDomain{
		{Domain: "a.cert.test", UpstreamPort: 1, SSL: true},
		{Domain: "exact.test", UpstreamPort: 2, SSL: true},
	}}}}
	raw, warnings, err := BuildConfig(apps, regs, Options{})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("BuildConfig: %v %v", err, warnings)
	}
	v := decodeView(t, raw)
	if v.Apps.TLS == nil || v.Apps.TLS.Automation != nil {
		t.Fatalf("tls = %+v, want certificates only, no automation", v.Apps.TLS)
	}
	if n := len(v.Apps.TLS.Certificates.LoadFiles); n != 2 {
		t.Errorf("load_files = %d, want 2", n)
	}
	if ah := v.Apps.HTTP.Servers["https"].AutomaticHTTPS; ah != nil {
		t.Errorf("certificate catch-alls need no skip list: %+v", ah)
	}
}

// TestBuildConfigWithoutRegistrationsServesNothing: after the upgrade an
// install with no registrations excludes every host (fail closed).
func TestBuildConfigWithoutRegistrationsServesNothing(t *testing.T) {
	raw, warnings, err := BuildConfig(sampleApps(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v, want one per host", warnings)
	}
	v := decodeView(t, raw)
	for name, srv := range v.Apps.HTTP.Servers {
		if len(srv.Routes) != 0 {
			t.Errorf("server %s has routes: %v", name, routeKinds(v, name))
		}
	}
	if v.Apps.TLS != nil {
		t.Error("tls app present without registrations")
	}
}
