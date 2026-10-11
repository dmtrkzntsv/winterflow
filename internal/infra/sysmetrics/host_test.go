package sysmetrics

import (
	"errors"
	"testing"
	"time"
)

const procStat1 = "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 50 0 50 350 50 0 0 0 0 0\nintr 1 2 3\n"

// +100 busy (user 60, system 40) and +100 idle (idle 80, iowait 20): 50 %.
const procStat2 = "cpu  160 0 140 780 120 0 0 0 0 0\ncpu0 80 0 70 390 60 0 0 0 0 0\n"

const meminfo = `MemTotal:        8000000 kB
MemFree:          500000 kB
MemAvailable:    6000000 kB
Buffers:          100000 kB
Cached:          2000000 kB
SwapTotal:       1000000 kB
SwapFree:         750000 kB
`

const diskstats1 = `   7       0 loop0 11 0 9999 1 0 0 9999 0 0 2 1 0 0 0 0 0 0
 259       0 nvme0n1 100 0 1000 10 50 0 2000 5 0 0 0 0 0 0 0 0 0
 259       1 nvme0n1p1 90 0 900 10 40 0 1800 5 0 0 0 0 0 0 0 0 0
 252       0 zram0 5 0 4000 0 5 0 4000 0 0 0 0 0 0 0 0 0 0
 253       0 dm-0 5 0 4000 0 5 0 4000 0 0 0 0 0 0 0 0 0 0
`

// nvme0n1 +120 sectors read, +240 written over 60 s: 1024 B/s and 2048 B/s.
const diskstats2 = `   7       0 loop0 11 0 99999 1 0 0 99999 0 0 2 1 0 0 0 0 0 0
 259       0 nvme0n1 100 0 1120 10 50 0 2240 5 0 0 0 0 0 0 0 0 0
 259       1 nvme0n1p1 90 0 1020 10 40 0 2040 5 0 0 0 0 0 0 0 0 0
 252       0 zram0 5 0 8000 0 5 0 8000 0 0 0 0 0 0 0 0 0 0
 253       0 dm-0 5 0 8000 0 5 0 8000 0 0 0 0 0 0 0 0 0 0
`

const netdevHeader = "Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"

const netdev1 = netdevHeader +
	"    lo: 5000 1 0 0 0 0 0 0 5000 1 0 0 0 0 0 0\n" +
	"  eth0: 1000 1 0 0 0 0 0 0 2000 1 0 0 0 0 0 0\n" +
	"docker0: 7000 1 0 0 0 0 0 0 7000 1 0 0 0 0 0 0\n" +
	"veth1a2b: 7000 1 0 0 0 0 0 0 7000 1 0 0 0 0 0 0\n" +
	"br-0f3e: 7000 1 0 0 0 0 0 0 7000 1 0 0 0 0 0 0\n"

// eth0 +6000 rx, +12000 tx over 60 s; the excluded interfaces move a lot.
const netdev2 = netdevHeader +
	"    lo: 905000 1 0 0 0 0 0 0 905000 1 0 0 0 0 0 0\n" +
	"  eth0: 7000 1 0 0 0 0 0 0 14000 1 0 0 0 0 0 0\n" +
	"docker0: 907000 1 0 0 0 0 0 0 907000 1 0 0 0 0 0 0\n" +
	"veth1a2b: 907000 1 0 0 0 0 0 0 907000 1 0 0 0 0 0 0\n" +
	"br-0f3e: 907000 1 0 0 0 0 0 0 907000 1 0 0 0 0 0 0\n"

