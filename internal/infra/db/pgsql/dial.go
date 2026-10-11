package pgsql

import (
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

// pgx sets no socket timeouts of its own (Bun's pgdriver had 5 s dial,
// 10 s read and 5 s write), and many callers pass context.Background(), so
// a dead or blackholed server would otherwise hang a caller for the
// kernel's TCP timeouts (minutes to hours).
const (
	// defaultConnectTimeout bounds the whole connection setup (dial, TLS,
	// startup and authentication) when the DSN sets no connect_timeout.
	defaultConnectTimeout = 5 * time.Second
	// tcpKeepAlive is the idle time before, and the interval between,
	// keepalive probes on an idle connection.
	tcpKeepAlive = 15 * time.Second
	// tcpUserTimeout (Linux) drops a connection whose sent data stays
	// unacknowledged this long, e.g. a query sent to a peer that vanished.
	tcpUserTimeout = 30 * time.Second
)

// bound gives cfg a connect timeout (unless the DSN set a nonzero
// connect_timeout; 0, libpq's "wait forever", is not honoured: every
// network operation needs a deadline) and a dialer with TCP keepalive and
// TCP_USER_TIMEOUT. These bound a silent peer at the socket level without
// touching the read/write deadlines pgx itself sets to cancel a query when
// its context ends.
func bound(cfg *pgx.ConnConfig) {
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	d := &net.Dialer{
		Timeout: cfg.ConnectTimeout,
		KeepAliveConfig: net.KeepAliveConfig{
			Enable:   true,
			Idle:     tcpKeepAlive,
			Interval: tcpKeepAlive,
			Count:    3,
		},
		Control: func(network, _ string, c syscall.RawConn) error {
			if !strings.HasPrefix(network, "tcp") {
				return nil // a Unix socket: no TCP options
			}
			return setUserTimeout(c, tcpUserTimeout)
		},
	}
	cfg.DialFunc = d.DialContext
}
