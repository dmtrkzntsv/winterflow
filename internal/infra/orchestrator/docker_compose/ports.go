package dockercompose

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
	"winterflow/pkg/fsutil"
)

// ErrPortTaken marks a pinned host port another app holds, or one the
// listen probe finds busy: the save is refused (a 400 when the API sees it
// first).
var ErrPortTaken = model.Rejected("port taken")

// portOwner is the app and expose entry holding a host port.
type portOwner struct {
	appID string
	entry string
}

// appSpec parses the x-winterflow block of the compose files the app in
// dir runs (a git-sourced app's included).
func (r *Repository) appSpec(dir string) (xwinterflow.Spec, error) {
	contents, err := composeContents(dir, r.appComposeFiles(dir))
	if err != nil {
		return xwinterflow.Spec{}, fmt.Errorf("read compose files: %w", err)
	}
	return xwinterflow.Parse(xwinterflow.ModeApp, contents...)
}

// readPorts is the ports key of the app's config.json (none without one).
func (r *Repository) readPorts(dir string) (map[string]model.PortAllocation, error) {
	raw, err := os.ReadFile(filepath.Join(dir, configRel))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]model.PortAllocation{}, nil
	}
	if err != nil {
		return nil, err
	}
	ports, _, err := model.ParsePorts(raw)
	return ports, err
}

// pinnedOnly is the pinned subset of ports.
func pinnedOnly(ports map[string]model.PortAllocation) map[string]model.PortAllocation {
	out := map[string]model.PortAllocation{}
	for id, p := range ports {
		if p.Pinned {
			out[id] = p
		}
	}
	return out
}

// loadPortIndexLocked builds the index of every app's host ports from the
// committed config.json files, once per process (file reads, no exec).
// The caller holds portMu.
func (r *Repository) loadPortIndexLocked() {
	if r.portIndex != nil {
		return
	}
	r.portIndex = map[int]portOwner{}
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if err != nil {
		return // no apps yet; an unreadable folder leaves the probe as the guard
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ports, err := r.readPorts(filepath.Join(r.cfg.GetAppsDataDir(), e.Name()))
		if err != nil {
			r.log.Warn("read app ports", "app_id", e.Name(), "error", err)
			continue
		}
		for id, p := range ports {
			r.portIndex[p.Host] = portOwner{appID: e.Name(), entry: id}
		}
	}
}

// syncPorts gives every expose entry of the app in dir its host port and
// stores them in config.json under ports (no key when there are none).
// Pins come first: refused when another app holds the port, or when the
// port is new to the app and the listen probe finds it busy. Then each
// entry keeps its previous port (prev: what the app had before this
// change, or what a rolled-back revision had) unless another app holds it
// now or, new to the app, the probe finds it busy. The rest get the first
// free port of EXPOSE_PORT_RANGE. Entries the compose file no longer has
// lose their port. One server-wide lock serializes allocations and keeps
// the index of every app's ports in step; ports the app already holds are
// never probed (its own containers hold them).
func (r *Repository) syncPorts(appID, dir string, entries []xwinterflow.Expose, prev, pins map[string]model.PortAllocation) error {
	r.portMu.Lock()
	defer r.portMu.Unlock()
	r.loadPortIndexLocked()
	owned := func(host int) bool { o, ok := r.portIndex[host]; return ok && o.appID == appID }
	holder := func(host int) (string, bool) { o, ok := r.portIndex[host]; return o.appID, ok && o.appID != appID }
	next := map[string]model.PortAllocation{}
	used := map[int]bool{}
	for _, e := range entries {
		pin, ok := pins[e.ID()]
		if !ok || !pin.Pinned {
			continue
		}
		if other, taken := holder(pin.Host); taken {
			return fmt.Errorf("%w: %s: port %d is used by the app %s", ErrPortTaken, e.ID(), pin.Host, r.appName(r.appDataDir(other), other))
		}
		if used[pin.Host] {
			return fmt.Errorf("%w: two entries are pinned to port %d", model.ErrPortsInvalid, pin.Host)
		}
		if !owned(pin.Host) {
			if err := r.probePort(e.Bind(), pin.Host, e.Protocol); err != nil {
				return fmt.Errorf("%w: %s: port %d is busy on this server: %v", ErrPortTaken, e.ID(), pin.Host, err)
			}
		}
		next[e.ID()] = model.PortAllocation{Host: pin.Host, Pinned: true}
		used[pin.Host] = true
	}
	for _, e := range entries {
		p, ok := prev[e.ID()]
		if _, done := next[e.ID()]; done || !ok || used[p.Host] {
			continue
		}
		if _, taken := holder(p.Host); taken {
			continue
		}
		if !owned(p.Host) && r.probePort(e.Bind(), p.Host, e.Protocol) != nil {
			continue
		}
		next[e.ID()] = model.PortAllocation{Host: p.Host}
		used[p.Host] = true
	}
	low, high := r.cfg.GetExposePortRange()
	for _, e := range entries {
		if _, done := next[e.ID()]; done {
			continue
		}
		host := 0
		for port := low; port <= high && host == 0; port++ {
			if _, held := r.portIndex[port]; held || used[port] {
				continue
			}
			if r.probePort(e.Bind(), port, e.Protocol) == nil {
				host = port
			}
		}
		if host == 0 {
			return fmt.Errorf("%s: no free port left in EXPOSE_PORT_RANGE %d-%d", e.ID(), low, high)
		}
		next[e.ID()] = model.PortAllocation{Host: host}
		used[host] = true
	}
	if err := r.writePorts(dir, next); err != nil {
		return err
	}
	for host, o := range r.portIndex {
		if o.appID == appID {
			delete(r.portIndex, host)
		}
	}
	for id, p := range next {
		r.portIndex[p.Host] = portOwner{appID: appID, entry: id}
	}
	return nil
}

