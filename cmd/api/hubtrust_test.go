package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	certpkg "winterflow/pkg/cert"
	"winterflow/pkg/logger"
)

func hubCertFile(t *testing.T, host string) (path, fp string) {
	t.Helper()
	dir := t.TempDir()
	if _, err := certpkg.EnsureHubPKI(dir, []string{host}); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "hub.crt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fp, err = certpkg.Fingerprint(string(data)); err != nil {
		t.Fatal(err)
	}
	return path, fp
}

func activeRows(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.Query(`SELECT fingerprint, hub_id FROM hub_certificates WHERE retired_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var fp, hub string
		if err := rows.Scan(&fp, &hub); err != nil {
			t.Fatal(err)
		}
		out[fp] = hub
	}
	return out
}

func TestHubTrustAndRetire(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	dbPath := filepath.Join(t.TempDir(), "api.sqlite")
	url := "sqlite://" + dbPath
	trust := func(path string) string {
		t.Helper()
		var out bytes.Buffer
		if err := runHubTrust([]string{"--cert", path}, url, log, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	first, fp1 := hubCertFile(t, "hub-01.winterflow.dev")
	if out := trust(first); !strings.Contains(out, fp1) || !strings.Contains(out, "hub-01.winterflow.dev") {
		t.Fatalf("output = %q", out)
	}
	trust(first) // idempotent
	other, fp3 := hubCertFile(t, "hub-02.winterflow.dev")
	trust(other)
	// A new certificate for hub-01 retires the first one.
	second, fp2 := hubCertFile(t, "hub-01.winterflow.dev")
	trust(second)
	if got := activeRows(t, dbPath); len(got) != 2 || got[fp2] != "hub-01.winterflow.dev" || got[fp3] != "hub-02.winterflow.dev" {
		t.Fatalf("active = %v", got)
	}
	// The retired certificate is not trusted again.
	if err := runHubTrust([]string{"--cert", first}, url, log, &bytes.Buffer{}); err == nil {
		t.Fatal("retired certificate trusted again")
	}

	var out bytes.Buffer
	if err := runHubRetire([]string{"--fingerprint", strings.ToUpper(fp3)}, url, log, &out); err != nil || out.String() != "retired 1 hub certificate(s)\n" {
		t.Fatalf("by fingerprint: %q %v", out.String(), err)
	}
	out.Reset()
	if err := runHubRetire([]string{"--hub", "HUB-01.winterflow.dev"}, url, log, &out); err != nil || out.String() != "retired 1 hub certificate(s)\n" {
		t.Fatalf("by hub: %q %v", out.String(), err)
	}
	out.Reset()
	if err := runHubRetire([]string{"--hub", "hub-01.winterflow.dev"}, url, log, &out); err != nil || out.String() != "retired 0 hub certificate(s)\n" {
		t.Fatalf("again: %q %v", out.String(), err)
	}
	if got := activeRows(t, dbPath); len(got) != 0 {
		t.Fatalf("active = %v", got)
	}
}

func TestHubTrustRefusesNonHubCert(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	dir := t.TempDir()
	if _, err := certpkg.EnsureHubPKI(dir, []string{"hub.test"}); err != nil {
		t.Fatal(err)
	}
	notPEM := filepath.Join(dir, "garbage")
	if err := os.WriteFile(notPEM, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	url := "sqlite://" + filepath.Join(t.TempDir(), "x.sqlite")
	// The CA certificate carries no winterflow-hub URI.
	for _, args := range [][]string{
		{"--cert", filepath.Join(dir, "ca.crt")},
		{"--cert", notPEM},
		{"--cert", filepath.Join(dir, "missing")},
		{},
		{"--cert", filepath.Join(dir, "hub.crt"), "extra"},
	} {
		if err := runHubTrust(args, url, log, &bytes.Buffer{}); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestHubRetireBadArgs(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	url := "sqlite://" + filepath.Join(t.TempDir(), "x.sqlite")
	for _, args := range [][]string{
		{},
		{"--hub", "h", "--fingerprint", strings.Repeat("a", 64)},
		{"--fingerprint", "xyz"},
		{"--fingerprint", strings.Repeat("a", 62)},
		{"--hub", "h", "extra"},
		{"--nope"},
	} {
		if err := runHubRetire(args, url, log, &bytes.Buffer{}); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	want := strings.Repeat("ab", 32)
	in := strings.ToUpper(strings.TrimSuffix(strings.Repeat("ab:", 32), ":"))
	if got, err := normalizeFingerprint(in); err != nil || got != want {
		t.Fatalf("got %q %v", got, err)
	}
}
