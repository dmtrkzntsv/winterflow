package hubusers

import (
	"encoding/base64"
	"errors"
	"slices"
	"testing"
)

func TestHubACLRules(t *testing.T) {
	got := HubACLRules("hub-01.winterflow.dev")
	want := []string{
		"resetkeys", "resetchannels",
		"~hub:status:hub-01.winterflow.dev",
		"~hub:draining:hub-01.winterflow.dev",
		"%R~hub:status:*",
		"&bus:requests:hub-01.winterflow.dev",
		"&hub:responses:hub-01.winterflow.dev",
		"&bus:responses", "&bus:events", "&hub:requests", "&hub:broadcast",
		"-@all",
		"+ping", "+hello", "+auth", "+client|setinfo", "+client|setname",
		"+get", "+set", "+del", "+exists", "+scan", "+mget",
		"+publish", "+subscribe", "+unsubscribe",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rules:\n got %q\nwant %q", got, want)
	}
}

func TestUsername(t *testing.T) {
	if got := Username("hub-01.winterflow.dev"); got != "hub:hub-01.winterflow.dev" {
		t.Fatalf("Username = %q", got)
	}
}

func TestValidHubID(t *testing.T) {
	for _, id := range []string{"hub-01.winterflow.dev", "a", "10.0.0.1", "x-y.z"} {
		if err := validHubID(id); err != nil {
			t.Errorf("validHubID(%q) = %v, want nil", id, err)
		}
	}
	for _, id := range []string{
		"", "Hub-01", "hub 01", "hub*", "hub?", "hub[1]", "hub:1", "hub_1",
		"hub/1", "hub\n", ">pw", "~x", "&x", "hub-01.winterflow.dev\x00",
		"héb",
	} {
		if err := validHubID(id); !errors.Is(err, ErrInvalidHubID) {
			t.Errorf("validHubID(%q) = %v, want ErrInvalidHubID", id, err)
		}
	}
}

func TestNewPassword(t *testing.T) {
	a, err := newPassword()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil || len(raw) != 32 {
		t.Fatalf("password %q: %d bytes, %v", a, len(raw), err)
	}
	b, _ := newPassword()
	if a == b {
		t.Fatal("two passwords are equal")
	}
}

func TestIsNoACLFile(t *testing.T) {
	msg := "ERR This Redis instance is not configured to use an ACL file. You may want to specify users via the ACL SETUSER command and then issue a CONFIG REWRITE (assuming you have a Redis configuration file set) in order to store users in the Redis configuration."
	if !isNoACLFile(errors.New(msg)) {
		t.Fatal("not recognized")
	}
	if isNoACLFile(errors.New("ERR something else")) || isNoACLFile(nil) {
		t.Fatal("false positive")
	}
}

func TestIsNoSuchUser(t *testing.T) {
	if !isNoSuchUser(errors.New("ERR No such user 'hub:x'")) {
		t.Fatal("not recognized")
	}
	if isNoSuchUser(errors.New("ERR syntax error")) || isNoSuchUser(nil) {
		t.Fatal("false positive")
	}
}

func TestEnsureRefusesBadID(t *testing.T) {
	u := New(nil)
	if _, _, err := u.Ensure(t.Context(), "Bad*ID"); !errors.Is(err, ErrInvalidHubID) {
		t.Fatalf("Ensure = %v", err)
	}
	if err := u.Delete(t.Context(), "bad id"); !errors.Is(err, ErrInvalidHubID) {
		t.Fatalf("Delete = %v", err)
	}
}
