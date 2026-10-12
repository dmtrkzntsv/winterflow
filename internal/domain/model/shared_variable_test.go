package model

import (
	"strings"
	"testing"
)

func TestValidateSharedVariableName(t *testing.T) {
	for _, ok := range []string{"SMTP_HOST", "_x", "a1", "smtp_pass", "Wf_lower_is_not_reserved"} {
		if err := ValidateSharedVariableName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	bad := map[string]string{
		"":                       "required",
		"1ABC":                   "letters, digits",
		"A-B":                    "letters, digits",
		"A B":                    "letters, digits",
		"COMPOSE_PROJECT_NAME":   "reserved",
		"WF_APP_ID":              "reserved",
		"WF_":                    "reserved",
		strings.Repeat("A", 256): "longer than 255",
	}
	for name, want := range bad {
		err := ValidateSharedVariableName(name)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to mention %q", name, err, want)
		}
	}
	if err := ValidateSharedVariableName(strings.Repeat("A", 255)); err != nil {
		t.Errorf("255 characters rejected: %v", err)
	}
}

func TestSharedVariableScope(t *testing.T) {
	srv := "s1"
	if (SharedVariable{}).Scope() != SharedScopeOrg || (SharedVariable{ServerID: &srv}).Scope() != SharedScopeServer {
		t.Fatal("scope wrong")
	}
}
