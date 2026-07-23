package api

import (
	"net/http"
	"os"
	"runtime"
	"syscall"

	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/cyberm-tech/vphone-web/internal/vm"
)

// diskUsage reports free/total bytes for the filesystem containing path.
type diskUsage struct {
	Path       string `json:"path"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
}

func statfs(path string) diskUsage {
	du := diskUsage{Path: path}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err == nil {
		du.TotalBytes = st.Blocks * uint64(st.Bsize)
		du.FreeBytes = st.Bavail * uint64(st.Bsize)
		du.UsedBytes = du.TotalBytes - st.Bfree*uint64(st.Bsize)
	}
	return du
}

// systemStatus is the GET /api/v1/system/status response.
type systemStatus struct {
	Hostname      string         `json:"hostname"`
	OS            string         `json:"os"`
	Arch          string         `json:"arch"`
	NumCPU        int            `json:"num_cpu"`
	GoMaxProcs    int            `json:"go_max_procs"`
	TotalVMs      int            `json:"total_vms"`
	RunningVMs    int            `json:"running_vms"`
	VMsByStatus   map[string]int `json:"vms_by_status"`
	MaxConcurrent int            `json:"max_concurrent_vms"`
	ActiveJobs    int            `json:"active_jobs"`
	AllocMB       uint64         `json:"alloc_mb"`
	VphoneCLIDir  string         `json:"vphone_cli_dir"`
	VMDisk        diskUsage      `json:"vm_disk"`
	IPSWDisk      diskUsage      `json:"ipsw_disk"`
}

// systemStatusHandler handles GET /api/v1/system/status.
func (s *Server) systemStatusHandler(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vms.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enumerate VMs")
		return
	}
	byStatus := map[string]int{}
	running := 0
	for _, v := range vms {
		byStatus[string(v.Status)]++
		if v.Status == vm.StatusRunning || v.Status == vm.StatusBooting || v.Status == vm.StatusStopping {
			running++
		}
	}

	active := 0
	if s.jobs != nil {
		if js, err := s.jobs.List("", 200); err == nil {
			for _, j := range js {
				if j.Status == jobs.StatusRunning || j.Status == jobs.StatusPending {
					active++
				}
			}
		}
	}

	hostname, _ := os.Hostname()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	writeJSON(w, http.StatusOK, systemStatus{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		NumCPU:        runtime.NumCPU(),
		GoMaxProcs:    runtime.GOMAXPROCS(0),
		TotalVMs:      len(vms),
		RunningVMs:    running,
		VMsByStatus:   byStatus,
		MaxConcurrent: s.cfg.Limits.MaxConcurrentVMs,
		ActiveJobs:    active,
		AllocMB:       mem.Alloc / (1024 * 1024),
		VphoneCLIDir:  s.cfg.Paths.VphoneCLI,
		VMDisk:        statfs(s.cfg.Paths.VMRoot),
		IPSWDisk:      statfs(s.cfg.Paths.IPSWDir),
	})
}

// systemConfig is the GET /api/v1/system/config response (safe fields only).
type systemConfig struct {
	VMRoot        string `json:"vm_root"`
	IPSWDir       string `json:"ipsw_dir"`
	VphoneCLIDir  string `json:"vphone_cli_dir"`
	PortBase      int    `json:"port_base"`
	PortBlockSize int    `json:"port_block_size"`
	MaxVMs        int    `json:"max_concurrent_vms"`
	MaxJobs       int    `json:"max_concurrent_jobs"`
}

// systemConfigHandler handles GET /api/v1/system/config.
func (s *Server) systemConfigHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, systemConfig{
		VMRoot:        s.cfg.Paths.VMRoot,
		IPSWDir:       s.cfg.Paths.IPSWDir,
		VphoneCLIDir:  s.cfg.Paths.VphoneCLI,
		PortBase:      s.cfg.Ports.Base,
		PortBlockSize: s.cfg.Ports.BlockSize,
		MaxVMs:        s.cfg.Limits.MaxConcurrentVMs,
		MaxJobs:       s.cfg.Limits.MaxConcurrentJobs,
	})
}
