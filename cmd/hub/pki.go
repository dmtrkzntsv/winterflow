package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"winterflow/pkg/cert"
)

// listFlag collects a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// runPKI implements the hidden `pki` subcommand the control-plane and hub
// installers call: it makes --dir hold the CA, the hub certificate for every
// --host and, with --redis-host, a redis.crt/redis.key pair for those hosts
// signed by the same CA. It prints "changed" or "unchanged".
func runPKI(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("pki", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var hosts, redisHosts listFlag
	dir := fs.String("dir", "", "directory for the CA and certificates")
	fs.Var(&hosts, "host", "hub host or IP (repeatable)")
	fs.Var(&redisHosts, "redis-host", "Redis server host or IP (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("pki: --dir is required")
	}
	changed, err := cert.EnsureHubPKI(filepath.Clean(*dir), hosts)
	if err != nil {
		return err
	}
	if len(redisHosts) > 0 {
		c, err := cert.EnsureServerCert(*dir, "redis", redisHosts)
		if err != nil {
			return err
		}
		changed = changed || c
	}
	if changed {
		fmt.Fprintln(out, "changed")
	} else {
		fmt.Fprintln(out, "unchanged")
	}
	return nil
}

// runDevAgentCert implements the hidden `dev-agent-cert` subcommand behind
// `make generate-agent-certs`: it writes agent.key, agent.crt (CN =
// --server-id, signed by the CA in --dir) and ca.crt into --out, for local
// hub/agent testing without enrollment.
func runDevAgentCert(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("dev-agent-cert", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "directory holding the CA (ca.crt, ca.key)")
	outDir := fs.String("out", "", "directory for agent.key, agent.crt and ca.crt")
	serverID := fs.String("server-id", "", "server id, the certificate's CN")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *outDir == "" || strings.TrimSpace(*serverID) == "" {
		return fmt.Errorf("dev-agent-cert: --dir, --out and --server-id are required")
	}
	if err := cert.IssueAgentCert(filepath.Clean(*dir), filepath.Clean(*outDir), *serverID); err != nil {
		return err
	}
	fmt.Fprintf(out, "agent certificate for %s written to %s\n", strings.TrimSpace(*serverID), *outDir)
	return nil
}
