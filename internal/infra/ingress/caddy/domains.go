package caddy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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
// keeps working under the coverage rule. Creating the dir makes it run once,
// so an apps-data dir that can't be read skips seeding (nothing is created)
// and the next start retries, rather than seeding nothing for good.
func (m *Manager) seedDomains() {
	if m.domains.Exists() {
		return
	}
	apps, warnings, err := m.readApps()
	if err != nil {
		m.log.Warn("ingress: apps-data unreadable, domain registrations not seeded (retried on the next start)", "error", err)
		return
	}
	for _, w := range warnings {
		m.log.Warn("ingress: seed domain registrations", "warning", w)
	}
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
func seedRegistrations(apps []AppIngress) []model.RegisteredDomain {
	sorted := append([]AppIngress(nil), apps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AppID < sorted[j].AppID })
	seen := map[string]bool{}
	var out []model.RegisteredDomain
	add := func(host string, ssl bool) {
		if seen[host] || model.ValidateDomainName(host) != nil || model.IsWildcardDomain(host) {
			return
		}
		seen[host] = true
		mode := model.DomainTLSOff
		if ssl {
			mode = model.DomainTLSAuto
		}
		out = append(out, model.RegisteredDomain{Name: host, TLS: mode})
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

// domainUsers maps each registration name to the ids of the apps (in scan
// order) with at least one hostname that registration wins.
func domainUsers(apps []AppIngress, regs []domains.Registration) map[string][]string {
	out := map[string][]string{}
	for _, a := range apps {
		used := map[string]bool{}
		for _, h := range a.Ingress.DomainNames() {
			if win, ok := model.Winning(regs, h); ok && !used[win.Name] {
				used[win.Name] = true
				out[win.Name] = append(out[win.Name], a.AppID)
			}
		}
	}
	return out
}

func describe(r domains.Registration, apps []string) command.Domain {
	return command.Domain{RegisteredDomain: r.RegisteredDomain, Apps: apps}
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
// the store checks the pair (match, coverage, expiry) before writing
// anything. The caller reloads the proxy.
func (m *Manager) CreateDomain(ctx context.Context, in command.CreateDomainRequest) (command.Domain, error) {
	if !m.cfg.IsIngressEnabled() {
		return command.Domain{}, ErrIngressDisabled
	}
	var cert, key []byte
	if in.TLS == model.DomainTLSCertificate {
		plain, err := crypto.DecryptWithPrivateKey(m.cfg.GetAgentKeyPath(), in.Key)
		if err != nil {
			return command.Domain{}, fmt.Errorf("decrypt the certificate key: %w", err)
		}
		cert, key = []byte(in.Certificate), []byte(plain)
	}
	if err := ctx.Err(); err != nil {
		return command.Domain{}, err
	}
	reg, err := m.domains.Put(in.Name, in.TLS, cert, key)
	if err != nil {
		return command.Domain{}, err
	}
	// The other registrations decide which hosts this one wins.
	regs, err := m.domains.List()
	if err != nil {
		regs = []domains.Registration{reg}
	}
	apps, _ := m.scanApps()
	return describe(reg, domainUsers(apps, regs)[reg.Name]), nil
}

// orphanedBy returns the apps (in scan order) with a hostname that name wins
// and no other registration in regs covers — the hosts deleting name would
// drop out of ingress. A host a remaining registration (e.g. a wildcard next
// to a seeded exact name) still covers just moves over to it.
func orphanedBy(apps []AppIngress, regs []domains.Registration, name string) []AppIngress {
	rest := make([]domains.Registration, 0, len(regs))
	for _, r := range regs {
		if r.Name != name {
			rest = append(rest, r)
		}
	}
	var out []AppIngress
	for _, a := range apps {
		for _, h := range a.Ingress.DomainNames() {
			if win, ok := model.Winning(regs, h); !ok || win.Name != name {
				continue
			}
			if _, ok := model.Winning(rest, h); !ok {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// DeleteDomain removes a registration, refusing while an app uses a hostname
// only this registration covers (the error names those apps). It fails
// closed: when apps-data or an app's config can't be read, it can't tell
// whether the name is in use, so it refuses too.
func (m *Manager) DeleteDomain(_ context.Context, name string) error {
	if !m.cfg.IsIngressEnabled() {
		return ErrIngressDisabled
	}
	regs, err := m.domains.List()
	if err != nil {
		return err
	}
	apps, warnings, err := m.readApps()
	if err == nil && len(warnings) > 0 {
		err = errors.New(strings.Join(warnings, "; "))
	}
	if err != nil {
		return fmt.Errorf("cannot remove %s: the apps using it can't be checked: %w", name, err)
	}
	if users := orphanedBy(apps, regs, name); len(users) > 0 {
		names := make([]string, 0, len(users))
		for _, a := range users {
			names = append(names, a.label())
		}
		return fmt.Errorf("%s is used by %s and no other domain on this server covers those hostnames: remove them from those apps (or add a covering domain) first", name, strings.Join(names, ", "))
	}
	existed, err := m.domains.Delete(name)
	if err != nil {
		return err
	}
	if !existed {
		return fmt.Errorf("%s is not added on this server", name)
	}
	return nil
}
