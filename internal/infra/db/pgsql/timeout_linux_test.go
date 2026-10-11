package pgsql

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The dialer pgx uses turns on TCP keepalive and TCP_USER_TIMEOUT, which
// bound a silent peer on an established connection without touching the
// read/write deadlines pgx uses for context cancellation.
func TestDialSetsKeepAliveAndUserTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg, err := parseConfig("postgres://u:p@" + ln.Addr().String() + "/db")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := cfg.DialFunc(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	raw, err := c.(*net.TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	opts := map[string][2]int{
		"SO_KEEPALIVE":     {unix.SOL_SOCKET, unix.SO_KEEPALIVE},
		"TCP_KEEPIDLE":     {unix.IPPROTO_TCP, unix.TCP_KEEPIDLE},
		"TCP_USER_TIMEOUT": {unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT},
	}
	want := map[string]int{
		"SO_KEEPALIVE":     1,
		"TCP_KEEPIDLE":     int(tcpKeepAlive / time.Second),
		"TCP_USER_TIMEOUT": int(tcpUserTimeout / time.Millisecond),
	}
	for name, o := range opts {
		var got int
		var gerr error
		if err := raw.Control(func(fd uintptr) { got, gerr = unix.GetsockoptInt(int(fd), o[0], o[1]) }); err != nil {
			t.Fatal(err)
		}
		if gerr != nil || got != want[name] {
			t.Errorf("%s = %d, %v; want %d", name, got, gerr, want[name])
		}
	}
}

// A Unix-socket host (host=/var/run/postgresql) dials through the same
// function; the TCP-only socket options must not break it.
func TestDialUnixSocket(t *testing.T) {
	path := t.TempDir() + "/.s.PGSQL.5432"
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg, err := parseConfig("postgres://u:p@/db?host=" + t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := cfg.DialFunc(ctx, "unix", path)
	if err != nil {
		t.Fatalf("unix dial: %v", err)
	}
	_ = c.Close()
}