func hostFixture(t *testing.T) string {
	return tree(t, map[string]string{
		"proc/stat":                          procStat1,
		"proc/meminfo":                       meminfo,
		"proc/loadavg":                       "0.52 0.41 0.30 2/345 6789\n",
		"proc/diskstats":                     diskstats1,
		"proc/net/dev":                       netdev1,
		"sys/block/nvme0n1/":                 "",
		"sys/block/loop0/":                   "",
		"sys/block/zram0/":                   "",
		"sys/block/dm-0/":                    "",
		"sys/class/hwmon/hwmon0/name":        "acpitz\n",
		"sys/class/hwmon/hwmon0/temp1_input": "27800\n",
		"sys/class/hwmon/hwmon1/name":        "coretemp\n",
		"sys/class/hwmon/hwmon1/temp1_input": "55000\n",
		"sys/class/hwmon/hwmon1/temp1_label": "Package id 0\n",
		"sys/class/hwmon/hwmon1/temp2_input": "0\n",
		"sys/class/hwmon/hwmon2/name":        "acpitz\n",
		"sys/class/hwmon/hwmon2/temp1_input": "30000\n",
	})
}

func fakeStatFS(m map[string]fsInfo) func(string) (fsInfo, error) {
	return func(p string) (fsInfo, error) {
		fi, ok := m[p]
		if !ok {
			return fsInfo{}, errors.New("no such mount")
		}
		return fi, nil
	}
}

func TestHostFirstSampleHasNoRates(t *testing.T) {
	h := NewHost(hostFixture(t), "/", "/srv/data")
	h.statFS = fakeStatFS(map[string]fsInfo{
		"/":         {dev: 1, used: 40, total: 100},
		"/srv/data": {dev: 1, used: 40, total: 100}, // same filesystem: deduped
	})
	s := h.Collect(time.Unix(1000, 0))
	if s.CPU != nil || s.DiskReadBps != nil || s.NetRxBps != nil {
		t.Fatalf("first sample carries rates: %+v", s)
	}
	if s.MemTotal != 8000000*1024 || s.MemUsed != 2000000*1024 {
		t.Fatalf("mem = %v / %v", s.MemUsed, s.MemTotal)
	}
	if s.SwapTotal != 1000000*1024 || s.SwapUsed != 250000*1024 {
		t.Fatalf("swap = %v / %v", s.SwapUsed, s.SwapTotal)
	}
	if s.Load1 != 0.52 || s.Load5 != 0.41 || s.Load15 != 0.30 {
		t.Fatalf("load = %v %v %v", s.Load1, s.Load5, s.Load15)
	}
	if len(s.Disks) != 1 || s.Disks[0].Mount != "/" || s.Disks[0].Used != 40 {
		t.Fatalf("disks = %+v", s.Disks)
	}
	if s.Temp == nil || *s.Temp != 55 {
		t.Fatalf("temp = %v", s.Temp)
	}
	want := map[string]float64{"acpitz": 27.8, "Package id 0": 55, "acpitz #2": 30}
	if len(s.Temps) != len(want) {
		t.Fatalf("temps = %v", s.Temps)
	}
	for k, v := range want {
		if s.Temps[k] != v {
			t.Fatalf("temps = %v, want %v", s.Temps, want)
		}
	}
}

func TestHostRatesAgainstPreviousSample(t *testing.T) {
	root := hostFixture(t)
	h := NewHost(root, "/")
	h.statFS = fakeStatFS(nil)
	h.Collect(time.Unix(1000, 0))
	write(t, root, map[string]string{"proc/stat": procStat2, "proc/diskstats": diskstats2, "proc/net/dev": netdev2})
	s := h.Collect(time.Unix(1060, 0))
	if s.CPU == nil || *s.CPU != 50 {
		t.Fatalf("cpu = %v", s.CPU)
	}
	if s.DiskReadBps == nil || *s.DiskReadBps != 1024 || *s.DiskWriteBps != 2048 {
		t.Fatalf("disk io = %v %v (partitions, loop, zram, dm must not count)", s.DiskReadBps, s.DiskWriteBps)
	}
	if s.NetRxBps == nil || *s.NetRxBps != 100 || *s.NetTxBps != 200 {
		t.Fatalf("net = %v %v (lo, docker0, veth, br- must not count)", s.NetRxBps, s.NetTxBps)
	}
	if len(s.Disks) != 0 {
		t.Fatalf("unmeasurable mount reported: %+v", s.Disks)
	}
}

