package sysmetrics

import (
	"testing"
	"time"
)

const (
	ctrA = "aaaa1111"
	ctrB = "bbbb2222"
)

func netdev(rx, tx string) string {
	return netdevHeader +
		"    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n" +
		"  eth0: " + rx + " 1 0 0 0 0 0 0 " + tx + " 1 0 0 0 0 0 0\n"
}

// v2 systemd driver: the dev host's layout.
func v2Systemd(usage, rx, tx string) map[string]string {
	dir := "sys/fs/cgroup/system.slice/docker-" + ctrA + ".scope/"
	return map[string]string{
		dir + "cpu.stat":       "usage_usec " + usage + "\nuser_usec 1\nsystem_usec 1\n",
		dir + "memory.current": "1040384\n",
		dir + "memory.stat":    "anon 1\nfile 40960\nactive_file 0\ninactive_file 40960\n",
		dir + "memory.max":     "max\n",
		dir + "cgroup.procs":   "378136\n378310\n",
		"proc/378136/net/dev":  netdev(rx, tx),
	}
}

func TestContainersV2SystemdDriver(t *testing.T) {
	root := tree(t, v2Systemd("1000000", "1000", "2000"))
	c := NewContainers(root, 4)
	got := c.Collect(time.Unix(1000, 0), []string{ctrA})
	st, ok := got[ctrA]
	if !ok {
		t.Fatal("v2 systemd layout not resolved")
	}
	if st.CPU != nil || st.NetRx != nil {
		t.Fatalf("first reading has rates: %+v", st)
	}
	if st.MemUsed != 1040384-40960 || st.MemLimit != nil {
		t.Fatalf("mem = %v limit = %v", st.MemUsed, st.MemLimit)
	}
	// +60 s of CPU time over 60 s on 4 cores = 25 % of the host.
	write(t, root, v2Systemd("61000000", "7000", "14000"))
	st = c.Collect(time.Unix(1060, 0), []string{ctrA})[ctrA]
	if st.CPU == nil || *st.CPU != 25 {
		t.Fatalf("cpu = %v", st.CPU)
	}
	if st.NetRx == nil || *st.NetRx != 100 || *st.NetTx != 200 {
		t.Fatalf("net = %v %v (lo excluded)", st.NetRx, st.NetTx)
	}
}

func TestContainersV2CgroupfsDriverWithLimit(t *testing.T) {
	dir := "sys/fs/cgroup/docker/" + ctrB + "/"
	root := tree(t, map[string]string{
		dir + "cpu.stat":       "usage_usec 500\n",
		dir + "memory.current": "2000\n",
		dir + "memory.stat":    "inactive_file 500\n",
		dir + "memory.max":     "268435456\n",
	})
	st, ok := NewContainers(root, 2).Collect(time.Unix(1000, 0), []string{ctrB})[ctrB]
	if !ok || st.MemUsed != 1500 || st.MemLimit == nil || *st.MemLimit != 268435456 {
		t.Fatalf("cgroupfs stat = %+v, %v", st, ok)
	}
	if st.NetRx != nil {
		t.Fatal("no cgroup.procs must mean no net")
	}
}

func TestContainersV1Fallback(t *testing.T) {
	sub := "docker/" + ctrA + "/"
	root := tree(t, map[string]string{
		"sys/fs/cgroup/cpu,cpuacct/" + sub + "cpuacct.usage":    "2000000000\n",
		"sys/fs/cgroup/memory/" + sub + "memory.usage_in_bytes": "5000\n",
		"sys/fs/cgroup/memory/" + sub + "memory.stat":           "cache 9\ntotal_inactive_file 1000\n",
		"sys/fs/cgroup/memory/" + sub + "memory.limit_in_bytes": "9223372036854771712\n",
	})
	c := NewContainers(root, 1)
	st, ok := c.Collect(time.Unix(1000, 0), []string{ctrA})[ctrA]
	if !ok || st.MemUsed != 4000 || st.MemLimit != nil {
		t.Fatalf("v1 stat = %+v, %v (unlimited must be nil)", st, ok)
	}
	write(t, root, map[string]string{"sys/fs/cgroup/cpu,cpuacct/" + sub + "cpuacct.usage": "32000000000\n"})
	st = c.Collect(time.Unix(1060, 0), []string{ctrA})[ctrA]
	if st.CPU == nil || *st.CPU != 50 { // 30 s of CPU in 60 s on one core
		t.Fatalf("v1 cpu = %v", st.CPU)
	}
}

func TestContainersRestartResetsCounters(t *testing.T) {
	root := tree(t, v2Systemd("90000000", "50000", "50000"))
	c := NewContainers(root, 1)
	c.Collect(time.Unix(1000, 0), []string{ctrA})
	// Restarted: a fresh cgroup (usage starts over) and a new pid/netns.
	files := v2Systemd("1000", "10", "10")
	files["sys/fs/cgroup/system.slice/docker-"+ctrA+".scope/cgroup.procs"] = "400000\n"
	files["proc/400000/net/dev"] = netdev("10", "10")
	write(t, root, files)
	st := c.Collect(time.Unix(1060, 0), []string{ctrA})[ctrA]
	if st.CPU != nil || st.NetRx != nil || st.NetTx != nil {
		t.Fatalf("restart produced rates: %+v", st)
	}
	files = v2Systemd("6001000", "6010", "6010")
	files["sys/fs/cgroup/system.slice/docker-"+ctrA+".scope/cgroup.procs"] = "400000\n"
	files["proc/400000/net/dev"] = netdev("6010", "6010")
	write(t, root, files)
	st = c.Collect(time.Unix(1120, 0), []string{ctrA})[ctrA]
	if st.CPU == nil || *st.CPU != 10 || st.NetRx == nil || *st.NetRx != 100 {
		t.Fatalf("after restart = %+v", st)
	}
}

func TestContainersHostNetworkHasNoNet(t *testing.T) {
	files := v2Systemd("1", "1", "1")
	files["proc/378136/net/dev"] = netdevHeader +
		"  eth0: 1 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0\n" +
		"docker0: 1 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0\n"
	root := tree(t, files)
	c := NewContainers(root, 1)
	c.Collect(time.Unix(1000, 0), []string{ctrA})
	st := c.Collect(time.Unix(1060, 0), []string{ctrA})[ctrA]
	if st.NetRx != nil || st.NetTx != nil {
		t.Fatalf("host-netns container reported net: %+v", st)
	}
}

func TestContainersUnknownLayoutAndPruning(t *testing.T) {
	root := tree(t, v2Systemd("1", "1", "1"))
	c := NewContainers(root, 1)
	got := c.Collect(time.Unix(1000, 0), []string{ctrA, "unknown"})
	if _, ok := got["unknown"]; ok || len(got) != 1 {
		t.Fatalf("unresolvable container reported: %v", got)
	}
	c.Collect(time.Unix(1060, 0), nil)
	if len(c.prev) != 0 {
		t.Fatalf("delta state kept for vanished containers: %v", c.prev)
	}
}
