package sysmetrics

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"winterflow/internal/domain/metrics"
)

// fsInfo is one filesystem from statfs.
type fsInfo struct {
	dev         uint64
	used, total uint64
}

// hostCounters are the cumulative counters rates are computed from.
type hostCounters struct {
	at                         time.Time
	cpu                        cpuTimes
	diskRead, diskWrite        uint64
	netRx, netTx               uint64
	haveCPU, haveDisk, haveNet bool
}

// Host collects the host part of a sample. Not safe for concurrent use: one
// reporter goroutine owns it.
type Host struct {
	root   string
	mounts []string
	statFS func(path string) (fsInfo, error)
	prev   *hostCounters
}

// NewHost reads procfs/sysfs under root and measures the filesystems of the
// given mount paths (the root filesystem and the agent's data dir).
func NewHost(root string, mounts ...string) *Host {
	return &Host{root: root, mounts: mounts, statFS: statFS}
}

func (h *Host) read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(h.root, rel))
}

// Collect takes one host sample. Rates compare against the previous call;
// the first call, and any counter that went backwards (reboot, wrap, a
// device vanishing), leaves that rate nil for this interval.
func (h *Host) Collect(now time.Time) metrics.ServerSample {
	var s metrics.ServerSample
	cur := hostCounters{at: now}

	if b, err := h.read("proc/stat"); err == nil {
		if t, err := parseProcStat(b); err == nil {
			cur.cpu, cur.haveCPU = t, true
		}
	}
	if b, err := h.read("proc/meminfo"); err == nil {
		if m, err := parseMeminfo(b); err == nil {
			s.MemTotal = float64(m.total)
			s.MemUsed = float64(m.total - m.available)
			s.SwapTotal = float64(m.swapTotal)
			s.SwapUsed = float64(m.swapTotal - m.swapFree)
		}
	}
	if b, err := h.read("proc/loadavg"); err == nil {
		s.Load1, s.Load5, s.Load15, _ = parseLoadavg(b)
	}
	if b, err := h.read("proc/diskstats"); err == nil {
		cur.diskRead, cur.diskWrite = parseDiskstats(b, h.wholeDisk)
		cur.haveDisk = true
	}
	if b, err := h.read("proc/net/dev"); err == nil {
		cur.netRx, cur.netTx = sumNet(parseNetDev(b), hostInterface)
		cur.haveNet = true
	}
	s.Disks = h.disks()
	if temps, hottest, ok := readHwmon(h.root); ok {
		s.Temps = temps
		s.Temp = &hottest
	}

	if p := h.prev; p != nil {
		secs := now.Sub(p.at).Seconds()
		if secs > 0 {
			if cur.haveCPU && p.haveCPU && cur.cpu.total > p.cpu.total && cur.cpu.idle >= p.cpu.idle {
				dt := float64(cur.cpu.total - p.cpu.total)
				busy := dt - float64(cur.cpu.idle-p.cpu.idle)
				if busy < 0 {
					busy = 0
				}
				pct := 100 * busy / dt
				s.CPU = &pct
			}
			if cur.haveDisk && p.haveDisk {
				s.DiskReadBps = rate(p.diskRead, cur.diskRead, secs)
				s.DiskWriteBps = rate(p.diskWrite, cur.diskWrite, secs)
			}
			if cur.haveNet && p.haveNet {
				s.NetRxBps = rate(p.netRx, cur.netRx, secs)
				s.NetTxBps = rate(p.netTx, cur.netTx, secs)
			}
		}
	}
	h.prev = &cur
	return s
}

// rate is (cur-prev)/secs, or nil when the counter went backwards.
func rate(prev, cur uint64, secs float64) *float64 {
	if cur < prev || secs <= 0 {
		return nil
	}
	r := float64(cur-prev) / secs
	return &r
}

// wholeDisk accepts physical whole disks (isWholeDisk). diskstats writes
// "/" in names that sysfs spells "!" (cciss/c0d0).
func (h *Host) wholeDisk(name string) bool {
	return isWholeDisk(h.root, strings.ReplaceAll(name, "/", "!"))
}

// isWholeDisk accepts a physical whole disk by its sysfs name: listed in
// root/sys/block (partitions are not) and not a loop/ram/dm/zram/md device.
func isWholeDisk(root, sysName string) bool {
	if virtualDisk(sysName) {
		return false
	}
	_, err := os.Stat(filepath.Join(root, "sys", "block", sysName))
	return err == nil
}

// disks measures the configured mount paths, one entry per filesystem.
func (h *Host) disks() []metrics.Disk {
	out := []metrics.Disk{}
	seen := map[uint64]bool{}
	for _, m := range h.mounts {
		fi, err := h.statFS(m)
		if err != nil || fi.total == 0 || seen[fi.dev] {
			continue
		}
		seen[fi.dev] = true
		out = append(out, metrics.Disk{Mount: m, Used: float64(fi.used), Total: float64(fi.total)})
	}
	return out
}
