package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPKISubcommand(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := runPKI(append([]string{"--dir", dir}, args...), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	for _, f := range []string{"ca.crt", "ca.key", "hub.crt", "hub.key", "redis.crt", "redis.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("%s exists before the first run", f)
		}
	}
	if got := run("--host", "hub-01.example.com", "--host", "10.0.0.5"); got != "changed\n" {
		t.Fatalf("first run = %q", got)
	}
	for _, f := range []string{"ca.crt", "ca.key", "hub.crt", "hub.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "redis.crt")); err == nil {
		t.Error("redis pair issued without --redis-host")
	}
	if got := run("--host", "hub-01.example.com", "--host", "10.0.0.5"); got != "unchanged\n" {
		t.Fatalf("second run = %q", got)
	}
	if got := run("--host", "hub-01.example.com", "--host", "10.0.0.5", "--redis-host", "127.0.0.1", "--redis-host", "cp.example.com"); got != "changed\n" {
		t.Fatalf("redis run = %q", got)
	}
	for _, f := range []string{"redis.crt", "redis.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing: %v", f, err)
		}
	}
	if got := run("--host", "hub-01.example.com", "--host", "10.0.0.5", "--redis-host", "127.0.0.1", "--redis-host", "cp.example.com"); got != "unchanged\n" {
		t.Fatalf("redis rerun = %q", got)
	}
}

func TestPKISubcommandRequiresDirAndHost(t *testing.T) {
	var out bytes.Buffer
	if err := runPKI([]string{"--host", "h"}, &out); err == nil {
		t.Error("missing --dir accepted")
	}
	if err := runPKI([]string{"--dir", t.TempDir()}, &out); err == nil {
		t.Error("missing --host accepted")
	}
}

func TestDevAgentCertSubcommand(t *testing.T) {
	ca := t.TempDir()
	var out bytes.Buffer
	if err := runPKI([]string{"--dir", ca, "--host", "localhost"}, &out); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "agent-certs")
	out.Reset()
	if err := runDevAgentCert([]string{"--dir", ca, "--out", dst, "--server-id", "srv-dev"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"agent.key", "agent.crt", "ca.crt"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err != nil {
			t.Errorf("%s missing: %v", f, err)
		}
	}
	if out.Len() == 0 {
		t.Error("no output")
	}
	for name, args := range map[string][]string{
		"no dir":       {"--out", dst, "--server-id", "s"},
		"no out":       {"--dir", ca, "--server-id", "s"},
		"no server id": {"--dir", ca, "--out", dst},
		"no CA":        {"--dir", t.TempDir(), "--out", dst, "--server-id", "s"},
		"bad flag":     {"--nope"},
	} {
		if err := runDevAgentCert(args, &out); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
