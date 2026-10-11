package command

import (
	"encoding/json"
	"errors"
	"fmt"

	"winterflow/internal/domain/model"
)

// Domain registrations: the names an agent's ingress accepts hostnames under,
// each with a TLS mode. The agent's {AGENT_DATA_DIR}/domains/ is the source of
// truth; the API mirrors it into its server_domains index.

// Domain is one registration as the agent reports it, with the ids of the
// apps using a covered host on this agent. The key never leaves the agent.
type Domain struct {
	model.RegisteredDomain
	Apps []string `json:"apps"`
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
	Name        string          `json:"name"`
	TLS         model.DomainTLS `json:"tls"`
	Certificate string          `json:"certificate,omitempty"`
	Key         string          `json:"key,omitempty"`
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
	if r.TLS == model.DomainTLSCertificate {
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