// writePorts replaces the ports key of the app's config.json (none: the
// key is removed); every other key is kept, an unchanged file not rewritten.
func (r *Repository) writePorts(dir string, ports map[string]model.PortAllocation) error {
	cfg, raw, err := r.readAppConfig(dir)
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		if _, ok := cfg[model.PortsKey]; !ok {
			return nil
		}
		delete(cfg, model.PortsKey)
	} else {
		cfg[model.PortsKey] = ports
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if bytes.Equal(out, raw) {
		return nil
	}
	return fsutil.WriteFile(filepath.Join(dir, configRel), out, 0o644)
}

// forgetPorts drops an app's ports from the index (the app is deleted).
func (r *Repository) forgetPorts(appID string) {
	r.portMu.Lock()
	defer r.portMu.Unlock()
	for host, o := range r.portIndex {
		if o.appID == appID {
			delete(r.portIndex, host)
		}
	}
}

// probeListen reports whether port is free on bind by listening there for
// a moment: a program or a container's docker-proxy already holding it
// makes it fail.
func probeListen(bind string, port int, protocol string) error {
	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	if protocol == xwinterflow.ProtocolUDP {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			return err
		}
		return c.Close()
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return l.Close()
}

// applyExpose checks the ingress of a save against the app's expose
// entries (a hostname may only point at an http one) and syncs its ports.
func (r *Repository) applyExpose(appID, dir string, config []byte, spec xwinterflow.Spec, prev, pins map[string]model.PortAllocation) error {
	ing, err := model.ParseIngress(config)
	if err != nil {
		return fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
	}
	if err := spec.CheckTargets(ing); err != nil {
		return err
	}
	return r.syncPorts(appID, dir, spec.Expose, prev, pins)
}

// restorePorts re-syncs the ports of a revision a rollback just restored
// into dir. On a refusal the worktree goes back to HEAD (with source/ at
// HEAD's lock): nothing of the rollback is committed.
func (r *Repository) restorePorts(appID, dir string) error {
	spec, err := r.appSpec(dir)
	if err == nil {
		var restored map[string]model.PortAllocation
		if restored, err = r.readPorts(dir); err == nil {
			err = r.syncPorts(appID, dir, spec.Expose, restored, pinnedOnly(restored))
		}
	}
	if err == nil {
		return nil
	}
	if head, herr := gitHead(dir); herr == nil {
		if rerr := gitRestore(dir, head); rerr != nil {
			r.log.Warn("undo a refused rollback", "dir", dir, "error", rerr)
		}
		r.restoreSource(appID, dir)
	}
	return fmt.Errorf("rollback: %w", err)
}
