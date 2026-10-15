package template

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

// Limits of an instance's own choices.
const (
	maxHosts   = 50
	maxAppName = 100
)

// CheckName checks the app name an instance is created with.
func CheckName(name string) error {
	if strings.TrimSpace(name) == "" || len(name) > maxAppName || !trimmed(name) {
		return Invalid("name", "the app name must be 1-%d characters, no surrounding spaces", maxAppName)
	}
	return nil
}

// CheckHosts checks an instance's hostnames: each a valid hostname, at most
// once, routed to an http expose entry of the revision (tcp and udp entries
// never take hostnames). Whether a registered domain covers each, and that
// no other app holds it, is save-app's check.
func CheckHosts(spec xwinterflow.Spec, hosts []model.InstanceHost) error {
	if len(hosts) > maxHosts {
		return Invalid("hosts", "at most %d", maxHosts)
	}
	targets := spec.HTTPTargets()
	for i, h := range hosts {
		if !targets[h.Expose] {
			return Invalid(fmt.Sprintf("hosts[%d].expose", i), "%q is not an http expose entry of the template", h.Expose)
		}
	}
	if err := ingressOf(hosts).Validate(); err != nil {
		return Invalid("hosts", "%v", err)
	}
	return nil
}

// ingressOf is the app ingress the hosts render into.
func ingressOf(hosts []model.InstanceHost) *model.Ingress {
	ing := &model.Ingress{Domains: []model.IngressDomain{}, Redirects: []model.IngressRedirect{}}
	for _, h := range hosts {
		ing.Domains = append(ing.Domains, model.IngressDomain{Domain: h.Host, Expose: h.Expose})
	}
	return ing
}

// BackupChoice is an instance's setting for one backup its compose file
// declares.
type BackupChoice struct {
	Enabled bool   `json:"enabled"`
	Storage string `json:"storage,omitempty" description:"A storage name on the instance's server; required when enabled."`
}

// CreateBackups is the backups list a new instance starts with: one entry
// per choice, by name. A choice must name a backup the revision declares;
// a backup without one starts off.
func CreateBackups(spec xwinterflow.Spec, choices map[string]BackupChoice) ([]recipe.BackupConfig, error) {
	declared := map[string]bool{}
	for _, r := range spec.Compose.Recipe {
		declared[r.Name] = true
	}
	out := []recipe.BackupConfig{}
	for _, name := range slices.Sorted(maps.Keys(choices)) {
		c := choices[name]
		if !declared[name] {
			return nil, Invalid("backups."+name, "the template declares no backup %q", name)
		}
		if c.Storage != "" {
			if err := model.ValidateStorageName(c.Storage); err != nil {
				return nil, Invalid("backups."+name+".storage", "%v", err)
			}
		} else if c.Enabled {
			return nil, Invalid("backups."+name+".storage", "an enabled backup needs a storage")
		}
		out = append(out, recipe.BackupConfig{Name: name, Enabled: c.Enabled, Storage: c.Storage})
	}
	return out, nil
}
