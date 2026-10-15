package xwinterflow

import (
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func TestCheckValues(t *testing.T) {
	s := parse(t, ModeApp, notes)
	ok := map[string]string{"BASE_URL": "https://notes.example.com", "LANGUAGE": "de"}
	secrets := map[string]bool{"SESSION_SECRET": true}
	if err := s.CheckValues(ok, secrets); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		plain  map[string]string
		secret map[string]bool
		want   string
	}{
		{"required missing", map[string]string{}, secrets, "variable BASE_URL: a value is required"},
		{"required empty", map[string]string{"BASE_URL": ""}, secrets, "variable BASE_URL: a value is required"},
		{"required secret missing", ok, map[string]bool{}, "variable SESSION_SECRET: a value is required"},
		{"pattern", map[string]string{"BASE_URL": "notes.example.com"}, secrets, `variable BASE_URL: "notes.example.com" does not match ^https?://.+`},
		{"dollar", map[string]string{"BASE_URL": "https://x/$HOME"}, secrets, "variable BASE_URL: a plain value can't contain $"},
		{"secret sent plain", map[string]string{"BASE_URL": "https://x", "SESSION_SECRET": "s"}, map[string]bool{}, "variable SESSION_SECRET: it is a secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.CheckValues(tc.plain, tc.secret)
			if !errors.Is(err, ErrValue) || !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	// A declared plain variable given as a secret is checked after decryption.
	if err := s.CheckValues(map[string]string{"BASE_URL": "https://x"}, map[string]bool{"SESSION_SECRET": true, "LANGUAGE": true}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSecretValues(t *testing.T) {
	s := parse(t, ModeApp, "services:\n  app:\n    image: a\n    environment: { PIN: x, TOKEN: y }\nx-winterflow:\n  variables:\n    PIN: { encrypted: true, pattern: '[0-9]{4}' }\n    TOKEN: { encrypted: true, required: true }\n")
	if err := s.CheckSecretValues(map[string]string{"PIN": "1234", "TOKEN": "t$k"}); err != nil {
		t.Fatal(err)
	}
	err := s.CheckSecretValues(map[string]string{"PIN": "12a4", "TOKEN": "t"})
	if !errors.Is(err, ErrValue) || !strings.Contains(err.Error(), "variable PIN: the secret value does not match [0-9]{4}") || strings.Contains(err.Error(), "12a4") {
		t.Fatalf("err = %v", err)
	}
	if err := s.CheckSecretValues(map[string]string{"TOKEN": ""}); !errors.Is(err, ErrValue) || !strings.Contains(err.Error(), "TOKEN: a value is required") {
		t.Fatalf("err = %v", err)
	}
}

// A draft may leave a required variable empty; every other rule holds.
func TestCheckDraftValues(t *testing.T) {
	s := parse(t, ModeApp, notes)
	if err := s.CheckDraftValues(map[string]string{}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	for want, plain := range map[string]map[string]string{
		`variable BASE_URL: "notes" does not match`:        {"BASE_URL": "notes"},
		"variable BASE_URL: a plain value can't contain $": {"BASE_URL": "https://x/$y"},
		"variable SESSION_SECRET: it is a secret":          {"SESSION_SECRET": "s"},
	} {
		if err := s.CheckDraftValues(plain, map[string]bool{}); !errors.Is(err, ErrValue) || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q", err, want)
		}
	}
}
