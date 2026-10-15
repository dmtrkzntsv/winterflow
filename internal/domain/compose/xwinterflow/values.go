package xwinterflow

import (
	"fmt"
	"strings"

	"winterflow/internal/domain/model"
)

// ErrValue marks a declared variable's value that breaks its rules: a
// required one without a value, a value failing its pattern, a plain value
// with $. It matches model.ErrRejected.
var ErrValue = model.Rejected("invalid variable value")

func valueError(name, format string, args ...any) error {
	return fmt.Errorf("%w: variable %s: %s", ErrValue, name, fmt.Sprintf(format, args...))
}

// CheckValues checks a save's values of the declared variables: plain are
// the plain values by name, secret the names that carry a secret
// (ciphertext to the API). A required variable needs a non-empty plain
// value or a secret; an encrypted one may not be plain; a plain value must
// match the pattern and may not contain $ (compose interpolates .env). A
// secret's pattern is checked once it is decrypted (CheckSecretValues).
func (s Spec) CheckValues(plain map[string]string, secret map[string]bool) error {
	return s.checkValues(plain, secret, true)
}

// CheckDraftValues is CheckValues for a draft: a required variable may
// still be empty (the deploy needs it), every other rule applies.
func (s Spec) CheckDraftValues(plain map[string]string, secret map[string]bool) error {
	return s.checkValues(plain, secret, false)
}

func (s Spec) checkValues(plain map[string]string, secret map[string]bool, required bool) error {
	for _, v := range s.Variables {
		if secret[v.Name] {
			continue
		}
		val := plain[v.Name]
		switch {
		case val == "" && v.Required && required:
			return valueError(v.Name, "a value is required")
		case val == "":
		case v.Encrypted:
			return valueError(v.Name, "it is a secret: save its value as a secret")
		case strings.Contains(val, "$"):
			return valueError(v.Name, "a plain value can't contain $ (make the variable secret to use one)")
		case !matches(v, val):
			return valueError(v.Name, "%q does not match %s", val, v.Pattern)
		}
	}
	return nil
}

// CheckSecretValues checks decrypted secret values (by name) against their
// variables: a required one may not be empty, and each must match its
// pattern. The value never appears in the error.
func (s Spec) CheckSecretValues(secrets map[string]string) error {
	for _, v := range s.Variables {
		val, ok := secrets[v.Name]
		switch {
		case !ok:
		case val == "" && v.Required:
			return valueError(v.Name, "a value is required")
		case val != "" && !matches(v, val):
			return valueError(v.Name, "the secret value does not match %s", v.Pattern)
		}
	}
	return nil
}
