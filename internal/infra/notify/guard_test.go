package notify

import (
	"context"
	"github.com/nicholas-fedor/shoutrrr/pkg/router"
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
// connCounter is a TCP listener that counts the connections it accepts and
// answers each with a minimal HTTP 200, so every service gets as far as it
// can.
func connCounter(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var n atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}"))
			c.Close()
		}
	}()
	return ln.Addr().String(), &n
}

// loopbackURLs points every service whose destination the URL chooses at
// addr (services with a fixed public host cannot be aimed anywhere).
func loopbackURLs(addr string) map[string]string {
	return map[string]string{
		"bark":       "bark://:devicekey@" + addr + "/?scheme=http",
		"generic":    "generic://" + addr + "/hook?disabletls=yes",
		"gotify":     "gotify://" + addr + "/Aaaaaaaaaaaaaaa?disabletls=yes",
		"matrix":     "matrix://user:pass@" + addr + "/?rooms=!r:x&disableTLS=yes",
		"mattermost": "mattermost://" + addr + "/abcdefghijklmnopqrstuvwxyz?disabletls=yes",
		"ntfy":       "ntfy://" + addr + "/topic?scheme=http",
		"rocketchat": "rocketchat://" + addr + "/tokenA/tokenB",
		"signal":     "signal://" + addr + "/+1234567890/+1234567891?disabletls=yes",
		"opsgenie":   "opsgenie://" + addr + "/apikey",
		"googlechat": "googlechat://" + addr + "/v1/spaces/foo/messages?key=k&token=t",
		"smtp":       "smtp://u:p@" + addr + "/?fromaddress=a@b.c&toaddresses=d@e.f&encryption=None&usestarttls=no",
		"mqtt":       "mqtt://" + addr + "/topic?disabletls=yes",
		"xmpp":       "xmpp://u@x.y:p@" + addr + "/?to=a@b.c",
	}
}

// settle gives a connection the kernel already accepted time to reach
// the counter, so a send that connects and returns at once is counted.
func settle() { time.Sleep(100 * time.Millisecond) }

func TestServiceNeverConnects(t *testing.T) {
	addr, n := connCounter(t)
	for name, raw := range loopbackURLs(addr) {
		_, _ = Service(raw)
		settle()
		if got := n.Load(); got != 0 {
			t.Fatalf("validating a %s URL opened %d connection(s)", name, got)
		}
	}
}

func TestGuardedSendNeverConnectsToPrivate(t *testing.T) {
	addr, n := connCounter(t)
	guarded := NewShoutrrr(2*time.Second, false)
	allowed := NewShoutrrr(2*time.Second, true)
	for name, raw := range loopbackURLs(addr) {
		_ = guarded.Send(context.Background(), raw, "T", "B")
		settle()
		if got := n.Load(); got != 0 {
			t.Fatalf("a guarded %s send reached a loopback listener (%d connection(s))", name, got)
		}
		// The URL really aims at the listener: allowed, it connects.
		_ = allowed.Send(context.Background(), raw, "T", "B")
		settle()
		if n.Load() == 0 {
			t.Fatalf("an allowed %s send never reached the listener: the test URL proves nothing", name)
		}
		n.Store(0)
	}
}

// A redirect from an allowed host to a refused one is refused too, the
// Matrix login included (it runs before Shoutrrr injects a client).
func TestGuardedSendRefusesRedirectsToPrivate(t *testing.T) {
	target, n := connCounter(t)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+target+"/stolen", http.StatusFound)
	}))
	defer redirector.Close()
	// Allow the redirector (a public host in real life), refuse the target.
	refuse := func(address string) bool { return address == target }
	defer refuseHook.Store(refuseHook.Swap(&refuse))

	from := strings.TrimPrefix(redirector.URL, "http://")
	guarded := NewShoutrrr(2*time.Second, false)
	for name, raw := range map[string]string{
		"generic": "generic://" + from + "/hook?disabletls=yes",
		"matrix":  "matrix://user:pass@" + from + "/?rooms=!r:x&disableTLS=yes",
		"ntfy":    "ntfy://" + from + "/topic?scheme=http",
	} {
		_ = guarded.Send(context.Background(), raw, "T", "B")
		settle()
		if got := n.Load(); got != 0 {
			t.Fatalf("a guarded %s send followed a redirect to a refused address (%d connection(s))", name, got)
		}
	}
}

func TestEveryShoutrrrSchemeIsReviewed(t *testing.T) {
	for _, s := range (&router.ServiceRouter{}).ListServices() {
		if _, ok := reviewedSchemes[s]; !ok {
			t.Errorf("Shoutrrr scheme %q is not reviewed: check whether its sender connects while it is created "+
				"(before our client and dialer are injected) and add it to reviewedSchemes", s)
		}
	}
}

func TestServiceValidatesMatrixWithoutConnecting(t *testing.T) {
	if _, err := Service("matrix://user@example.org/?rooms=!r:x"); err == nil {
		t.Fatal("a matrix URL without a password or token was accepted")
	}
	if name, err := Service("matrix://user:pass@example.org/?rooms=!r:x"); err != nil || name != "matrix" {
		t.Fatalf("valid matrix URL: %q, %v", name, err)
	}
	if _, err := Service("matrix+https://user:pass@example.org/"); err == nil {
		t.Fatal("a matrix URL with a transport suffix was accepted")
	}
}
