package secretbox

import (
	"errors"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := New("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	url := "telegram://123:ABC@telegram?chats=@ops"
	sealed, err := b.Seal(url)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "telegram") {
		t.Fatalf("plaintext visible: %s", sealed)
	}
	again, _ := b.Seal(url)
	if again == sealed {
		t.Fatal("same nonce twice")
	}
	got, err := b.Open(sealed)
	if err != nil || got != url {
		t.Fatalf("open = %q, %v", got, err)
	}
}

func TestOpenWithAnotherSecretFails(t *testing.T) {
	a, _ := New("one")
	b, _ := New("two")
	sealed, _ := a.Seal("ntfy://ntfy.sh/topic")
	if _, err := b.Open(sealed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []string{"", "not base64!", "QUJD"} {
		if _, err := a.Open(bad); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestNewRejectsEmptySecret(t *testing.T) {
	if _, err := New(""); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("err = %v", err)
	}
}
