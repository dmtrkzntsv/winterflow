package auth

import (
	"context"
	"testing"

	"winterflow/internal/domain/model"
)

type fakeStore struct {
	users        int
	bootstrapped string
	valid        map[string]string // email -> password
}

func (f *fakeStore) CountUsers(context.Context) (int, error) { return f.users, nil }

func (f *fakeStore) BootstrapLocalAdmin(_ context.Context, email, password string) (model.User, error) {
	if f.users > 0 {
		return model.User{}, model.ErrNotBootstrap
	}
	f.users = 1
	f.bootstrapped = email
	f.valid = map[string]string{email: password}
	return model.User{ID: "admin-1"}, nil
}

func (f *fakeStore) VerifyLocalCredentials(_ context.Context, email, password string) (model.User, error) {
	if f.valid[email] == password && password != "" {
		return model.User{ID: "u-" + email}, nil
	}
	return model.User{}, model.ErrInvalidCredentials
}

func TestCheckerBootstrapsOnEmpty(t *testing.T) {
	f := &fakeStore{users: 0}
	check := localCredChecker(f)

	ok, err := check("  Admin@Example.COM ", "SuperSecret1")
	if err != nil || !ok {
		t.Fatalf("bootstrap login = %v, %v", ok, err)
	}
	if f.bootstrapped != "admin@example.com" {
		t.Errorf("bootstrapped email = %q, want normalized", f.bootstrapped)
	}

	// Subsequent logins verify instead of bootstrapping.
	if ok, _ := check("admin@example.com", "SuperSecret1"); !ok {
		t.Error("second login with same creds failed")
	}
	if ok, _ := check("admin@example.com", "wrong"); ok {
		t.Error("wrong password accepted")
	}
}

func TestCheckerVerifiesWhenNotEmpty(t *testing.T) {
	f := &fakeStore{users: 2, valid: map[string]string{"a@b.io": "pw12345678"}}
	check := localCredChecker(f)

	if ok, _ := check("A@B.io", "pw12345678"); !ok {
		t.Error("valid credentials rejected")
	}
	if ok, _ := check("newcomer@b.io", "anything"); ok {
		t.Error("unknown user accepted on non-empty instance")
	}
	if f.bootstrapped != "" {
		t.Error("bootstrap ran on a non-empty instance")
	}
}

func TestCheckerRejectsBlank(t *testing.T) {
	f := &fakeStore{users: 0}
	check := localCredChecker(f)
	for _, pair := range [][2]string{{"", "pw"}, {"a@b.io", ""}, {"", ""}} {
		if ok, _ := check(pair[0], pair[1]); ok {
			t.Errorf("blank credentials %q/%q accepted", pair[0], pair[1])
		}
	}
	if f.bootstrapped != "" {
		t.Error("bootstrap ran with blank credentials")
	}
}
