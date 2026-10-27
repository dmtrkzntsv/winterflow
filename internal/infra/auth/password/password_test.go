package password

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := Hash("s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected PHC prefix: %s", h)
	}
	ok, rehash, err := Verify(h, "s3cret-password")
	if err != nil || !ok || rehash {
		t.Errorf("Verify = %v, %v, %v", ok, rehash, err)
	}
	h2, _ := Hash("s3cret-password")
	if h == h2 {
		t.Error("salt is not random")
	}
}

func TestVerifyBcryptNeedsRehash(t *testing.T) {
	b, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	ok, rehash, err := Verify(string(b), "pw")
	if err != nil || !ok || !rehash {
		t.Errorf("Verify = %v, %v, %v", ok, rehash, err)
	}
	if ok, rehash, _ := Verify(string(b), "other"); ok || rehash {
		t.Error("wrong bcrypt password accepted")
	}
}

func TestVerifyWrong(t *testing.T) {
	h, _ := Hash("pw")
	ok, rehash, err := Verify(h, "pwX")
	if ok || rehash || err != nil {
		t.Errorf("Verify = %v, %v, %v", ok, rehash, err)
	}
}

func TestVerifyWeakerArgonNeedsRehash(t *testing.T) {
	h := encode("pw", []byte("0123456789abcdef"), 8, 1, 1, 16)
	ok, rehash, err := Verify(h, "pw")
	if err != nil || !ok || !rehash {
		t.Errorf("Verify = %v, %v, %v", ok, rehash, err)
	}
}

func TestVerifyFailsClosed(t *testing.T) {
	valid := encode("pw", []byte("0123456789abcdef"), 8, 1, 1, 16)
	for name, stored := range map[string]string{
		"empty":           "",
		"unknown scheme":  "abcdef",
		"wrong version":   strings.Replace(valid, "v=19", "v=18", 1),
		"bad params":      "$argon2id$v=19$m=x,t=y,p=z$c2FsdA$aGFzaA",
		"bad salt b64":    "$argon2id$v=19$m=8,t=1,p=1$!!!$aGFzaA",
		"empty key":       "$argon2id$v=19$m=8,t=1,p=1$c2FsdA$",
		"missing section": "$argon2id$v=19$m=8,t=1,p=1$c2FsdA",
		"m over cap":      "$argon2id$v=19$m=4194305,t=1,p=1$c2FsdA$aGFzaA",
		"t over cap":      "$argon2id$v=19$m=8,t=65,p=1$c2FsdA$aGFzaA",
		"p over cap":      "$argon2id$v=19$m=8,t=1,p=17$c2FsdA$aGFzaA",
		"bad bcrypt":      "$2a$10$short",
	} {
		if ok, rehash, err := Verify(stored, "pw"); ok || rehash || err != nil {
			t.Errorf("%s: Verify = %v, %v, %v", name, ok, rehash, err)
		}
	}
}
