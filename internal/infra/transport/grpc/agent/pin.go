package agent

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"winterflow/pkg/cert"
)

// ErrHubNotTrusted marks a connection attempt that failed because the hub's
// certificate is not one the agent accepts: it lacks the winterflow-hub URI
// SAN (a CA-signed certificate that is not a hub's, such as the Redis one),
// or its fingerprint is not in the stored pin set. Run answers it with an
// ask (the pin set may be stale), at most once per askFloor.
var ErrHubNotTrusted = errors.New("hub certificate not trusted")

// verifyHub is the TLS VerifyPeerCertificate hook. It runs after the normal
// chain and host name verification against the agent's CA, so the first
// verified chain's leaf is the hub's certificate: it must carry a
// winterflow-hub URI SAN and, when a pin set is stored, its fingerprint must
// be in the set. With no set stored (an agent installed before pinning, or
// an API that trusts none yet) the CA and marker checks are all, logged
// once.
func (a *Agent) verifyHub(_ [][]byte, chains [][]*x509.Certificate) error {
	if len(chains) == 0 || len(chains[0]) == 0 {
		return a.notTrusted(errors.New("no verified hub certificate chain"))
	}
	leaf := chains[0][0]
	sum := sha256.Sum256(leaf.Raw)
	fp := hex.EncodeToString(sum[:])
	if _, ok := cert.HubIDFromCert(leaf); !ok {
		return a.notTrusted(fmt.Errorf("the certificate lacks the %s URI (fingerprint %s)", cert.HubURIPrefix, fp))
	}
	fps := a.pins()
	if len(fps) == 0 {
		if a.warnedNoPins.CompareAndSwap(false, true) {
			a.log.Warn("no trusted hub certificates stored: checking the CA and the hub marker only until the API names some", "fingerprint", fp)
		}
		a.accepted(fp)
		return nil
	}
	a.warnedNoPins.Store(false)
	if !slices.Contains(fps, fp) {
		return a.notTrusted(fmt.Errorf("fingerprint %s is not among the %d trusted hub certificates", fp, len(fps)))
	}
	a.accepted(fp)
	return nil
}

// accepted records the fingerprint of the hub certificate just accepted.
func (a *Agent) accepted(fp string) {
	a.pinMu.Lock()
	a.seenFP = fp
	a.pinMu.Unlock()
}

// notTrusted records why the hub's certificate was refused (read by
// connectRegisterStream once the attempt fails) and returns the TLS error.
func (a *Agent) notTrusted(cause error) error {
	err := fmt.Errorf("%w: %w", ErrHubNotTrusted, cause)
	a.pinMu.Lock()
	a.pinErr = err
	a.pinMu.Unlock()
	a.log.Warn("refused the hub's certificate", "error", err)
	return err
}

// takePinError returns and clears the last certificate refusal.
func (a *Agent) takePinError() error {
	a.pinMu.Lock()
	defer a.pinMu.Unlock()
	err := a.pinErr
	a.pinErr = nil
	return err
}

// peerFingerprint is the fingerprint of the last hub certificate the agent
// accepted (the connected hub's while a stream is up); "" before any.
func (a *Agent) peerFingerprint() string {
	a.pinMu.Lock()
	defer a.pinMu.Unlock()
	return a.seenFP
}
