package control

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

// DaemonInfo are the facts about this daemon that nyxd knows at startup
// (GET /v1/info adds what it reads at request time).
type DaemonInfo struct {
	// Mode is rootful, rootless or userns-remap.
	Mode string
	// HostUID is the host user running a rootless daemon (-1 otherwise).
	HostUID int
	// SubIDs is true when a subordinate ID range is mapped (rootless and
	// remap); false in single-ID rootless mode.
	SubIDs bool
	// Cgroups is false when container resource limits cannot be applied.
	Cgroups bool
	// LogDir is where container logs live.
	LogDir string
}

// SetDaemonInfo records the startup facts reported by GET /v1/info.
func (s *Server) SetDaemonInfo(info DaemonInfo) { s.info = info }

// InfoResponse is GET /v1/info, the source of nyx info: plain facts for bug
// reports. Fields that could not be read are left empty.
type InfoResponse struct {
	ServerVersion string `json:"server_version"`
	Commit        string `json:"commit"`
	BuildDate     string `json:"build_date"`
	GoVersion     string `json:"go_version"`
	Mode          string `json:"mode"`
	HostUID       *int   `json:"host_uid,omitempty"`
	SubIDs        bool   `json:"subordinate_ids"`

	NetworkDriver string `json:"network_driver"`
	DNS           string `json:"dns"`
	Socket        string `json:"socket"`

	BaseDir       string `json:"base_dir"`
	LogDir        string `json:"log_dir"`
	StorageDriver string `json:"storage_driver"`
	UserXattr     bool   `json:"overlay_userxattr"`
	FreeBytes     uint64 `json:"free_bytes,omitempty"`

	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Kernel   string `json:"kernel,omitempty"`
	CgroupV2 bool   `json:"cgroup_v2"`
	Limits   bool   `json:"resource_limits"`
	AppArmor bool   `json:"apparmor"`

	CrunPath    string `json:"crun_path,omitempty"`
	CrunVersion string `json:"crun_version,omitempty"`

	ContainersRunning int `json:"containers_running"`
	ContainersStopped int `json:"containers_stopped"`
	Images            int `json:"images"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	out := InfoResponse{
		ServerVersion: s.version,
		Commit:        s.gitCommit,
		BuildDate:     s.buildDate,
		GoVersion:     goruntime.Version(),
		Mode:          s.info.Mode,
		SubIDs:        s.info.SubIDs,
		NetworkDriver: s.netDriver,
		DNS:           s.dnsMode,
		Socket:        s.socket,
		BaseDir:       s.dataDir,
		LogDir:        s.containerLogDir(),
		StorageDriver: "overlay",
		UserXattr:     s.info.Mode == "rootless",
		OS:            goruntime.GOOS,
		Arch:          goruntime.GOARCH,
		Kernel:        readTrimmed("/proc/sys/kernel/osrelease"),
		CgroupV2:      fileExists("/sys/fs/cgroup/cgroup.controllers"),
		Limits:        s.info.Cgroups,
		AppArmor:      readTrimmed("/sys/module/apparmor/parameters/enabled") == "Y",
	}
	if out.Mode == "" {
		out.Mode = "rootful"
	}
	if s.info.HostUID >= 0 && out.Mode == "rootless" {
		uid := s.info.HostUID
		out.HostUID = &uid
	}
	out.FreeBytes = freeBytes(s.dataDir)
	if s.rt != nil {
		out.CrunPath = s.rt.Binary()
		out.CrunVersion = s.crunVersion(r.Context(), out.CrunPath)
	}
	if s.sup != nil {
		out.ContainersRunning = len(s.sup.ListInfo(r.Context()))
		out.ContainersStopped = len(s.sup.ExitedInfo())
	}
	if s.store != nil {
		if refs, err := s.store.ListImageRefs(); err == nil {
			out.Images = len(refs)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

var crunVersionCache sync.Map // binary path -> version

// crunVersion is the version line of `crun --version`, read once per binary.
func (s *Server) crunVersion(ctx context.Context, bin string) string {
	if v, ok := crunVersionCache.Load(bin); ok {
		return v.(string)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	v := parseCrunVersion(string(b))
	crunVersionCache.Store(bin, v)
	return v
}

// parseCrunVersion picks "1.21" out of "crun version 1.21\ncommit: ...".
func parseCrunVersion(out string) string {
	first, _, _ := strings.Cut(out, "\n")
	if v, ok := strings.CutPrefix(strings.TrimSpace(first), "crun version "); ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(first)
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
