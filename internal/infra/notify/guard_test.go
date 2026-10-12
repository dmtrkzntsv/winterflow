package notify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrivate(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.10", "169.254.169.254",
		"100.64.0.1", "0.0.0.0", "::1", "fd00::1", "fe80::1", "224.0.0.1"} {
		if !Private(net.ParseIP(s)) {
			t.Errorf("%s not private", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "149.154.167.220", "2606:4700:4700::1111", "100.128.0.1"} {
		if Private(net.ParseIP(s)) {
			t.Errorf("%s private", s)
		}
	}
}

// With private targets forbidden, an HTTP service never connects to a
// loopback server, and a non-HTTP one is refused before dialing.
func TestGuardRefusesPrivateTargets(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	guarded := NewShoutrrr(2*time.Second, false)
	ctx := context.Background()

	raw := "generic://" + strings.TrimPrefix(srv.URL, "http://") + "/hook?disabletls=yes"
	if err := guarded.Send(ctx, raw, "T", "B"); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("generic to loopback: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("the guarded client reached a loopback server")
	}
	if err := guarded.Send(ctx, "smtp://u:p@127.0.0.1:25/?fromaddress=a@b.c&toaddresses=d@e.f", "T", "B"); err == nil ||
		!strings.Contains(err.Error(), "private") {
		t.Fatalf("smtp to loopback: %v", err)
	}
	if err := checkHost(ctx, "smtp://u:p@10.0.0.5:25/"); !errors.Is(err, ErrPrivateTarget) {
		t.Fatalf("smtp to RFC 1918: %v", err)
	}
	if err := checkHost(ctx, "smtp://u:p@203.0.113.7:25/"); err != nil {
		t.Fatalf("public smtp refused: %v", err)
	}
	// HTTP services are checked at dial time, not by their URL host
	// (telegram://token@telegram has no real host).
	if err := checkHost(ctx, "telegram://1:a@telegram?chats=1"); err != nil {
		t.Fatalf("telegram refused: %v", err)
	}
	// Allowed (standalone): the same loopback send goes through.
	if err := NewShoutrrr(2*time.Second, true).Send(ctx, raw, "T", "B"); err != nil || hits.Load() != 1 {
		t.Fatalf("allowed send: err=%v hits=%d", err, hits.Load())
	}
}

func TestPrivateReservedAndEmbeddedRanges(t *testing.T) {
	cases := map[string]bool{
		"0.1.2.3":              true, // 0.0.0.0/8
		"192.0.0.8":            true, // 192.0.0.0/24
		"198.18.0.1":           true, // 198.18.0.0/15
		"198.19.255.254":       true, // 198.18.0.0/15
		"240.0.0.1":            true, // 240.0.0.0/4
		"255.255.255.255":      true, // broadcast
		"64:ff9b::a00:1":       true, // NAT64 of 10.0.0.1
		"64:ff9b::7f00:1":      true, // NAT64 of 127.0.0.1
		"64:ff9b:1::a9fe:a9fe": true, // local-use NAT64 of 169.254.169.254
		"2002:c0a8:101::1":     true, // 6to4 of 192.168.1.1
		"2002:a00:1::":         true, // 6to4 of 10.0.0.1
		"1.1.1.1":              false,
		"198.20.0.1":           false,
		"192.0.1.1":            false,
		"64:ff9b::808:808":     false, // NAT64 of 8.8.8.8
		"2002:808:808::1":      false, // 6to4 of 8.8.8.8
		"2606:4700:4700::1111": false,
	}
	for s, want := range cases {
		if got := Private(net.ParseIP(s)); got != want {
			t.Errorf("Private(%s) = %v, want %v", s, got, want)
		}
	}
}

// Services that send through the injected client are guarded at dial
// time; resolving their "host" (a secret key) would fail and leak it.
func TestCheckHostSkipsHTTPServices(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{
		"ifttt://SECRETWEBHOOKKEY/?events=ev",
		"pushbullet://SECRETACCESSTOKEN/dev",
		"notifiarr://SECRETAPIKEY123",
		"signalgrid://SECRETCLIENTKEY/phone",
	} {
		if err := checkHost(ctx, raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestCheckHostErrorOmitsHost(t *testing.T) {
	err := checkHost(context.Background(), "smtp://u:p@secret-host-xyz.invalid:25/")
	if err == nil {
		t.Fatal("an unresolvable host was accepted")
	}
	if strings.Contains(err.Error(), "secret-host-xyz") {
		t.Fatalf("host leaked: %v", err)
	}
}
