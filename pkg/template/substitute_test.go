package template

import (
	"os"
	"strings"
	"testing"
)

// unsetenv guarantees key is absent from the environment for the duration of
// the test, restoring the original value afterwards (via t.Setenv's cleanup).
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	_ = os.Unsetenv(key)
}

func TestSubstitute(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		vars    map[string]string
		want    string
		wantErr string // substring the error must contain; empty means no error
	}{
		{
			name:  "no variables passes through",
			input: "plain text, no substitution",
			want:  "plain text, no substitution",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "simple substitution from vars map",
			input: "image: nginx:${TAG}",
			vars:  map[string]string{"TAG": "1.27"},
			want:  "image: nginx:1.27",
		},
		{
			name:  "unset simple variable becomes empty string",
			input: "value=[${WF_TEST_UNSET_VAR}]",
			want:  "value=[]",
		},
		{
			name:  "multiple variables with surrounding text",
			input: "${A}-${B}-${A}",
			vars:  map[string]string{"A": "x", "B": "y"},
			want:  "x-y-x",
		},
		{
			name:  "dollar without braces is left untouched",
			input: "cost is $VAR and $5",
			vars:  map[string]string{"VAR": "nope"},
			want:  "cost is $VAR and $5",
		},
		{
			name:  "colon-dash default used when unset",
			input: "${WF_TEST_UNSET_VAR:-fallback}",
			want:  "fallback",
		},
		{
			name:  "colon-dash default used when set but empty",
			input: "${EMPTY:-fallback}",
			vars:  map[string]string{"EMPTY": ""},
			want:  "fallback",
		},
		{
			name:  "colon-dash default ignored when set",
			input: "${VAR:-fallback}",
			vars:  map[string]string{"VAR": "real"},
			want:  "real",
		},
		{
			name:  "dash default used when unset",
			input: "${WF_TEST_UNSET_VAR-fallback}",
			want:  "fallback",
		},
		{
			name:  "dash default NOT used when set to empty",
			input: "[${EMPTY-fallback}]",
			vars:  map[string]string{"EMPTY": ""},
			want:  "[]",
		},
		{
			name:  "dash default ignored when set",
			input: "${VAR-fallback}",
			vars:  map[string]string{"VAR": "real"},
			want:  "real",
		},
		{
			name:    "colon-question errors when unset",
			input:   "${WF_TEST_UNSET_VAR:?var is required}",
			wantErr: "var is required",
		},
		{
			name:    "colon-question errors when set but empty",
			input:   "${EMPTY:?must not be empty}",
			vars:    map[string]string{"EMPTY": ""},
			wantErr: "must not be empty",
		},
		{
			name:  "colon-question passes when set",
			input: "${VAR:?unused}",
			vars:  map[string]string{"VAR": "ok"},
			want:  "ok",
		},
		{
			name:    "question errors when unset",
			input:   "${WF_TEST_UNSET_VAR?missing}",
			wantErr: "missing",
		},
		{
			name:  "question does NOT error when set to empty",
			input: "[${EMPTY?unused}]",
			vars:  map[string]string{"EMPTY": ""},
			want:  "[]",
		},
		{
			name:    "error aborts whole substitution",
			input:   "${OK} then ${WF_TEST_UNSET_VAR:?boom}",
			vars:    map[string]string{"OK": "fine"},
			wantErr: "boom",
		},
		{
			name:  "default value may contain special characters",
			input: "${WF_TEST_UNSET_VAR:-http://localhost:8080/path}",
			want:  "http://localhost:8080/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unsetenv(t, "WF_TEST_UNSET_VAR")
			got, err := Substitute(tt.input, tt.vars)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Substitute(%q) = %q, want error containing %q", tt.input, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Substitute(%q) error = %q, want it to contain %q", tt.input, err, tt.wantErr)
				}
				if got != "" {
					t.Fatalf("Substitute(%q) returned %q alongside an error, want empty", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Substitute(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("Substitute(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSubstituteEnvFallback(t *testing.T) {
	t.Setenv("WF_TEST_ENV_VAR", "from-env")

	got, err := Substitute("v=${WF_TEST_ENV_VAR}", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v=from-env" {
		t.Fatalf("env fallback: got %q, want %q", got, "v=from-env")
	}

	// The vars map takes precedence over the environment.
	got, err = Substitute("v=${WF_TEST_ENV_VAR}", map[string]string{"WF_TEST_ENV_VAR": "from-map"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v=from-map" {
		t.Fatalf("map precedence: got %q, want %q", got, "v=from-map")
	}

	// A variable set-but-empty in the environment counts as set for ${VAR-def}.
	t.Setenv("WF_TEST_ENV_VAR", "")
	got, err = Substitute("[${WF_TEST_ENV_VAR-def}]", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "[]" {
		t.Fatalf("set-but-empty env var: got %q, want %q", got, "[]")
	}
}

// The implementation has no $$-escape handling (unlike the full Compose spec):
// "$${VAR}" still substitutes the inner ${VAR} and keeps the leading dollar.
// This test documents the current behavior so a future change is deliberate.
func TestSubstituteNoDollarEscape(t *testing.T) {
	got, err := Substitute("$${VAR}", map[string]string{"VAR": "v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "$v" {
		t.Fatalf("Substitute($${VAR}) = %q, want %q (no escape support)", got, "$v")
	}
}
