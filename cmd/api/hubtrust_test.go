package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
	certpkg "winterflow/pkg/cert"
	"winterflow/pkg/logger"
)

// fakeUsers hands out an in-memory hub user manager and counts the
// connections opened.
type fakeUsers struct {
	enrolltest.RedisUsers
	opened  int
	openErr error
}

func (f *fakeUsers) open(context.Context) (port.HubRedisUsers, func(), error) {
	if f.openErr != nil {
		return nil, nil, f.openErr
	}
	f.opened++
	return &f.RedisUsers, func() {}, nil
}

func hubCertFile(t *testing.T, host string) (path, ca, fp string) {
	t.Helper()
	dir := t.TempDir()
	if _, err := certpkg.EnsureHubPKI(dir, []string{host}); err != nil {
		t.Fatal(err)
	}
	path, ca = filepath.Join(dir, "hub.crt"), filepath.Join(dir, "ca.crt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fp, err = certpkg.Fingerprint(string(data)); err != nil {
		t.Fatal(err)
	}
	return path, ca, fp
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
	users := &fakeUsers{}
	trust := func(path, ca string) string {
		t.Helper()
		var out bytes.Buffer
		if err := runHubTrust([]string{"--cert", path}, url, ca, users.open, log, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	first, ca1, fp1 := hubCertFile(t, "hub-01.winterflow.dev")
	if out := trust(first, ca1); !strings.Contains(out, fp1) || !strings.Contains(out, "hub-01.winterflow.dev") {
		t.Fatalf("output = %q", out)
	}
	trust(first, ca1) // idempotent
	other, ca3, fp3 := hubCertFile(t, "hub-02.winterflow.dev")
	// A certificate of another CA is refused (a wrong file), also via --ca.
	if err := runHubTrust([]string{"--cert", other}, url, ca1, users.open, log, &bytes.Buffer{}); err == nil {
		t.Fatal("certificate of another CA trusted")
	}
	if err := runHubTrust([]string{"--cert", other, "--ca", ca3}, url, ca1, users.open, log, &bytes.Buffer{}); err != nil {
		t.Fatalf("--ca: %v", err)
	}
	// A new certificate for hub-01 retires the first one.
	second, ca2, fp2 := hubCertFile(t, "hub-01.winterflow.dev")
	trust(second, ca2)
	if got := activeRows(t, dbPath); len(got) != 2 || got[fp2] != "hub-01.winterflow.dev" || got[fp3] != "hub-02.winterflow.dev" {
		t.Fatalf("active = %v", got)
	}
	// The retired certificate is not trusted again.
	if err := runHubTrust([]string{"--cert", first}, url, ca1, users.open, log, &bytes.Buffer{}); err == nil {
		t.Fatal("retired certificate trusted again")
	}

	// Without --redis-env-out no Redis user is touched.
	if users.opened != 0 {
		t.Fatalf("redis opened %d times", users.opened)
	}
	var out bytes.Buffer
	if err := runHubRetire([]string{"--fingerprint", strings.ToUpper(fp3)}, url, users.open, log, &out); err != nil ||
		out.String() != "retired 1 hub certificate(s)\ndeleted the Redis user of hub hub-02.winterflow.dev\n" {
		t.Fatalf("by fingerprint: %q %v", out.String(), err)
	}
	out.Reset()
	if err := runHubRetire([]string{"--hub", "HUB-01.winterflow.dev"}, url, users.open, log, &out); err != nil ||
		out.String() != "retired 1 hub certificate(s)\ndeleted the Redis user of hub hub-01.winterflow.dev\n" {
		t.Fatalf("by hub: %q %v", out.String(), err)
	}
	if want := []string{"hub-02.winterflow.dev", "hub-01.winterflow.dev"}; strings.Join(users.Deleted, ",") != strings.Join(want, ",") {
		t.Fatalf("deleted users = %v", users.Deleted)
	}
	out.Reset()
	// Nothing left to retire is an error, by hub and by fingerprint.
	if err := runHubRetire([]string{"--hub", "hub-01.winterflow.dev"}, url, users.open, log, &out); err == nil || !strings.Contains(err.Error(), "no active hub certificate") || out.Len() != 0 {
		t.Fatalf("again: %q %v", out.String(), err)
	}
	if err := runHubRetire([]string{"--fingerprint", fp3}, url, users.open, log, &out); err == nil || !strings.Contains(err.Error(), "no active hub certificate") {
		t.Fatalf("again by fingerprint: %v", err)
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
		{"--cert", filepath.Join(dir, "hub.crt"), "--ca", filepath.Join(dir, "missing")},
		{"--cert", filepath.Join(dir, "hub.crt"), "--ca", ""},
	} {
		if err := runHubTrust(args, url, filepath.Join(dir, "ca.crt"), (&fakeUsers{}).open, log, &bytes.Buffer{}); err == nil {
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
		if err := runHubRetire(args, url, (&fakeUsers{}).open, log, &bytes.Buffer{}); err == nil {
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

func TestHubTrustWritesRedisEnv(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	url := "sqlite://" + filepath.Join(t.TempDir(), "api.sqlite")
	path, ca, _ := hubCertFile(t, "hub-01.winterflow.dev")
	envOut := filepath.Join(t.TempDir(), "redis.env")
	users := &fakeUsers{}
	var out bytes.Buffer
	if err := runHubTrust([]string{"--cert", path, "--redis-env-out", envOut}, url, ca, users.open, log, &out); err != nil {
		t.Fatal(err)
	}
	pass := users.Password("hub-01.winterflow.dev")
	data, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatal(err)
	}
	if pass == "" || string(data) != "REDIS_USERNAME=hub:hub-01.winterflow.dev\nREDIS_PASSWORD="+pass+"\n" {
		t.Fatalf("env file = %q (password %q)", data, pass)
	}
	if fi, _ := os.Stat(envOut); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	// The password goes to the file only.
	if strings.Contains(out.String(), pass) || !strings.Contains(out.String(), envOut) {
		t.Fatalf("output = %q", out.String())
	}
	// A rerun resets the password and rewrites the file.
	if err := runHubTrust([]string{"--cert", path, "--redis-env-out", envOut}, url, ca, users.open, log, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(envOut); string(again) == string(data) || !strings.Contains(string(again), users.Password("hub-01.winterflow.dev")) {
		t.Fatalf("rerun env file = %q", again)
	}
}

func TestHubTrustRedisFailures(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	url := "sqlite://" + filepath.Join(t.TempDir(), "api.sqlite")
	path, ca, _ := hubCertFile(t, "hub-01.winterflow.dev")
	envOut := filepath.Join(t.TempDir(), "redis.env")
	// A folder that does not exist is refused before anything changes.
	users := &fakeUsers{}
	if err := runHubTrust([]string{"--cert", path, "--redis-env-out", filepath.Join(t.TempDir(), "no", "redis.env")}, url, ca, users.open, log, &bytes.Buffer{}); err == nil || users.opened != 0 {
		t.Fatalf("missing folder: %v (opened %d)", err, users.opened)
	}
	for name, u := range map[string]*fakeUsers{
		"no connection": {openErr: errors.New("redis down")},
		"ensure fails":  {RedisUsers: enrolltest.RedisUsers{Err: errors.New("redis down")}},
	} {
		if err := runHubTrust([]string{"--cert", path, "--redis-env-out", envOut}, url, ca, u.open, log, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "redis down") {
			t.Errorf("%s: err = %v", name, err)
		}
		if _, err := os.Stat(envOut); !os.IsNotExist(err) {
			t.Errorf("%s: env file written", name)
		}
	}
}

// A Redis failure after the retire fails the command; a rerun (nothing
// left to retire) still deletes the user.
func TestHubRetireRedisFailure(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	url := "sqlite://" + filepath.Join(t.TempDir(), "api.sqlite")
	path, ca, _ := hubCertFile(t, "hub-01.winterflow.dev")
	if err := runHubTrust([]string{"--cert", path}, url, ca, (&fakeUsers{}).open, log, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	down := &fakeUsers{openErr: errors.New("redis down")}
	var out bytes.Buffer
	if err := runHubRetire([]string{"--hub", "hub-01.winterflow.dev"}, url, down.open, log, &out); err == nil || !strings.Contains(err.Error(), "redis down") {
		t.Fatalf("err = %v", err)
	}
	users := &fakeUsers{}
	err := runHubRetire([]string{"--hub", "hub-01.winterflow.dev"}, url, users.open, log, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no active hub certificate") || len(users.Deleted) != 1 || users.Deleted[0] != "hub-01.winterflow.dev" {
		t.Fatalf("rerun: %v, deleted %v", err, users.Deleted)
	}
	// An unknown fingerprint does not contact Redis.
	other := &fakeUsers{}
	if err := runHubRetire([]string{"--fingerprint", strings.Repeat("ab", 32)}, url, other.open, log, &bytes.Buffer{}); err == nil || other.opened != 0 {
		t.Fatalf("unknown fingerprint: %v (opened %d)", err, other.opened)
	}
}