func TestHostCounterResetDropsTheInterval(t *testing.T) {
	root := hostFixture(t)
	h := NewHost(root, "/")
	h.statFS = fakeStatFS(nil)
	h.Collect(time.Unix(1000, 0))
	write(t, root, map[string]string{"proc/stat": procStat2, "proc/diskstats": diskstats2, "proc/net/dev": netdev2})
	h.Collect(time.Unix(1060, 0))
	// Counters back at their old values (e.g. an interface re-created).
	write(t, root, map[string]string{"proc/stat": procStat1, "proc/diskstats": diskstats1, "proc/net/dev": netdev1})
	s := h.Collect(time.Unix(1120, 0))
	if s.CPU != nil || s.DiskReadBps != nil || s.NetRxBps != nil || s.NetTxBps != nil {
		t.Fatalf("backwards counters produced rates: %+v", s)
	}
	// The next interval measures again from the reset values.
	write(t, root, map[string]string{"proc/stat": procStat2})
	if s := h.Collect(time.Unix(1180, 0)); s.CPU == nil || *s.CPU != 50 {
		t.Fatalf("cpu after reset = %v", s.CPU)
	}
}

func TestHostWithoutSensorsOrFiles(t *testing.T) {
	h := NewHost(t.TempDir(), "/")
	h.statFS = fakeStatFS(nil)
	s := h.Collect(time.Unix(1000, 0))
	if s.Temp != nil || s.Temps != nil {
		t.Fatalf("no hwmon must mean no temperature: %+v", s)
	}
	if s.MemTotal != 0 || s.Disks == nil {
		t.Fatalf("empty root sample = %+v", s)
	}
	// A second sample over the same empty root: nothing to compare, nothing
	// to report, and no panic on the missing previous counters.
	s = h.Collect(time.Unix(1060, 0))
	if s.CPU != nil || s.DiskReadBps != nil || s.DiskWriteBps != nil || s.NetRxBps != nil || s.NetTxBps != nil {
		t.Fatalf("empty root produced rates: %+v", s)
	}
	if s.Temp != nil || s.Temps != nil {
		t.Fatalf("empty root produced temperatures: %+v", s)
	}
	if s.MemTotal != 0 || s.MemUsed != 0 || s.SwapTotal != 0 || s.Load1 != 0 {
		t.Fatalf("empty root produced gauges: %+v", s)
	}
	if s.Disks == nil || len(s.Disks) != 0 {
		t.Fatalf("empty root disks = %#v, want empty non-nil", s.Disks)
	}
}

func TestParseMeminfoWithoutMemAvailable(t *testing.T) {
	m, err := parseMeminfo([]byte("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 250 kB\n"))
	if err != nil || m.available != 400*1024 || m.swapTotal != 0 {
		t.Fatalf("meminfo = %+v, %v", m, err)
	}
	if _, err := parseMeminfo([]byte("garbage")); err == nil {
		t.Fatal("meminfo without MemTotal accepted")
	}
}

func TestParseProcStatErrors(t *testing.T) {
	if _, err := parseProcStat([]byte("intr 1\n")); err == nil {
		t.Fatal("missing cpu line accepted")
	}
	if _, err := parseProcStat([]byte("cpu  x 0 0 0 0\n")); err == nil {
		t.Fatal("non-numeric field accepted")
	}
}

func TestParseLoadavgShort(t *testing.T) {
	if _, _, _, err := parseLoadavg([]byte("0.1 0.2")); err == nil {
		t.Fatal("short loadavg accepted")
	}
}

// md RAID arrays repeat their members' I/O; nbd and optical drives are not
// local disks. None of them may count towards host disk I/O.
func TestVirtualDisk(t *testing.T) {
	for _, n := range []string{"loop0", "ram1", "dm-0", "zram0", "md0", "md127", "nbd3", "sr0"} {
		if !virtualDisk(n) {
			t.Errorf("%s counted as a physical disk", n)
		}
	}
	for _, n := range []string{"sda", "nvme0n1", "mmcblk0", "vda", "xvda"} {
		if virtualDisk(n) {
			t.Errorf("%s excluded", n)
		}
	}
}
