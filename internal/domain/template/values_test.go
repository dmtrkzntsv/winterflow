package template

import (
	"reflect"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/crypto/cryptotest"
)

func TestResolveMergesOverTheStoredValues(t *testing.T) {
	s := spec(t)
	ct := cryptotest.Ciphertext(t, "s3cret")
	v, err := Resolve(s, Stored{}, Given{
		Values:  map[string]string{"BASE_URL": "https://notes.example.test", "LANGUAGE": ""},
		Secrets: map[string]string{"SESSION_SECRET": ct},
	})
	if err != nil {
		t.Fatal(err)
	}
	// LANGUAGE given empty: the compose file's own "en" stands, nothing is sent.
	if !reflect.DeepEqual(v.Plain, map[string]string{"BASE_URL": "https://notes.example.test"}) ||
		!reflect.DeepEqual(v.Secrets, map[string]string{"SESSION_SECRET": ct}) ||
		!reflect.DeepEqual(v.Stored, Stored{Values: map[string]string{"BASE_URL": "https://notes.example.test"}, SecretNames: []string{"SESSION_SECRET"}}) {
		t.Fatalf("values = %+v", v)
	}

	// An update: the stored secret is kept, a new plain value merges, a
	// given "" clears a stored one.
	next, err := Resolve(s, Stored{Values: map[string]string{"BASE_URL": "https://a.test", "LANGUAGE": "de", "GONE": "x"}, SecretNames: []string{"SESSION_SECRET", "OLD"}},
		Given{Values: map[string]string{"LANGUAGE": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Plain, map[string]string{"BASE_URL": "https://a.test"}) ||
		next.Secrets["SESSION_SECRET"] != command.EncryptedPlaceholder || len(next.Secrets) != 1 ||
		!reflect.DeepEqual(next.Stored.SecretNames, []string{"SESSION_SECRET"}) {
		t.Fatalf("update = %+v", next)
	}
}

func TestResolveRefuses(t *testing.T) {
	s := spec(t)
	ct := cryptotest.Ciphertext(t, "s3cret")
	ok := map[string]string{"BASE_URL": "https://a.test"}
	sec := map[string]string{"SESSION_SECRET": ct}
	cases := map[string]struct {
		stored Stored
		given  Given
		field  string
	}{
		"an undeclared value":            {given: Given{Values: map[string]string{"NOPE": "1"}, Secrets: sec}, field: "values.NOPE"},
		"a secret sent plain":            {given: Given{Values: map[string]string{"SESSION_SECRET": "x"}}, field: "values.SESSION_SECRET: SESSION_SECRET is a secret"},
		"a dollar":                       {given: Given{Values: map[string]string{"BASE_URL": "https://$x"}, Secrets: sec}, field: "values.BASE_URL: may not contain $"},
		"an undeclared secret":           {given: Given{Values: ok, Secrets: map[string]string{"NOPE": ct}}, field: "secrets.NOPE"},
		"a plain value as secret":        {given: Given{Values: ok, Secrets: map[string]string{"LANGUAGE": ct}}, field: "secrets.LANGUAGE: LANGUAGE is not a secret"},
		"the keep placeholder":           {given: Given{Values: ok, Secrets: map[string]string{"SESSION_SECRET": command.EncryptedPlaceholder}}, field: "a ciphertext is required"},
		"a plaintext secret":             {given: Given{Values: ok, Secrets: map[string]string{"SESSION_SECRET": "s3cret"}}, field: "secrets.SESSION_SECRET: not a ciphertext"},
		"a missing required value":       {given: Given{Secrets: sec}, field: "variable BASE_URL: a value is required"},
		"a missing required secret":      {given: Given{Values: ok}, field: "variable SESSION_SECRET: a value is required"},
		"a pattern mismatch":             {given: Given{Values: map[string]string{"BASE_URL": "ftp://a"}, Secrets: sec}, field: "variable BASE_URL"},
		"a stored value off the pattern": {stored: Stored{Values: map[string]string{"BASE_URL": "https://a.test", "LANGUAGE": "deu"}, SecretNames: []string{"SESSION_SECRET"}}, field: "variable LANGUAGE"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(s, c.stored, c.given)
			wantInvalid(t, err, c.field)
		})
	}
}
