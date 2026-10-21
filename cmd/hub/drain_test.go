package main

import (
	"bytes"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestDrainSubcommand(t *testing.T) {
	mr := miniredis.RunT(t)
	t.Setenv("REDIS_ADDR", mr.Addr())
	t.Setenv("REDIS_TLS", "false")
	t.Setenv("HUB_PUBLIC_ADDR", "hub-01.example.com:50051")
	const key = "hub:draining:hub-01.example.com"

	var out bytes.Buffer
	if err := runDrain(true, &out); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists(key) {
		t.Fatal("drain did not set the flag")
	}
	if mr.TTL(key) != 0 {
		t.Fatal("the flag must not expire")
	}
	if out.String() != "hub-01.example.com: draining\n" {
		t.Fatalf("drain output %q", out.String())
	}

	out.Reset()
	if err := runDrain(false, &out); err != nil {
		t.Fatal(err)
	}
	if mr.Exists(key) {
		t.Fatal("undrain did not clear the flag")
	}
	if out.String() != "hub-01.example.com: accepting\n" {
		t.Fatalf("undrain output %q", out.String())
	}
}

func TestDrainSubcommandNeedsHubID(t *testing.T) {
	t.Setenv("REDIS_ADDR", "127.0.0.1:1")
	t.Setenv("REDIS_TLS", "false")
	t.Setenv("HUB_PUBLIC_ADDR", "")
	if err := runDrain(true, &bytes.Buffer{}); err == nil {
		t.Fatal("drain without HUB_PUBLIC_ADDR: want error")
	}
}

func TestDrainSubcommandRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	t.Setenv("REDIS_ADDR", mr.Addr())
	t.Setenv("REDIS_TLS", "false")
	t.Setenv("HUB_PUBLIC_ADDR", "hub-01.example.com:50051")
	mr.Close()
	if err := runDrain(true, &bytes.Buffer{}); err == nil {
		t.Fatal("drain with redis down: want error")
	}
}
