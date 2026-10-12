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
