package command

// MaxProcesses bounds a process.list answer: the busiest processes are
// kept (CPU, then memory), so a host with thousands of processes stays far
// below the gRPC message limit. Total says how many there were.
const MaxProcesses = 2000

// ProcessListRequest asks the agent for its host's processes.
type ProcessListRequest struct{}

// ProcessListResponse is one reading of the host's processes.
type ProcessListResponse struct {
	SampledAt int64     `json:"sampled_at"` // unix ms
	NCPU      int       `json:"ncpu"`
	MemTotal  uint64    `json:"mem_total"` // bytes
	Total     int       `json:"total"`     // processes seen; len(Processes) is at most MaxProcesses
	Processes []Process `json:"processes"`
}

// Process is one process. The rates are nil when unknown: the process
// started between the two readings, or /proc/<pid>/io is not readable to
// the agent (it needs CAP_SYS_PTRACE for other users' processes).
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
	ReadBps     *float64 `json:"read_bps"`
	WriteBps    *float64 `json:"write_bps"`
	ContainerID string   `json:"container_id,omitempty"`
	AppID       string   `json:"app_id,omitempty"`
}
