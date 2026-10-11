package model

import (
	"errors"
	"fmt"
	"testing"
)

// Every user-fixable sentinel matches ErrRejected, wrapped or not, and keeps
// its own identity and message.
func TestRejectedSentinels(t *testing.T) {
	for _, sentinel := range []error{ErrIngressInvalid, ErrDomainTaken, ErrDomainNotCovered, ErrStorageRef, ErrReservedVariable} {
		wrapped := fmt.Errorf("%w: detail", sentinel)
		if !errors.Is(wrapped, ErrRejected) || !errors.Is(wrapped, sentinel) {
			t.Errorf("%v: not matched", sentinel)
		}
	}
	if !errors.Is(NotCoveredError("a.example.com"), ErrRejected) {
		t.Error("an uncovered host is not rejected")
	}
	if ErrDomainTaken.Error() != "domain already in use" || errors.Is(ErrDomainTaken, ErrIngressInvalid) {
		t.Errorf("sentinel identity: %v", ErrDomainTaken)
	}
	if errors.Is(errors.New("db down"), ErrRejected) {
		t.Error("an internal error is rejected")
	}
	other := Rejected("invalid backup")
	if !errors.Is(fmt.Errorf("%w: x", other), ErrRejected) || other.Error() != "invalid backup" || errors.Is(other, ErrStorageRef) {
		t.Errorf("Rejected = %v", other)
	}
}
