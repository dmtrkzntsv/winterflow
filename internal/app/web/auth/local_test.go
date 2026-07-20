package auth

import (
	"context"
	"testing"

	"winterflow/internal/domain/model"
)

type fakeStore struct {
	valid map[string]string // email -> password
}

func (f *fakeStore) VerifyLocalCredentials(_ context.Context, email, password string) (model.User, error) {
	if f.valid[email] == password && password != "" {
		return model.User{ID: "u-" + email}, nil
	}
	return model.User{}, model.ErrInvalidCredentials
}

func TestCheckerVerifies(t *testing.T) {
	f := &fakeStore{valid: map[string]string{"a@b.io": "pw12345678"}}
	check := localCredChecker(f)

	if ok, _ := check("A@B.io", "pw12345678"); !ok {
		t.Error("valid credentials rejected (email normalization)")
	}
	if ok, _ := check("a@b.io", "wrong"); ok {
		t.Error("wrong password accepted")
	}
	if ok, _ := check("newcomer@b.io", "anything"); ok {
		t.Error("unknown user accepted — login must never create accounts")
	}
}

func TestCheckerRejectsBlank(t *testing.T) {
	check := localCredChecker(&fakeStore{})
	for _, pair := range [][2]string{{"", "pw"}, {"a@b.io", ""}, {"", ""}} {
		if ok, _ := check(pair[0], pair[1]); ok {
			t.Errorf("blank credentials %q/%q accepted", pair[0], pair[1])
		}
	}
}
