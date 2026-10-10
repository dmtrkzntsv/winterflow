package caddy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
	"winterflow/pkg/crypto"
)

// ErrIngressDisabled refuses domain mutations on an agent started with
// INGRESS_ENABLED=false: there is no proxy to apply them to.
var ErrIngressDisabled = errors.New("ingress is disabled on this server")

// seedDomains is the upgrade path: on the first start without a domains/
// dir, register every hostname existing apps use as an exact registration —
// auto where its ingress entry had ssl:true, off otherwise — so routing
// keeps working under the coverage rule. Creating the dir makes it run once.
func (m *Manager) seedDomains() {
	if m.domains.Exists() {
		return
	}
	apps, _ := m.scanApps()
	regs := seedRegistrations(apps)
	seeded, err := m.domains.Seed(regs)
	if err != nil {
		m.log.Warn("ingress: seed domain registrations", "error", err)
		return
	}
	if seeded {
		m.log.Info("ingress: registered the hostnames existing apps use", "count", len(regs), "dir", m.domains.Dir())
	}
}

// seedRegistrations maps the apps' hostnames (routes and domain-level
// redirect sources) to exact registrations. Apps are scanned in app-id order
// and the first entry for a host decides its mode, matching BuildConfig's
// first-claim-wins rule.
func seedRegistrations(apps []AppIngress) []domains.Registration {
	sorted := append([]AppIngress(nil), apps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AppID < sorted[j].AppID })
	seen := map[string]bool{}
	var out []domains.Registration
	add := func(host string, ssl bool) {
		if seen[host] || model.ValidateDomainName(host) != nil || model.IsWildcardDomain(host) {
			return
		}
		seen[host] = true
		mode := command.DomainTLSOff
		if ssl {
			mode = command.DomainTLSAuto
		}
		out = append(out, domains.Registration{Name: host, TLS: mode})
	}
	for _, a := range sorted {
		for _, d := range a.Ingress.Domains {
			add(d.Domain, d.SSL)
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path == "" {
				add(r.Domain, r.SSL)
			}
		}
	}
	return out
}

// domainUsers maps each registration name to the apps (in app-id order)
// with at least one hostname that registration wins.
func domainUsers(apps []AppIngress, regs []domains.Registration) map[string][]AppIngress {
	names := make([]string, len(regs))
	for i, r := range regs {
		names[i] = r.Name
	}
	out := map[string][]AppIngress{}
	for _, a := range apps {
		used := map[string]bool{}
		for _, h := range a.Ingress.DomainNames() {
			if win, ok := model.WinningDomain(names, h); ok && !used[win] {
				used[win] = true
				out[win] = append(out[win], a)
			}
		}
	}
	return out
}

func describe(r domains.Registration, users []AppIngress) command.Domain {
	d := command.Domain{Name: r.Name, TLS: r.TLS, Apps: []string{}}
	for _, a := range users {
		d.Apps = append(d.Apps, a.AppID)
	}
	if r.TLS == command.DomainTLSCertificate {
		if notAfter, issuer, err := domains.CertInfo(r.CertFile); err == nil {
			d.NotAfter, d.Issuer = &notAfter, issuer
		}
	}
	return d
}

// ListDomains reports every registration with the apps using it. It works
// with ingress disabled too: it only reads the stored list.
func (m *Manager) ListDomains(context.Context) ([]command.Domain, error) {
	regs, err := m.domains.List()
	if err != nil {
		return nil, err
	}
	apps, _ := m.scanApps()
	users := domainUsers(apps, regs)
	out := make([]command.Domain, 0, len(regs))
	for _, r := range regs {
		out = append(out, describe(r, users[r.Name]))
	}
	return out, nil
}

// CreateDomain registers (or replaces) a name. In certificate mode the key
// arrives ECIES-encrypted; it is decrypted with the agent's private key and
// the pair must match, cover the name and not be expired before anything is
// written. The caller reloads the proxy.
func (m *Manager) CreateDomain(_ context.Context, in command.CreateDomainRequest) (command.Domain, error) {
	if !m.cfg.IsIngressEnabled() {
		return command.Domain{}, ErrIngressDisabled
	}
	var cert, key []byte
	if in.TLS == command.DomainTLSCertificate {
		plain, err := crypto.DecryptWithPrivateKey(m.cfg.GetAgentKeyPath(), in.Key)
		if err != nil {
			return command.Domain{}, fmt.Errorf("decrypt the certificate key: %w", err)
		}
		cert, key = []byte(in.Certificate), []byte(plain)
		if _, err := domains.CheckCertificate(in.Name, cert, key, time.Now()); err != nil {
			return command.Domain{}, err
		}
	}
	if err := m.domains.Put(in.Name, in.TLS, cert, key); err != nil {
		return command.Domain{}, err
	}
	doms, err := m.ListDomains(context.Background())
	if err != nil {
		return command.Domain{}, err
	}
	for _, d := range doms {
		if d.Name == in.Name {
			return d, nil
		}
	}
	return command.Domain{Name: in.Name, TLS: in.TLS, Apps: []string{}}, nil
}

// DeleteDomain removes a registration, refusing while any app uses a
// hostname this registration wins (the error names those apps).
func (m *Manager) DeleteDomain(_ context.Context, name string) error {
	if !m.cfg.IsIngressEnabled() {
		return ErrIngressDisabled
	}
	regs, err := m.domains.List()
	if err != nil {
		return err
	}
	apps, _ := m.scanApps()
	if users := domainUsers(apps, regs)[name]; len(users) > 0 {
		labels := make([]string, 0, len(users))
		for _, a := range users {
			if a.Name != "" {
				labels = append(labels, fmt.Sprintf("%s (%s)", a.Name, a.AppID))
			} else {
				labels = append(labels, a.AppID)
			}
		}
		return fmt.Errorf("%s is used by %s: remove its hostnames from those apps first", name, strings.Join(labels, ", "))
	}
	existed, err := m.domains.Delete(name)
	if err != nil {
		return err
	}
	if !existed {
		return fmt.Errorf("%s is not registered on this server", name)
	}
	return nil
}
