package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"winterflow/pkg/logger"
)

func TestHubTokenSubcommand(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	path := filepath.Join(t.TempDir(), "api.sqlite")
	var out bytes.Buffer
	if err := runHubToken([]string{"--ttl", "30m"}, "sqlite://"+path, log, &out); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(out.String())
	if !regexp.MustCompile(`^wfh_[A-Za-z0-9_-]{43}$`).MatchString(token) {
		t.Fatalf("token = %q", token)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("output has more than the token: %q", out.String())
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sum := sha256.Sum256([]byte(token))
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM hub_join_tokens WHERE token_hash = ? AND used_at IS NULL`, hex.EncodeToString(sum[:])).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hashed row: n=%d err=%v", n, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM hub_join_tokens WHERE token_hash = ?`, token).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plaintext stored: n=%d err=%v", n, err)
	}
}

func TestHubTokenSubcommandBadArgs(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	for _, args := range [][]string{{"--ttl", "0"}, {"--ttl", "-1h"}, {"extra"}, {"--nope"}} {
		if err := runHubToken(args, "sqlite://"+filepath.Join(t.TempDir(), "x.sqlite"), log, &bytes.Buffer{}); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
