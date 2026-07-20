package util

import (
	"regexp"
	"strings"
	"testing"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestGenerateID(t *testing.T) {
	id := GenerateID()
	if !uuidRe.MatchString(id) {
		t.Fatalf("GenerateID() = %q, not a canonical lowercase UUID", id)
	}
	// Version nibble (first char of third group) must be 7 (UUIDv7).
	if id[14] != '7' {
		t.Fatalf("GenerateID() = %q, version nibble = %c, want 7", id, id[14])
	}
	// Variant nibble (first char of fourth group) must be RFC 4122 (8, 9, a, b).
	if v := id[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Fatalf("GenerateID() = %q, variant nibble = %c, want one of 8/9/a/b", id, v)
	}
}

func TestGenerateIDUniqueAndSortable(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	prev := ""
	for i := 0; i < n; i++ {
		id := GenerateID()
		if _, dup := seen[id]; dup {
			t.Fatalf("GenerateID() produced duplicate %q", id)
		}
		seen[id] = struct{}{}
		// UUIDv7 is time-ordered: ids generated later must never sort before
		// earlier ones (equal prefixes are broken by random bits, so >=).
		if prev != "" && id < prev {
			t.Fatalf("GenerateID() not monotonically sortable: %q came after %q", id, prev)
		}
		prev = id
	}
}

func TestGenerateRandomCode(t *testing.T) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for _, length := range []int{0, 1, 6, 32} {
		code := GenerateRandomCode(length)
		if len(code) != length {
			t.Fatalf("GenerateRandomCode(%d) = %q, len = %d", length, code, len(code))
		}
		for _, r := range code {
			if !strings.ContainsRune(charset, r) {
				t.Fatalf("GenerateRandomCode(%d) = %q, contains %q outside charset", length, code, r)
			}
		}
	}
}

func TestGenerateRandomCodeVaries(t *testing.T) {
	// 20 chars over a 36-symbol alphabet: a collision means the RNG is broken.
	if a, b := GenerateRandomCode(20), GenerateRandomCode(20); a == b {
		t.Fatalf("two GenerateRandomCode(20) calls returned the same value %q", a)
	}
}

func TestGenerateSecureToken(t *testing.T) {
	tests := []struct {
		length  int
		wantLen int // hex of length/2 bytes => 2*(length/2) chars
	}{
		{0, 0},
		{2, 2},
		{8, 8},
		{7, 6}, // odd lengths round down to the nearest even count
		{64, 64},
	}
	for _, tt := range tests {
		tok, err := GenerateSecureToken(tt.length)
		if err != nil {
			t.Fatalf("GenerateSecureToken(%d) error: %v", tt.length, err)
		}
		if len(tok) != tt.wantLen {
			t.Fatalf("GenerateSecureToken(%d) = %q, len = %d, want %d", tt.length, tok, len(tok), tt.wantLen)
		}
		if !regexp.MustCompile(`^[0-9a-f]*$`).MatchString(tok) {
			t.Fatalf("GenerateSecureToken(%d) = %q, want lowercase hex", tt.length, tok)
		}
	}
}

func TestGenerateSecureTokenVaries(t *testing.T) {
	a, err1 := GenerateSecureToken(32)
	b, err2 := GenerateSecureToken(32)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if a == b {
		t.Fatalf("two GenerateSecureToken(32) calls returned the same value %q", a)
	}
}

func TestRefString(t *testing.T) {
	if got := RefString(""); got != nil {
		t.Fatalf("RefString(\"\") = %q, want nil", *got)
	}
	got := RefString("hello")
	if got == nil || *got != "hello" {
		t.Fatalf("RefString(\"hello\") = %v, want pointer to \"hello\"", got)
	}
}

func TestDerefString(t *testing.T) {
	if got := DerefString(nil); got != "" {
		t.Fatalf("DerefString(nil) = %q, want \"\"", got)
	}
	s := "world"
	if got := DerefString(&s); got != "world" {
		t.Fatalf("DerefString(&%q) = %q", s, got)
	}
}

func TestRefDerefRoundTrip(t *testing.T) {
	for _, s := range []string{"", "a", "with spaces", "ünïcödé"} {
		if got := DerefString(RefString(s)); got != s {
			t.Fatalf("DerefString(RefString(%q)) = %q", s, got)
		}
	}
}
