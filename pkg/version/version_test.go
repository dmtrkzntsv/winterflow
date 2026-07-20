package version

import "testing"

// withVersion temporarily overrides the package-level version variable.
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestGetVersionDefault(t *testing.T) {
	if got := GetVersion(); got != "0.0.0" {
		t.Fatalf("GetVersion() = %q, want %q (build-time default)", got, "0.0.0")
	}
}

func TestParseNumericVersion(t *testing.T) {
	tests := []struct {
		name   string
		semVer string
		want   int
	}{
		{"plain semver", "1.2.3", 1002003},
		{"zero version", "0.0.0", 0},
		{"v prefix stripped by regex", "v1.2.3", 1002003},
		{"embedded in release name", "winterflow-agent-2.10.7-beta", 2010007},
		{"multi-digit parts", "12.345.678", 12345678},
		{"two-part version has no regex match", "1.2", 1002},
		{"garbage yields zero", "not-a-version", 0},
		{"empty string", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseNumericVersion(tt.semVer); got != tt.want {
				t.Fatalf("ParseNumericVersion(%q) = %d, want %d", tt.semVer, got, tt.want)
			}
		})
	}
}

func TestParseNumericVersionOrdering(t *testing.T) {
	// The numeric encoding must preserve semver ordering for parts < 1000.
	ordered := []string{"0.0.1", "0.0.9", "0.1.0", "0.9.9", "1.0.0", "1.0.10", "1.2.3", "2.0.0", "10.0.0"}
	for i := 1; i < len(ordered); i++ {
		prev, cur := ParseNumericVersion(ordered[i-1]), ParseNumericVersion(ordered[i])
		if prev >= cur {
			t.Fatalf("ordering broken: %q (%d) should be < %q (%d)", ordered[i-1], prev, ordered[i], cur)
		}
	}
}

func TestGetNumericVersion(t *testing.T) {
	withVersion(t, "3.14.15")
	if got := GetNumericVersion(); got != 3014015 {
		t.Fatalf("GetNumericVersion() = %d, want 3014015", got)
	}
}

func TestIsSmallerThan(t *testing.T) {
	withVersion(t, "1.2.3")
	tests := []struct {
		other string
		want  bool
	}{
		{"1.2.4", true},
		{"2.0.0", true},
		{"1.2.3", false},
		{"1.2.2", false},
		{"0.9.9", false},
	}
	for _, tt := range tests {
		if got := IsSmallerThan(tt.other); got != tt.want {
			t.Fatalf("IsSmallerThan(%q) with current 1.2.3 = %v, want %v", tt.other, got, tt.want)
		}
	}
}
