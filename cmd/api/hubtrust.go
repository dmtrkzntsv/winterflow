package main

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository/hubcert"
	certpkg "winterflow/pkg/cert"
	"winterflow/pkg/logger"
)

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

// readHubCert reads a PEM hub certificate and returns its hub id (from the
// winterflow-hub URI SAN) and fingerprint.
func readHubCert(path string) (hubID, fingerprint string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCertFile+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > maxCertFile {
		return "", "", fmt.Errorf("%s: larger than %d bytes", path, maxCertFile)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", "", fmt.Errorf("%s: not a PEM certificate", path)
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", path, err)
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

// runHubTrust is the hidden `hub-trust --cert <path>` subcommand: it
// records a hub certificate (the control plane's own, made by
// `winterflow-hub pki`) as trusted and retires the older certificates of
// its hub id. Rerunning it with the same certificate changes nothing.
func runHubTrust(args []string, dbURL string, log *logger.Logger, out io.Writer) error {
	fs := flag.NewFlagSet("hub-trust", flag.ContinueOnError)
	path := fs.String("cert", "", "the hub certificate (PEM)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *path == "" {
		return fmt.Errorf("--cert is required")
	}
	hubID, fp, err := readHubCert(*path)
	if err != nil {
		return err
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
	_, err = fmt.Fprintf(out, "trusted hub certificate %s for hub %s\n", fp, hubID)
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
// certificate (a leaked key) and prints how many it retired. Agents stop
// accepting a retired certificate once they refresh their pin set.
func runHubRetire(args []string, dbURL string, log *logger.Logger, out io.Writer) error {
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
	n, err := repo.Retire(ctx, hubID, fp, time.Now())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "retired %d hub certificate(s)\n", n)
	return err
}
