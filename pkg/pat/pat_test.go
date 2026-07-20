package pat

import (
	"regexp"
	"strings"
	"testing"
)

func TestGenerateFormat(t *testing.T) {
	plaintext, hash, prefix, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plaintext, "wfp_") {
		t.Errorf("plaintext %q lacks wfp_ prefix", plaintext)
	}
	if len(plaintext) != len("wfp_")+40 {
		t.Errorf("len = %d, want %d", len(plaintext), len("wfp_")+40)
	}
	if !regexp.MustCompile(`^wfp_[0-9A-Za-z]{40}$`).MatchString(plaintext) {
		t.Errorf("plaintext %q not base62", plaintext)
	}
	if prefix != plaintext[:PrefixLen] {
		t.Errorf("prefix %q != first %d chars of %q", prefix, PrefixLen, plaintext)
	}
	if hash != Hash(plaintext) {
		t.Error("returned hash does not match Hash(plaintext)")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Errorf("hash %q not hex sha256", hash)
	}
}

func TestGenerateUnique(t *testing.T) {
	a, _, _, _ := Generate()
	b, _, _, _ := Generate()
	if a == b {
		t.Error("two generated tokens are equal")
	}
}

func TestHashStable(t *testing.T) {
	if Hash("wfp_x") != Hash("wfp_x") {
		t.Error("Hash not deterministic")
	}
	if Hash("a") == Hash("b") {
		t.Error("distinct inputs collide")
	}
}
