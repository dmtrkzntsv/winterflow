package model

import "testing"

func TestIdentitySameIssuer(t *testing.T) {
	const iss = "https://idp.example.test"
	for _, c := range []struct {
		id   Identity
		want bool
	}{
		{Identity{Provider: ProviderOIDC, Issuer: iss}, true},
		{Identity{Provider: ProviderOIDC, Issuer: "https://other.example.test"}, false},
		// A generic OIDC identity without an issuer cannot be trusted.
		{Identity{Provider: ProviderOIDC}, false},
		// Rows that predate the column: fixed-issuer providers.
		{Identity{Provider: ProviderGoogle}, true},
		{Identity{Provider: ProviderApple}, true},
		{Identity{Provider: ProviderGoogle, Issuer: "https://evil.example.test"}, false},
	} {
		if got := c.id.SameIssuer(iss); got != c.want {
			t.Errorf("%+v.SameIssuer = %v, want %v", c.id, got, c.want)
		}
	}
}
