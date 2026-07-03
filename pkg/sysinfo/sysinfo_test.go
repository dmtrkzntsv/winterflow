package sysinfo

import (
	"net"
	"runtime"
	"strconv"
	"testing"
)

func TestCPUCores(t *testing.T) {
	n, err := strconv.Atoi(CPUCores())
	if err != nil || n < 1 {
		t.Fatalf("CPUCores() = %q, want a positive integer", CPUCores())
	}
}

func TestMemoryTotalBytes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only collector")
	}
	v := MemoryTotalBytes()
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n < 1<<20 {
		t.Fatalf("MemoryTotalBytes() = %q, want > 1MiB worth of bytes", v)
	}
}

func TestDiskTotalBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("statfs is unix-only")
	}
	v := DiskTotalBytes("/")
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n == 0 {
		t.Fatalf("DiskTotalBytes(/) = %q, want a positive byte count", v)
	}
	if got := DiskTotalBytes("/definitely-not-a-mountpoint-xyz"); got != "" {
		t.Fatalf("bad path should yield empty, got %q", got)
	}
}

func TestServerIP(t *testing.T) {
	// May legitimately be empty in a network-less sandbox; when non-empty it
	// must parse as an IP.
	v := ServerIP()
	if v == "" {
		t.Skip("no outbound route available")
	}
	if net.ParseIP(v) == nil {
		t.Fatalf("ServerIP() = %q, not a valid IP", v)
	}
}
