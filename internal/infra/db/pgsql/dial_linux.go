package pgsql

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// setUserTimeout sets TCP_USER_TIMEOUT on the socket being dialed.
func setUserTimeout(c syscall.RawConn, d time.Duration) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(d/time.Millisecond))
	}); err != nil {
		return err
	}
	return serr
}
