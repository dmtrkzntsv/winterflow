package recipe

import (
	"path"
	"regexp"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// Mount is one mount of a service as its compose file writes it: a named
// volume (Source is the volume's key under volumes:) or a bind mount
// (Source is the host path as written, ./media or /srv/media). Anonymous
// volumes and tmpfs mounts hold nothing a backup can keep and are left out.
type Mount struct {
	Type   string `json:"type"` // volume | bind
	Source string `json:"source"`
	Target string `json:"target"`
}

// ServiceMounts is a service and its mounts, by target.
type ServiceMounts struct {
	Name   string  `json:"name"`
	Mounts []Mount `json:"mounts"`
}

// Targets is where a backup added in the UI can point: the compose files'
// volumes (the volume form) and each service's volume and bind mounts (the
// service form). Mount resolution at save checks the choice again.
type Targets struct {
	Volumes  []string        `json:"volumes"`
	Services []ServiceMounts `json:"services"`
}

// Targets lists the volumes and the services' mounts, sorted by name and
// target ([] for none).
func (c Compose) Targets() Targets {
	t := Targets{Volumes: []string{}, Services: []ServiceMounts{}}
	for v := range c.Volumes {
		t.Volumes = append(t.Volumes, v)
	}
	sort.Strings(t.Volumes)
	names := make([]string, 0, len(c.Services))
	for name := range c.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mounts := append([]Mount{}, c.Mounts[name]...)
		sort.Slice(mounts, func(i, j int) bool { return mounts[i].Target < mounts[j].Target })
		t.Services = append(t.Services, ServiceMounts{Name: name, Mounts: mounts})
	}
	return t
}

// volumeKeyRE is a volume key in the short syntax.
var volumeKeyRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// parseMount reads one entry of a service's volumes: the short syntax
// (source:target[:mode]) or the long one (type, source, target). ok is false
// for what a backup can't use (an anonymous volume, tmpfs, an unreadable
// entry).
func parseMount(n *yaml.Node) (Mount, bool) {
	var m Mount
	switch n.Kind {
	case yaml.ScalarNode:
		parts := strings.Split(n.Value, ":")
		if len(parts) < 2 || parts[0] == "" {
			return Mount{}, false
		}
		m.Source, m.Target = parts[0], parts[1]
		// As compose reads it: a name is a volume, anything else (./x, /x,
		// ~/x, an interpolated path) a bind mount.
		m.Type = "volume"
		if !volumeKeyRE.MatchString(m.Source) {
			m.Type = "bind"
		}
	case yaml.MappingNode:
		var long struct {
			Type   string `yaml:"type"`
			Source string `yaml:"source"`
			Target string `yaml:"target"`
		}
		if n.Decode(&long) != nil || long.Source == "" || (long.Type != "volume" && long.Type != "bind") {
			return Mount{}, false
		}
		m = Mount{Type: long.Type, Source: long.Source, Target: long.Target}
	default:
		return Mount{}, false
	}
	if !strings.HasPrefix(m.Target, "/") {
		return Mount{}, false
	}
	m.Target = path.Clean(m.Target)
	return m, true
}

// addMount adds m to a service's mounts; a later compose file's mount at the
// same target replaces the earlier one, as compose merges them.
func addMount(mounts []Mount, m Mount) []Mount {
	for i := range mounts {
		if mounts[i].Target == m.Target {
			mounts[i] = m
			return mounts
		}
	}
	return append(mounts, m)
}
