package command

// MaxProcesses bounds a process.list answer: the busiest processes are
// kept (CPU, then memory), so a host with thousands of processes stays far
// below the gRPC message limit. Total says how many there were.
const MaxProcesses = 2000

// ProcessListRequest asks the agent for its host's processes.
type ProcessListRequest struct{}

// ProcessListResponse is one reading of the host's processes. Disk I/O is
// per group (cgroup v2 io.stat, readable without privilege, whole physical
// disks only), not per process. A host without a cgroup v2 root has no
// groups.
type ProcessListResponse struct {
	SampledAt int64     `json:"sampled_at"` // unix ms
	NCPU      int       `json:"ncpu"`
	MemTotal  uint64    `json:"mem_total"` // bytes
	Total     int       `json:"total"`     // processes seen; len(Processes) is at most MaxProcesses
	Processes []Process `json:"processes"`
	// Groups holds the group of every process seen, the ones past the cap
	// too: busiest writer first, then reader, unknown rates last; ties put
	// containers first, then more processes, then the lower ID.
	Groups []ProcessGroup `json:"groups"`
}

// ProcessGroup is one cgroup's disk I/O: a container, a systemd service or
// a session scope. Rates are nil when unknown (first sight, io.stat not
// readable); a device that starts or stops counting adds nothing.
type ProcessGroup struct {
	ID          string   `json:"id"`    // cgroup v2 path, e.g. /system.slice/postgresql.service
	Label       string   `json:"label"` // leaf name; for a container its name from the status snapshot, empty when it is in none
	Kind        string   `json:"kind"`  // container | service | scope | slice | other
	ContainerID string   `json:"container_id,omitempty"`
	AppID       string   `json:"app_id,omitempty"`
	Processes   int      `json:"processes"` // processes seen in it
	ReadBps     *float64 `json:"read_bps"`
	WriteBps    *float64 `json:"write_bps"`
}

// Process is one process. CPUPct is nil when unknown: the process started
// between the two readings.
type Process struct {
	PID         int      `json:"pid"`
	PPID        int      `json:"ppid"`
	Name        string   `json:"name"`
	Cmdline     string   `json:"cmdline"`
	User        string   `json:"user"`
	State       string   `json:"state"`
	Threads     int      `json:"threads"`
	StartTime   int64    `json:"start_time"` // unix s
	Kernel      bool     `json:"kernel"`
	CPUPct      *float64 `json:"cpu_pct"` // per core, top-style: may exceed 100
	RSS         uint64   `json:"rss"`     // bytes
	MemPct      float64  `json:"mem_pct"`
	Group       string   `json:"group,omitempty"` // its ProcessGroup's ID; empty when none
	ContainerID string   `json:"container_id,omitempty"`
	AppID       string   `json:"app_id,omitempty"`
}
