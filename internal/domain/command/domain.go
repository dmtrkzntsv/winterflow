package command

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
)

// Domain registrations: the names an agent's ingress accepts hostnames under,
// each with a TLS mode. The agent's {AGENT_DATA_DIR}/domains/ is the source of
// truth; the API mirrors it into its server_domains index.

// DomainTLS is how TLS works for hostnames under a registration.
type DomainTLS string

const (
	// DomainTLSAuto: the agent obtains one ACME (HTTP-01) cert per app host.
	DomainTLSAuto DomainTLS = "auto"
	// DomainTLSCertificate: an uploaded PEM cert + key serves every host.
	DomainTLSCertificate DomainTLS = "certificate"
	// DomainTLSOff: TLS ends in front (tunnel, proxy); plain HTTP only.
	DomainTLSOff DomainTLS = "off"
)

// Valid reports whether t is one of the three modes.
func (t DomainTLS) Valid() bool {
	return t == DomainTLSAuto || t == DomainTLSCertificate || t == DomainTLSOff
}

// Domain is one registration as the agent reports it. The key never leaves
// the agent; NotAfter and Issuer describe the stored certificate.
type Domain struct {
	Name     string     `json:"name"`
	TLS      DomainTLS  `json:"tls"`
	NotAfter *time.Time `json:"not_after,omitempty"` // certificate mode
	Issuer   string     `json:"issuer,omitempty"`    // certificate mode, CN of the issuer
	Apps     []string   `json:"apps"`                // app ids using a covered host on this agent
}

// MarshalJSON renders a nil Apps as [] so clients can iterate it unconditionally.
func (d Domain) MarshalJSON() ([]byte, error) {
	type plain Domain
	if d.Apps == nil {
		d.Apps = []string{}
	}
	return json.Marshal(plain(d))
}

type ListDomainsRequest struct{}

type ListDomainsResponse struct {
	Domains []Domain `json:"domains"`
}

// CreateDomainRequest registers (or replaces) a name. In certificate mode
// Certificate is the PEM chain and Key the ECIES ciphertext of the PEM key,
// encrypted in the browser with the agent's public key.
type CreateDomainRequest struct {
	Name        string    `json:"name"`
	TLS         DomainTLS `json:"tls"`
	Certificate string    `json:"certificate,omitempty"`
	Key         string    `json:"key,omitempty"`
}

// Validate checks the name and mode, and that Certificate and Key are present
// exactly when the mode is certificate.
func (r CreateDomainRequest) Validate() error {
	if err := model.ValidateDomainName(r.Name); err != nil {
		return err
	}
	if !r.TLS.Valid() {
		return fmt.Errorf("tls must be auto, certificate or off, got %q", r.TLS)
	}
	if r.TLS == DomainTLSCertificate {
		if r.Certificate == "" || r.Key == "" {
			return errors.New("certificate mode needs a certificate and a key")
		}
	} else if r.Certificate != "" || r.Key != "" {
		return fmt.Errorf("a certificate and key are only accepted in certificate mode, not %q", r.TLS)
	}
	return nil
}

type CreateDomainResponse struct {
	Domain Domain `json:"domain"`
	// Warnings are non-fatal ingress problems from the reload that applied
	// the change (a load failure keeps the previous routing serving).
	Warnings []string `json:"warnings,omitempty"`
}

type DeleteDomainRequest struct {
	Name string `json:"name"`
}

func (r DeleteDomainRequest) Validate() error { return model.ValidateDomainName(r.Name) }

type DeleteDomainResponse struct {
	Name string `json:"name"`
	// Warnings are non-fatal ingress problems from the reload that applied
	// the change (a load failure keeps the previous routing serving).
	Warnings []string `json:"warnings,omitempty"`
}
