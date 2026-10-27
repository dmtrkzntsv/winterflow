package oauthlogin

import (
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func TestSafeReturnTo(t *testing.T) {
	cases := map[string]string{
		"":                                     "/",
		"/":                                    "/",
		"/apps":                                "/apps",
		"/settings?tab=accounts":               "/settings?tab=accounts",
		"/a#frag":                              "/a#frag",
		"https://evil.example/":                "/",
		"//evil.example/x":                     "/",
		"/\\evil.example":                      "/",
		"/a\\b":                                "/",
		"javascript:alert(1)":                  "/",
		"apps":                                 "/",
		"/a\nb":                                "/",
		"/a\x7fb":                              "/",
		"/" + strings.Repeat("a", maxReturnTo): "/",
	}
	for in, want := range cases {
		if got := SafeReturnTo(in); got != want {
			t.Errorf("SafeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithError(t *testing.T) {
	cases := map[string]string{
		"/login":            "/login?error=denied",
		"/settings?tab=x":   "/settings?error=denied&tab=x",
		"/settings?error=y": "/settings?error=denied",
	}
	for in, want := range cases {
		if got := withError(in, "denied"); got != want {
			t.Errorf("withError(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoginErrorURL(t *testing.T) {
	cases := map[string]string{
		"":                 "/login?error=denied",
		"/":                "/login?error=denied",
		"//evil.example/x": "/login?error=denied",
		"/apps":            "/login?error=denied&next=%2Fapps",
		"/oauth/authorize?client_id=c&state=a%26b": "/login?error=denied&next=%2Foauth%2Fauthorize%3Fclient_id%3Dc%26state%3Da%2526b",
	}
	for in, want := range cases {
		if got := loginErrorURL(in, "denied"); got != want {
			t.Errorf("loginErrorURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrorURLKeepsReturnTo(t *testing.T) {
	login := model.LoginState{Intent: model.IntentLogin, ReturnTo: "/oauth/authorize?x=1"}
	if got := errorURL(login, ErrCodeProvider); got != "/login?error=provider_error&next=%2Foauth%2Fauthorize%3Fx%3D1" {
		t.Errorf("login errorURL = %q", got)
	}
	link := model.LoginState{Intent: model.IntentLink, ReturnTo: "/settings"}
	if got := errorURL(link, ErrCodeProvider); got != "/settings?error=provider_error" {
		t.Errorf("link errorURL = %q", got)
	}
}

func TestDeriveName(t *testing.T) {
	cases := []struct{ claim, email, want string }{
		{"Ada Lovelace", "ada@example.test", "Ada Lovelace"},
		{"  ", "ada@example.test", "ada"},
		{"", "@example.test", "@example.test"},
		{"", "", "User"},
		{strings.Repeat("é", 70), "", strings.Repeat("é", 64)},
	}
	for _, tc := range cases {
		if got := deriveName(tc.claim, tc.email); got != tc.want {
			t.Errorf("deriveName(%q, %q) = %q, want %q", tc.claim, tc.email, got, tc.want)
		}
	}
}

func TestAppleName(t *testing.T) {
	if got := appleName(`{"name":{"firstName":"Ada","lastName":"Lovelace"}}`); got != "Ada Lovelace" {
		t.Fatalf("appleName = %q", got)
	}
	if got := appleName(`not json`); got != "" {
		t.Fatalf("appleName(garbage) = %q", got)
	}
}
