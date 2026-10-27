package authserver

import (
	"strings"
	"testing"
)

func TestVerifyPKCE(t *testing.T) {
	if !VerifyPKCE(verifier, challenge) {
		t.Fatal("RFC 7636 vector rejected")
	}
	if VerifyPKCE("wrong", challenge) {
		t.Fatal("wrong verifier accepted")
	}
	if VerifyPKCE("", "") {
		t.Fatal("empty accepted")
	}
}

func TestSecretHash(t *testing.T) {
	raw, hash, err := newSecret(refreshPrefix)
	if err != nil || !strings.HasPrefix(raw, "wfr_") || len(raw) != 4+43 || hash != hashSecret(raw) || len(hash) != 64 {
		t.Fatalf("%q %q %v", raw, hash, err)
	}
	raw2, _, _ := newSecret(refreshPrefix)
	if raw == raw2 {
		t.Fatal("secrets must differ")
	}
}
