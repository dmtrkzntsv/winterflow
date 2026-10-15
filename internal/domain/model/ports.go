package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PortsKey is the key of an app's committed config.json holding the host
// port of each x-winterflow expose entry, keyed by the entry's id
// (<container>:<port>/<protocol>).
const PortsKey = "ports"

// PortAllocation is the host port of one expose entry. Pinned ports are the
// user's choice; the agent allocated the others from EXPOSE_PORT_RANGE.
type PortAllocation struct {
	Host   int  `json:"host"`
	Pinned bool `json:"pinned"`
}

// ErrPortsInvalid marks a ports map that can't be used (a 400).
var ErrPortsInvalid = rejected("invalid ports")

// ParsePorts reads the ports key of an app config blob. present reports
// whether the blob has the key at all: a save without it keeps the stored
// pins (an editor that doesn't know about ports must not drop them).
func ParsePorts(config []byte) (ports map[string]PortAllocation, present bool, err error) {
	var probe struct {
		Ports *map[string]PortAllocation `json:"ports"`
	}
	if err := json.Unmarshal(config, &probe); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrPortsInvalid, err)
	}
	if probe.Ports == nil {
		return map[string]PortAllocation{}, false, nil
	}
	for id, p := range *probe.Ports {
		if p.Host < 1 || p.Host > 65535 {
			return nil, true, fmt.Errorf("%w: %s: %d is not a port from 1 to 65535", ErrPortsInvalid, id, p.Host)
		}
	}
	return *probe.Ports, true, nil
}

// HTTPPorts maps each http entry's ingress target (<container>:<port>) to
// its host port.
func HTTPPorts(ports map[string]PortAllocation) map[string]int {
	out := map[string]int{}
	for id, p := range ports {
		if target, ok := strings.CutSuffix(id, "/http"); ok {
			out[target] = p.Host
		}
	}
	return out
}
