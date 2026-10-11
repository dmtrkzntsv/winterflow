//go:build !linux

package pgsql

import (
	"syscall"
	"time"
)

// setUserTimeout is a no-op where TCP_USER_TIMEOUT does not exist; keepalive
// alone bounds an idle dead peer there.
func setUserTimeout(syscall.RawConn, time.Duration) error { return nil }
