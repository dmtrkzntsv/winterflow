package command

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateDomainRequestValidate(t *testing.T) {
	ok := []CreateDomainRequest{
		{Name: "twillingate.app", TLS: DomainTLSAuto},
		{Name: "*.twillingate.app", TLS: DomainTLSOff},
		{Name: "*.twillingate.app", TLS: DomainTLSCertificate, Certificate: "PEM", Key: "CIPHERTEXT"},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v should be valid: %v", r, err)
		}
	}
	bad := map[string]CreateDomainRequest{
		"bad name":          {Name: "*.*.x.app", TLS: DomainTLSAuto},
		"uppercase":         {Name: "X.app", TLS: DomainTLSAuto},
		"no mode":           {Name: "x.app"},
		"unknown mode":      {Name: "x.app", TLS: "manual"},
		"cert without key":  {Name: "x.app", TLS: DomainTLSCertificate, Certificate: "PEM"},
		"key without cert":  {Name: "x.app", TLS: DomainTLSCertificate, Key: "C"},
		"cert in auto mode": {Name: "x.app", TLS: DomainTLSAuto, Certificate: "PEM", Key: "C"},
		"key in off mode":   {Name: "x.app", TLS: DomainTLSOff, Key: "C"},
	}
	for name, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: %+v should be rejected", name, r)
		}
	}
	err := CreateDomainRequest{Name: "x.app", TLS: "manual"}.Validate()
	if err == nil || !strings.Contains(err.Error(), "auto, certificate or off") {
		t.Errorf("mode error = %v", err)
	}
}

func TestDeleteDomainRequestValidate(t *testing.T) {
	if err := (DeleteDomainRequest{Name: "*.x.app"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"", "../x", "x/y", "*"} {
		if err := (DeleteDomainRequest{Name: n}).Validate(); err == nil {
			t.Errorf("%q should be rejected", n)
		}
	}
}

func TestDomainAppsMarshalsAsEmptyArray(t *testing.T) {
	raw, err := json.Marshal(Domain{Name: "x.app", TLS: DomainTLSOff})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"apps":[]`) {
		t.Errorf("nil Apps must marshal as []: %s", raw)
	}
	raw, _ = json.Marshal(ListDomainsResponse{Domains: []Domain{{Name: "x.app", TLS: DomainTLSOff, Apps: []string{"a1"}}}})
	if !strings.Contains(string(raw), `"apps":["a1"]`) {
		t.Errorf("apps lost: %s", raw)
	}
}
