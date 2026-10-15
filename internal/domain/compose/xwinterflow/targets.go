package xwinterflow

import (
	"fmt"
	"sort"

	"winterflow/internal/domain/model"
)

// CheckTargets refuses an ingress hostname whose expose target is not an
// http expose entry of the app (tcp/udp entries never take hostnames).
func (s Spec) CheckTargets(ing *model.Ingress) error {
	if ing == nil {
		return nil
	}
	targets := s.HTTPTargets()
	for _, d := range ing.Domains {
		if d.Expose != "" && !targets[d.Expose] {
			return fmt.Errorf("%w: domain %q: %s is not an http expose entry of this app", model.ErrIngressInvalid, d.Domain, d.Expose)
		}
	}
	return nil
}

// CheckPins refuses pins the app can't take: one for an id that is no
// expose entry of it, or two entries pinned to the same host port.
func (s Spec) CheckPins(ports map[string]model.PortAllocation) error {
	ids := map[string]bool{}
	for _, e := range s.Expose {
		ids[e.ID()] = true
	}
	keys := make([]string, 0, len(ports))
	for id := range ports {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	byHost := map[int]string{}
	for _, id := range keys {
		p := ports[id]
		if !p.Pinned {
			continue
		}
		if !ids[id] {
			return fmt.Errorf("%w: %s is not an expose entry of this app", model.ErrPortsInvalid, id)
		}
		if other, dup := byHost[p.Host]; dup {
			return fmt.Errorf("%w: %s and %s are both pinned to port %d", model.ErrPortsInvalid, other, id, p.Host)
		}
		byHost[p.Host] = id
	}
	return nil
}
