package model

import (
	"errors"
	"testing"
)

func TestManagedByTemplateMatchesBothClasses(t *testing.T) {
	err := ManagedByTemplate("shop")
	if !errors.Is(err, ErrConflict) || !errors.Is(err, ErrManagedByTemplate) {
		t.Fatalf("ManagedByTemplate = %v: want ErrConflict and ErrManagedByTemplate", err)
	}
	if errors.Is(ErrTemplateNotFound, ErrManagedByTemplate) {
		t.Fatal("another classified error matches ErrManagedByTemplate")
	}
}
