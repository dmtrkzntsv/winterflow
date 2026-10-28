package password

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var ctx = context.Background()

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := Hash(ctx, "s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected PHC prefix: %s", h)
	}
	ok, rehash, err := Verify(ctx, h, "s3cret-password")
	if err != nil || !ok || rehash {
		t.Errorf("Verify = %v, %v, %v", ok, rehash, err)
	}
	h2, _ := Hash(ctx, "s3cret-password")
	if h == h2 {
		t.Error("salt is not random")
	}
}

func TestVerifyBcryptNeedsRehash(t *testing.T) {
	b, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	ok, rehash, err := Verify(ctx, string(b), "pw")
	if err != nil || !ok || !rehash {
		t.Errorf("Verify = %v, %v, %v", ok, rehash, err)
	}
	if ok, rehash, _ := Verify(ctx, string(b), "other"); ok || rehash {
		t.Error("wrong bcrypt password accepted")
	}
}

func TestVerifyWrong(t *testing.T) {
	h, _ := Hash(ctx, "pw")
	ok, rehash, err := Verify(ctx, h, "pwX")
	if ok || rehash || err != nil {
		t.Errorf("Verify = %v, %v, %v", ok, rehash, err)
	}
}

func TestVerifyWeakerArgonNeedsRehash(t *testing.T) {
	h := encode("pw", []byte("0123456789abcdef"), 8, 1, 1, 16)
	ok, rehash, err := Verify(ctx, h, "pw")
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
		if ok, rehash, err := Verify(ctx, stored, "pw"); ok || rehash || err != nil {
			t.Errorf("%s: Verify = %v, %v, %v", name, ok, rehash, err)
		}
	}
}

// fillSlots takes every slot and returns a func giving them back.
func fillSlots(t *testing.T) func() {
	t.Helper()
	for range MaxConcurrent {
		slots <- struct{}{}
	}
	return func() {
		for range MaxConcurrent {
			<-slots
		}
	}
}

func TestConcurrencyCapBlocksBeyondSlots(t *testing.T) {
	if MaxConcurrent < 1 || MaxConcurrent > 4 {
		t.Fatalf("MaxConcurrent = %d, want 1..4", MaxConcurrent)
	}
	h, err := Hash(ctx, "pw")
	if err != nil {
		t.Fatal(err)
	}
	release := fillSlots(t)
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := Hash(short, "pw"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Hash with all slots taken = %v, want DeadlineExceeded", err)
	}
	if _, _, err := Verify(short, h, "pw"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Verify with all slots taken = %v, want DeadlineExceeded", err)
	}
	if err := VerifyDummy(short, "pw"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("VerifyDummy with all slots taken = %v, want DeadlineExceeded", err)
	}

	// A waiting call proceeds as soon as a slot is given back.
	done := make(chan error, 1)
	go func() {
		_, _, err := Verify(ctx, h, "pw")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Verify ran while every slot was taken: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestVerifyDummyFails(t *testing.T) {
	if err := VerifyDummy(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dummyHash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("dummy hash does not use the current parameters: %s", dummyHash)
	}
}
