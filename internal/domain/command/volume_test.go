package command

import (
	"strings"
	"testing"
)

func TestValidateVolumeName(t *testing.T) {
	ok := []string{"data", "a", "app_data-1.v2", "0abc", strings.Repeat("a", 255)}
	for _, n := range ok {
		if err := ValidateVolumeName(n); err != nil {
			t.Errorf("%q should be valid: %v", n, err)
		}
	}
	bad := []string{"", "-rf", "--all", "_x", ".x", "../x", "a/b", "a b", "a;b", "$(x)", strings.Repeat("a", 256)}
	for _, n := range bad {
		if err := ValidateVolumeName(n); err == nil {
			t.Errorf("%q should be rejected", n)
		}
	}
}
