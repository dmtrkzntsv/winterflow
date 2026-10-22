package main

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository/hubcert"
	certpkg "winterflow/pkg/cert"
	"winterflow/pkg/logger"
	"winterflow/pkg/redisenv"
)

// openUsersFunc connects to the hubs' Redis as its admin (the API's own
// Redis settings) and returns the hub user manager and its close function.
type openUsersFunc func(ctx context.Context) (port.HubRedisUsers, func(), error)

// maxCertFile bounds the certificate file hub-trust reads.
const maxCertFile = 64 << 10

// openHubCerts opens the API's database and its hub certificate repository.
func openHubCerts(dbURL string, log *logger.Logger) (*hubcert.Repo, func(), error) {
	conn, err := db.Open(log, dbURL)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	repo, err := hubcert.New(conn.Tx, conn.Driver)
	if err != nil {
		_ = conn.Shutdown()
		return nil, nil, err
	}
	return repo, func() { _ = conn.Shutdown() }, nil
}

// readCertFile reads the first PEM certificate of a size-capped file.
func readCertFile(path string) (*x509.Certificate, *pem.Block, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCertFile+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxCertFile {
		return nil, nil, fmt.Errorf("%s: larger than %d bytes", path, maxCertFile)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("%s: not a PEM certificate", path)
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return crt, block, nil
}

// readHubCert reads a PEM hub certificate, checks that it chains to the CA
// at caPath (a server certificate, valid now), and returns its hub id (from
// the winterflow-hub URI SAN) and fingerprint.
func readHubCert(path, caPath string) (hubID, fingerprint string, err error) {
	crt, block, err := readCertFile(path)
	if err != nil {
		return "", "", err
	}
	ca, _, err := readCertFile(caPath)
	if err != nil {
		return "", "", fmt.Errorf("read the CA: %w", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := crt.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return "", "", fmt.Errorf("%s does not chain to the CA %s: %w", path, caPath, err)
	}
	id, ok := certpkg.HubIDFromCert(crt)
	if !ok {
		return "", "", fmt.Errorf("%s: not a hub certificate (no %s URI); rerun winterflow-hub pki to reissue it", path, certpkg.HubURIPrefix)
	}
	fp, err := certpkg.Fingerprint(string(pem.EncodeToMemory(block)))
	if err != nil {
		return "", "", err
	}
	return id, fp, nil
}

// runHubTrust is the hidden `hub-trust --cert <path> [--redis-env-out
// <file>]` subcommand: it records a hub certificate (the control plane's
// own, made by `winterflow-hub pki`) as trusted and retires the older
// certificates of its hub id. The certificate must chain to the CA (--ca,
// default $HUB_CERT_DIR/ca.crt, the CA the API signs with), which catches
// a wrong file. Rerunning it with the same certificate changes nothing.
// With --redis-env-out it then creates the hub's Redis user, or resets its
// password, and writes REDIS_USERNAME / REDIS_PASSWORD to that file (0600,
// atomically); the password is printed nowhere else. Without the flag the
// Redis user is left as it is (a reset nobody receives would only lock the
// running hub out).
func runHubTrust(args []string, dbURL, caPath string, openUsers openUsersFunc, log *logger.Logger, out io.Writer) error {
	fs := flag.NewFlagSet("hub-trust", flag.ContinueOnError)
	path := fs.String("cert", "", "the hub certificate (PEM)")
	ca := fs.String("ca", caPath, "the CA certificate the hub certificate must chain to")
	envOut := fs.String("redis-env-out", "", "write the hub's Redis credentials to this file (0600)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *path == "" {
		return fmt.Errorf("--cert is required")
	}
	if *ca == "" {
		return fmt.Errorf("--ca is required")
	}
	if *envOut != "" {
		// Fail before anything changes when the file cannot be written.
		if fi, err := os.Stat(filepath.Dir(*envOut)); err != nil || !fi.IsDir() {
			return fmt.Errorf("--redis-env-out %s: its folder does not exist", *envOut)
		}
	}
	hubID, fp, err := readHubCert(*path, *ca)
	if err != nil {
		return err
	}
	// The hub id names its Redis user; refuse one that cannot (an IPv6
	// address) before anything is recorded.
	if err := port.CheckHubRedisID(hubID); err != nil {
		return fmt.Errorf("%s: hub id %q cannot name a Redis user; the host must be a DNS name or an IPv4 address", *path, hubID)
	}
	repo, closeDB, err := openHubCerts(dbURL, log)
	if err != nil {
		return err
	}
	defer closeDB()
	ctx, cancel := context.WithTimeout(context.Background(), hubTokenTimeout)
	defer cancel()
	if err := repo.Trust(ctx, hubID, fp, time.Now()); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "trusted hub certificate %s for hub %s\n", fp, hubID); err != nil {
		return err
	}
	if *envOut == "" {
		return nil
	}
	users, closeUsers, err := openUsers(ctx)
	if err != nil {
		return err
	}
	defer closeUsers()
	user, pass, err := users.Ensure(ctx, hubID)
	if err != nil {
		return fmt.Errorf("create the Redis user of hub %s: %w", hubID, err)
	}
	if err := redisenv.Write(*envOut, user, pass); err != nil {
		return fmt.Errorf("write the Redis credentials of hub %s: %w", hubID, err)
	}
	_, err = fmt.Fprintf(out, "wrote the Redis credentials of %s to %s\n", user, *envOut)
	return err
}

// normalizeFingerprint accepts a SHA-256 fingerprint in hex, upper or lower
// case, with or without colons, and returns it in lower case without them.
func normalizeFingerprint(s string) (string, error) {
	fp := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
	if b, err := hex.DecodeString(fp); err != nil || len(b) != 32 {
		return "", fmt.Errorf("--fingerprint must be a SHA-256 in hex (64 digits)")
	}
	return fp, nil
}

// runHubRetire is the hidden `hub-retire --hub <id> | --fingerprint <fp>`
// subcommand: it retires a hub's certificates (a removed hub) or one
// certificate (a leaked key) and prints how many it retired; retiring none
// is an error (exit status 1). A hub left with no active certificate also
// loses its Redis user (deleted, its sessions disconnected, under the
// per-hub lock so a hub joining at that moment keeps its new user), on a
// rerun too, so a Redis failure after the retire is mended by running it
// again. A hub id with no certificate on record never contacts Redis.
// Agents stop accepting a retired certificate once they refresh their pin
// set.
func runHubRetire(args []string, dbURL string, openUsers openUsersFunc, log *logger.Logger, out io.Writer) error {
	fs := flag.NewFlagSet("hub-retire", flag.ContinueOnError)
	hub := fs.String("hub", "", "retire every certificate of this hub id")
	fpArg := fs.String("fingerprint", "", "retire the certificate with this SHA-256 fingerprint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	hubID := strings.ToLower(strings.TrimSpace(*hub))
	var fp string
	switch {
	case hubID != "" && *fpArg == "":
	case hubID == "" && *fpArg != "":
		var err error
		if fp, err = normalizeFingerprint(*fpArg); err != nil {
			return err
		}
	default:
		return fmt.Errorf("give exactly one of --hub and --fingerprint")
	}
	repo, closeDB, err := openHubCerts(dbURL, log)
	if err != nil {
		return err
	}
	defer closeDB()
	ctx, cancel := context.WithTimeout(context.Background(), hubTokenTimeout)
	defer cancel()
	rel := &hubReleaser{open: openUsers}
	defer rel.close()
	n, err := repo.Retire(ctx, hubID, fp, time.Now(), rel.release)
	var delErr error
	if err != nil {
		if !rel.tried {
			return err // the retire itself failed
		}
		delErr = fmt.Errorf("%w (rerun hub-retire to finish)", err)
	}
	if n == 0 {
		// Nothing matched: a mistyped id or fingerprint must not pass as done.
		var err error
		if hubID != "" {
			err = fmt.Errorf("no active hub certificate of hub %q to retire (already retired, or a wrong hub id)", hubID)
		} else {
			err = fmt.Errorf("no active hub certificate with fingerprint %s to retire (already retired, or a wrong fingerprint)", fp)
		}
		return errors.Join(err, delErr)
	}
	if _, err := fmt.Fprintf(out, "retired %d hub certificate(s)\n", n); err != nil {
		return err
	}
	if delErr != nil {
		return delErr
	}
	for _, id := range rel.deleted {
		if _, err := fmt.Fprintf(out, "deleted the Redis user of hub %s\n", id); err != nil {
			return err
		}
	}
	return nil
}

// hubReleaser deletes the Redis user of a hub left with no active
// certificate; it connects to Redis only when there is one.
type hubReleaser struct {
	open    openUsersFunc
	users   port.HubRedisUsers
	closeFn func()
	tried   bool
	deleted []string
}

func (h *hubReleaser) release(ctx context.Context, hubID string) error {
	h.tried = true
	if h.users == nil {
		users, closeFn, err := h.open(ctx)
		if err != nil {
			return err
		}
		h.users, h.closeFn = users, closeFn
	}
	if err := h.users.Delete(ctx, hubID); err != nil {
		return fmt.Errorf("delete its Redis user: %w", err)
	}
	h.deleted = append(h.deleted, hubID)
	return nil
}

func (h *hubReleaser) close() {
	if h.closeFn != nil {
		h.closeFn()
	}
}
