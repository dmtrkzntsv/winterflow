package pgsql

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestParseConfigBoundsConnect(t *testing.T) {
	for dsn, want := range map[string]time.Duration{
		"postgres://u:p@db:5432/wf":                                   defaultConnectTimeout,
		"postgres://u:p@db:5432/wf?connect_timeout=0":                 defaultConnectTimeout, // 0 ("wait forever") is no deadline
		"postgres://u:p@db:5432/wf?connect_timeout=12":                12 * time.Second,
		"postgres://u:p@db:5432/wf?sslmode=disable&connect_timeout=1": time.Second,
	} {
		cfg, err := parseConfig(dsn)
		if err != nil {
			t.Fatalf("%s: %v", dsn, err)
		}
		if cfg.ConnectTimeout != want {
			t.Errorf("%s: ConnectTimeout = %v, want %v", dsn, cfg.ConnectTimeout, want)
		}
		if cfg.DialFunc == nil {
			t.Errorf("%s: no dial func", dsn)
		}
		if cfg.DefaultQueryExecMode != pgx.QueryExecModeSimpleProtocol {
			t.Errorf("%s: exec mode = %v", dsn, cfg.DefaultQueryExecMode)
		}
		if cfg.RuntimeParams["timezone"] != "UTC" {
			t.Errorf("%s: timezone = %q", dsn, cfg.RuntimeParams["timezone"])
		}
	}
}

// silentServer accepts TCP connections and never answers: a Postgres that
// hangs (or a peer behind a blackhole) as far as the client can tell.
func silentServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

// A pool connection opened on a context without a deadline (many callers
// pass context.Background()) still gives up on a silent server after the
// default connect timeout instead of hanging.
func TestSilentServerFailsWithinConnectTimeout(t *testing.T) {
	t.Parallel()
	cfg, err := parseConfig("postgres://u:p@" + silentServer(t) + "/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- db.PingContext(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ping against a silent server succeeded")
		}
		if took := time.Since(start); took < defaultConnectTimeout-time.Second || took > defaultConnectTimeout+4*time.Second {
			t.Fatalf("failed after %v, want about %v: %v", took, defaultConnectTimeout, err)
		}
	case <-time.After(defaultConnectTimeout + 10*time.Second):
		t.Fatal("ping against a silent server hung past the connect timeout")
	}
}
