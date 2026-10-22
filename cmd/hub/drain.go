package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	redisbus "winterflow/internal/infra/transport/redis/bus"
	hubsreg "winterflow/internal/infra/transport/redis/hubs"
	"winterflow/pkg/config"
)

// drainTimeout bounds the drain / undrain subcommand's Redis work.
const drainTimeout = 10 * time.Second

// runDrain is the hidden drain (on) / undrain (off) subcommand: it sets or
// deletes this hub's flag hub:draining:<hub_id> in the Redis the hub uses
// (same settings, TLS included). The running hub reads the flag every few
// seconds; it survives hub restarts until undrain (it lives in Redis).
func runDrain(on bool, out io.Writer) error {
	cfg := config.NewServerConfig("distributed")
	hubID := cfg.GetHubID()
	if hubID == "" {
		return errors.New("HUB_PUBLIC_ADDR must be host:port")
	}
	rc, err := redisbus.NewRedisClient(cfg)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := hubsreg.NewRegistry(rc).SetDraining(ctx, hubID, on); err != nil {
		return err
	}
	state := "accepting"
	if on {
		state = "draining"
	}
	_, err = fmt.Fprintf(out, "%s: %s\n", hubID, state)
	return err
}
