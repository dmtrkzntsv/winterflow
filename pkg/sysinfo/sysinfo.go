// Package sysinfo collects the host facts the agent reports as capabilities
// (cpu cores, total memory, total disk, outbound IP). Ported from the v1
// agent's collectors; stdlib only. Collectors degrade to "" on unsupported
// platforms or errors — a missing capability simply isn't displayed.
package sysinfo

import (
	"bufio"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// CPUCores returns the number of logical CPU cores.
func CPUCores() string {
	return strconv.Itoa(runtime.NumCPU())
}

// MemoryTotalBytes returns total physical memory in bytes, from /proc/meminfo
// (linux only; "" elsewhere or on failure).
func MemoryTotalBytes() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line) // "MemTotal:", "16384000", "kB"
		if len(fields) < 2 {
			return ""
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return ""
		}
		return strconv.FormatUint(kb*1024, 10)
	}
	return ""
}

// ServerIP returns the host's outbound IP address: the local address the
// kernel picks for a route to a public destination. No packets are sent (UDP
// dial only resolves the route). Empty when no route is available.
func ServerIP() string {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}
