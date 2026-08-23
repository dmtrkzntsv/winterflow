package model

import (
	"strings"
	"testing"
)

func validIngress() Ingress {
	return Ingress{
		Domains: []IngressDomain{
			{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true},
			{Domain: "internal.lan", UpstreamPort: 9000, SSL: false},
		},
		Redirects: []IngressRedirect{
			{Domain: "www.example.com", To: "https://blog.example.com", Code: 301, SSL: true},
			{Domain: "blog.example.com", Path: "/old-blog/*", To: "https://blog.example.com/blog", Code: 302},
		},
	}
}

func TestValidateAccepts(t *testing.T) {
	ing := validIngress()
	if err := ing.Validate(); err != nil {
		t.Fatalf("valid ingress rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Ingress)
		errHas string
	}{
		{"empty domain", func(i *Ingress) { i.Domains[0].Domain = "" }, "domain"},
		{"scheme in domain", func(i *Ingress) { i.Domains[0].Domain = "https://x.com" }, "hostname"},
		{"wildcard", func(i *Ingress) { i.Domains[0].Domain = "*.example.com" }, "hostname"},
		{"port in domain", func(i *Ingress) { i.Domains[0].Domain = "x.com:8080" }, "hostname"},
		{"trailing dot", func(i *Ingress) { i.Domains[0].Domain = "x.com." }, "hostname"},
		{"unicode", func(i *Ingress) { i.Domains[0].Domain = "bücher.de" }, "hostname"},
		{"uppercase", func(i *Ingress) { i.Domains[0].Domain = "X.COM" }, "hostname"},
		{"port zero", func(i *Ingress) { i.Domains[0].UpstreamPort = 0 }, "port"},
		{"port too big", func(i *Ingress) { i.Domains[0].UpstreamPort = 70000 }, "port"},
		{"dup within app", func(i *Ingress) { i.Domains[1].Domain = i.Domains[0].Domain }, "duplicate"},
		{"dup route vs redirect", func(i *Ingress) { i.Redirects[0].Domain = i.Domains[0].Domain }, "duplicate"},
		{"relative target", func(i *Ingress) { i.Redirects[0].To = "/nowhere" }, "absolute"},
		{"ftp target", func(i *Ingress) { i.Redirects[0].To = "ftp://x.com" }, "absolute"},
		{"bad code", func(i *Ingress) { i.Redirects[0].Code = 300 }, "code"},
		{"path rule unknown domain", func(i *Ingress) { i.Redirects[1].Domain = "other.example.com" }, "path rule"},
		{"path without leading slash", func(i *Ingress) { i.Redirects[1].Path = "old/*" }, "path"},
		{"placeholder injection in redirect target", func(i *Ingress) {
			i.Redirects[0].To = "https://attacker.com/x/{env.JWT_SECRET}"
		}, "must not contain"},
		{"placeholder injection in path rule", func(i *Ingress) {
			i.Redirects[1].Path = "/old/{http.request.header.Authorization}"
		}, "must not contain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ing := validIngress()
			tc.mutate(&ing)
			err := ing.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.errHas) {
				t.Fatalf("error %q does not mention %q", err, tc.errHas)
			}
		})
	}
}

func TestValidateAcceptsSingleLabelHost(t *testing.T) {
	// Dev setups use hosts like "localhost"; ACME won't issue for them, but
	// ssl:false single labels are legitimate.
	ing := Ingress{Domains: []IngressDomain{{Domain: "localhost", UpstreamPort: 8080}}}
	if err := ing.Validate(); err != nil {
		t.Fatalf("single-label host rejected: %v", err)
	}
}

func TestDomainNames(t *testing.T) {
	ing := validIngress()
	got := ing.DomainNames()
	// Route domains + domain-level redirect sources; path rules excluded.
	want := []string{"blog.example.com", "internal.lan", "www.example.com"}
	if len(got) != len(want) {
		t.Fatalf("DomainNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DomainNames = %v, want %v", got, want)
		}
	}
}

func TestParseIngress(t *testing.T) {
	ing, err := ParseIngress([]byte(`{"name":"x","ingress":{"domains":[{"domain":"a.example.com","upstream_port":81,"ssl":true}],"redirects":[]}}`))
	if err != nil || ing == nil {
		t.Fatalf("ParseIngress: %v, %v", ing, err)
	}
	if ing.Domains[0].Domain != "a.example.com" || ing.Domains[0].UpstreamPort != 81 || !ing.Domains[0].SSL {
		t.Fatalf("parsed = %+v", ing)
	}

	// Missing key => nil, nil (feature untouched).
	ing, err = ParseIngress([]byte(`{"name":"x"}`))
	if err != nil || ing != nil {
		t.Fatalf("missing key: got %v, %v; want nil, nil", ing, err)
	}

	// Broken JSON => error.
	if _, err := ParseIngress([]byte(`{`)); err == nil {
		t.Fatal("broken JSON accepted")
	}
}
