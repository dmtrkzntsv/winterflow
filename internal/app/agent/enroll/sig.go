package enroll

import (
	"context"
	"crypto/x509/pkix"
	"os"
	"os/signal"
	"syscall"
)

func pkixName(cn string) pkix.Name { return pkix.Name{CommonName: cn} }

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
}
