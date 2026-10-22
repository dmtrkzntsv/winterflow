package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"winterflow/internal/domain/usecase/hubjoin"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository/hubtoken"
	"winterflow/pkg/logger"
)

// hubTokenTimeout bounds the command's database work.
const hubTokenTimeout = time.Minute

// runHubToken is the hidden `hub-token [--ttl 1h]` subcommand: it issues a
// one-time hub join token against the API's database and prints it once to
// out. Only the token's hash is stored, so it cannot be shown again.
func runHubToken(args []string, dbURL string, log *logger.Logger, out io.Writer) error {
	fs := flag.NewFlagSet("hub-token", flag.ContinueOnError)
	ttl := fs.Duration("ttl", hubjoin.DefaultTTL, "how long the token stays redeemable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *ttl <= 0 {
		return fmt.Errorf("--ttl must be positive")
	}
	conn, err := db.Open(log, dbURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = conn.Shutdown() }()
	repo, err := hubtoken.New(conn.Tx, conn.Driver)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), hubTokenTimeout)
	defer cancel()
	token, _, err := hubjoin.New(repo, nil, nil, nil, nil).Issue(ctx, *ttl)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, token)
	return err
}
