package agent

import (
	"os"
	"runtime"

	"winterflow/pkg/sysinfo"
	"winterflow/pkg/version"
)

// HostCapabilities collects the host facts every agent reports, regardless of
// topology: platform identity plus hardware specs and the outbound IP.
// diskPath anchors the disk measurement (the agent's data dir mount). Empty
// values are omitted — a capability the host can't provide isn't displayed.
func HostCapabilities(diskPath string) map[string]string {
	caps := map[string]string{
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
		"version": version.GetVersion(),
	}
	if hostname, err := os.Hostname(); err == nil {
		caps["hostname"] = hostname
	}
	for name, value := range map[string]string{
		"server_ip":           sysinfo.ServerIP(),
		"system_cpu_cores":    sysinfo.CPUCores(),
		"system_memory_total": sysinfo.MemoryTotalBytes(),
		"system_disk_total":   sysinfo.DiskTotalBytes(diskPath),
	} {
		if value != "" {
			caps[name] = value
		}
	}
	return caps
}
