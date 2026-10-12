// Package sysmetrics reads host and container resource usage straight from
// procfs, sysfs and cgroupfs: file reads only, no process forks, so it is
// cheap enough to run every minute forever on a small box. Every path is
// resolved under an injectable root ("/" in production) so tests run against
// fixture trees.
package sysmetrics

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"
)

// cpuTimes is the aggregate line of /proc/stat in USER_HZ ticks.
type cpuTimes struct {
	total, idle uint64
}

// parseProcStat reads the aggregate "cpu" line. total is user+nice+system+
// idle+iowait+irq+softirq+steal (guest time is already inside user); idle
// includes iowait.
func parseProcStat(b []byte) (cpuTimes, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var vals [8]uint64
		for i := 0; i < 8 && i+1 < len(fields); i++ {
			v, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, err
			}
			vals[i] = v
		}
		var t cpuTimes
		for _, v := range vals {
			t.total += v
		}
		t.idle = vals[3] + vals[4]
		return t, nil
	}
	return cpuTimes{}, errors.New("proc/stat: no aggregate cpu line")
}

// memInfo is /proc/meminfo in bytes.
type memInfo struct {
	total, available, swapTotal, swapFree uint64
}

// parseMeminfo reads the fields the sample needs. Kernels older than 3.14
// lack MemAvailable; MemFree+Buffers+Cached stands in for it there.
func parseMeminfo(b []byte) (memInfo, error) {
	kv := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 1 && fields[1] == "kB" {
			v *= 1024
		}
		kv[key] = v
	}
	total, ok := kv["MemTotal"]
	if !ok {
		return memInfo{}, errors.New("meminfo: no MemTotal")
	}
	avail, ok := kv["MemAvailable"]
	if !ok {
		avail = kv["MemFree"] + kv["Buffers"] + kv["Cached"]
	}
	if avail > total {
		avail = total
	}
	free := kv["SwapFree"]
	if free > kv["SwapTotal"] {
		free = kv["SwapTotal"]
	}
	return memInfo{total: total, available: avail, swapTotal: kv["SwapTotal"], swapFree: free}, nil
}

// parseLoadavg reads the three load averages.
func parseLoadavg(b []byte) (l1, l5, l15 float64, err error) {
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return 0, 0, 0, errors.New("loadavg: short line")
	}
	var out [3]float64
	for i := range out {
		if out[i], err = strconv.ParseFloat(f[i], 64); err != nil {
			return 0, 0, 0, err
		}
	}
	return out[0], out[1], out[2], nil
}

// sectorSize is the unit of /proc/diskstats sector counts, fixed by the
// kernel regardless of the device's real sector size.
const sectorSize = 512

// parseDiskstats sums sectors read and written over the devices whole()
// accepts and returns bytes.
func parseDiskstats(b []byte, whole func(name string) bool) (read, written uint64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || !whole(f[2]) {
			continue
		}
		r, err1 := strconv.ParseUint(f[5], 10, 64)
		w, err2 := strconv.ParseUint(f[9], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		read += r * sectorSize
		written += w * sectorSize
	}
	return read, written
}

// virtualDisk reports device names that are never physical disks (md RAID
// arrays would double-count their members' I/O).
func virtualDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "dm-", "zram", "md", "nbd", "sr"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// netDev is one interface's byte counters from a net/dev file.
type netDev struct {
	name   string
	rx, tx uint64
}

// parseNetDev reads every interface line of a /proc/<pid>/net/dev file.
func parseNetDev(b []byte) []netDev {
	var out []netDev
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, netDev{name: strings.TrimSpace(name), rx: rx, tx: tx})
	}
	return out
}

// hostInterface reports whether a host interface counts toward host network
// traffic: loopback and Docker's virtual plumbing would double-count
// container traffic.
func hostInterface(name string) bool {
	if name == "lo" {
		return false
	}
	for _, p := range []string{"veth", "docker", "br-"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// hostNetns reports whether a container's net/dev shows the host's network
// namespace (network_mode: host): only the host has Docker's bridge and veth
// ends. The agent cannot readlink another user's /proc/<pid>/ns/net, so the
// interface names are the signal.
func hostNetns(devs []netDev) bool {
	for _, d := range devs {
		if d.name == "docker0" || strings.HasPrefix(d.name, "veth") || strings.HasPrefix(d.name, "br-") {
			return true
		}
	}
	return false
}

// sumNet adds the counters of the interfaces keep accepts.
func sumNet(devs []netDev, keep func(string) bool) (rx, tx uint64) {
	for _, d := range devs {
		if keep(d.name) {
			rx += d.rx
			tx += d.tx
		}
	}
	return rx, tx
}
