// Package bundle generates OCI runtime bundles for crun.
// A bundle = config.json (runtime-spec) + rootfs directory.
package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zrougamed/nyxd/pkg/oci"
)

// Spec is a minimal OCI runtime-spec config.json.
type Spec struct {
	OCIVersion string  `json:"ociVersion"`
	Process    Process `json:"process"`
	Root       Root    `json:"root"`
	Mounts     []Mount `json:"mounts"`
	Hooks      *Hooks  `json:"hooks,omitempty"`
	Linux      *Linux  `json:"linux,omitempty"`
	Hostname   string  `json:"hostname,omitempty"`
}

type Process struct {
	Terminal        bool     `json:"terminal"`
	User            User     `json:"user"`
	Args            []string `json:"args"`
	Env             []string `json:"env"`
	Cwd             string   `json:"cwd"`
	Capabilities    *Caps    `json:"capabilities,omitempty"`
	NoNewPrivileges bool     `json:"noNewPrivileges"`
	Rlimits         []Rlimit `json:"rlimits,omitempty"`
}

type User struct {
	UID uint32 `json:"uid"`
	GID uint32 `json:"gid"`
}

type Root struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly"`
}

type Mount struct {
	Destination string   `json:"destination"`
	Type        string   `json:"type,omitempty"`
	Source      string   `json:"source,omitempty"`
	Options     []string `json:"options,omitempty"`
}

type Hooks struct {
	Prestart  []Hook `json:"prestart,omitempty"`
	Poststart []Hook `json:"poststart,omitempty"`
	Poststop  []Hook `json:"poststop,omitempty"`
}

type Hook struct {
	Path    string   `json:"path"`
	Args    []string `json:"args,omitempty"`
	Timeout *int     `json:"timeout,omitempty"`
}

type Caps struct {
	Bounding    []string `json:"bounding"`
	Effective   []string `json:"effective"`
	Inheritable []string `json:"inheritable"`
	Permitted   []string `json:"permitted"`
	Ambient     []string `json:"ambient"`
}

type Linux struct {
	Namespaces        []Namespace       `json:"namespaces"`
	Resources         *Resources        `json:"resources,omitempty"`
	Seccomp           *Seccomp          `json:"seccomp,omitempty"`
	RootfsPropagation string            `json:"rootfsPropagation,omitempty"`
	MaskedPaths       []string          `json:"maskedPaths,omitempty"`
	ReadonlyPaths     []string          `json:"readonlyPaths,omitempty"`
	UIDMappings       []LinuxIDMapping  `json:"uidMappings,omitempty"`
	GIDMappings       []LinuxIDMapping  `json:"gidMappings,omitempty"`
}

// LinuxIDMapping is one uid/gid map entry (OCI linux.uidMappings / gidMappings).
type LinuxIDMapping struct {
	ContainerID uint32 `json:"containerID"`
	HostID      uint32 `json:"hostID"`
	Size        uint32 `json:"size"`
}

type Namespace struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

type Resources struct {
	Memory  *MemoryRes   `json:"memory,omitempty"`
	CPU     *CPURes      `json:"cpu,omitempty"`
	Pids    *PidsRes     `json:"pids,omitempty"`
	BlockIO *BlockIORes  `json:"blockIO,omitempty"`
}

type MemoryRes struct {
	Limit          int64  `json:"limit,omitempty"`
	Swap           int64  `json:"swap,omitempty"`
	Reservation    int64  `json:"reservation,omitempty"`
	OomKillDisable *bool  `json:"disableOOMKiller,omitempty"`
	OomScoreAdj    *int   `json:"oomScoreAdj,omitempty"`
}

type CPURes struct {
	Shares uint64 `json:"shares,omitempty"`
	Quota  int64  `json:"quota,omitempty"`
	Period uint64 `json:"period,omitempty"`
	Cpus   string `json:"cpus,omitempty"`
	Mems   string `json:"mems,omitempty"`
}

type BlockIORes struct {
	Weight uint16 `json:"weight,omitempty"`
}

type PidsRes struct {
	Limit int64 `json:"limit"`
}

type Rlimit struct {
	Type string `json:"type"`
	Hard uint64 `json:"hard"`
	Soft uint64 `json:"soft"`
}

// Options configure bundle generation.
type Options struct {
	ContainerID string
	RootFS      string // absolute path to merged overlayfs dir
	NetNS       string // absolute path to network namespace
	ImageConfig *oci.ImageConfig
	Env         []string
	Args        []string
	WorkDir     string
	User        *User
	Resources   *Resources
	ReadOnly    bool
	Hostname    string
	// ResolvConfPath, when set, is an absolute host path to a file bind-mounted read-only at /etc/resolv.conf.
	ResolvConfPath string
	// ExtraMounts are appended after default runtime mounts (binds, named volumes, etc.).
	ExtraMounts []Mount

	// SeccompProfile: empty or "default" = built-in deny-list profile; "unconfined" = omit seccomp;
	// otherwise absolute path to a JSON fragment matching OCI linux.seccomp.
	SeccompProfile string
	CapAdd         []string
	CapDrop        []string
	Privileged     bool
	// NoNewPrivileges defaults to true when nil; ignored (forced false) when Privileged.
	NoNewPrivileges *bool
	// UIDMappings/GIDMappings enable user namespaces when non-empty (crun + newuidmap).
	UIDMappings []LinuxIDMapping
	GIDMappings []LinuxIDMapping
}

// Generate writes an OCI bundle config.json to bundleDir and returns the dir path.
func Generate(bundleDir string, opts Options) (string, error) {
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		return "", fmt.Errorf("bundle dir: %w", err)
	}

	sec, err := resolveSeccomp(opts)
	if err != nil {
		return "", fmt.Errorf("seccomp: %w", err)
	}
	spec := buildSpec(opts, sec)
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", err
	}

	configPath := filepath.Join(bundleDir, "config.json")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return "", fmt.Errorf("write config.json: %w", err)
	}
	return bundleDir, nil
}

func buildSpec(opts Options, sec *Seccomp) Spec {
	imgCfg := opts.ImageConfig

	args := opts.Args
	if len(args) == 0 {
		args = append(imgCfg.Config.Entrypoint, imgCfg.Config.Cmd...)
	}

	env := mergeEnv(imgCfg.Config.Env, opts.Env)

	cwd := opts.WorkDir
	if cwd == "" {
		cwd = imgCfg.Config.WorkingDir
	}
	if cwd == "" {
		cwd = "/"
	}

	u := User{}
	if opts.User != nil {
		u = *opts.User
	}

	hostname := opts.Hostname
	if hostname == "" {
		hostname = opts.ContainerID
	}

	namespaces := []Namespace{
		{Type: "pid"},
		{Type: "mount"},
		{Type: "ipc"},
		{Type: "uts"},
	}
	if len(opts.UIDMappings) > 0 {
		namespaces = append([]Namespace{{Type: "user"}}, namespaces...)
	}
	if opts.NetNS != "" {
		namespaces = append(namespaces, Namespace{Type: "network", Path: opts.NetNS})
	} else {
		namespaces = append(namespaces, Namespace{Type: "network"})
	}

	var capz *Caps
	noNewPriv := true
	if opts.Privileged {
		capz = allCaps()
		noNewPriv = false
		sec = nil
	} else {
		capz = mergeCapabilities(minimalCapNames(), opts.CapAdd, opts.CapDrop)
		if opts.NoNewPrivileges != nil {
			noNewPriv = *opts.NoNewPrivileges
		}
	}

	linux := &Linux{
		Namespaces:        namespaces,
		Resources:         opts.Resources,
		Seccomp:           sec,
		RootfsPropagation: "rprivate",
		UIDMappings:       opts.UIDMappings,
		GIDMappings:       opts.GIDMappings,
	}
	if opts.Privileged {
		linux.MaskedPaths = nil
		linux.ReadonlyPaths = nil
	} else {
		linux.MaskedPaths = maskedPaths()
		linux.ReadonlyPaths = readonlyPaths()
	}

	return Spec{
		OCIVersion: "1.0.2",
		Hostname:   hostname,
		Root:       Root{Path: opts.RootFS, Readonly: opts.ReadOnly},
		Process: Process{
			Terminal:        false,
			User:            u,
			Args:            args,
			Env:             env,
			Cwd:             cwd,
			NoNewPrivileges: noNewPriv,
			Capabilities:    capz,
			Rlimits: []Rlimit{
				{Type: "RLIMIT_NOFILE", Hard: 1024, Soft: 1024},
				{Type: "RLIMIT_NPROC", Hard: 512, Soft: 512},
			},
		},
		Mounts: appendMounts(resolvThenDefault(opts.ResolvConfPath), opts.ExtraMounts),
		Linux:  linux,
	}
}

func defaultMounts() []Mount {
	return []Mount{
		{Destination: "/proc", Type: "proc", Source: "proc", Options: []string{"nosuid", "noexec", "nodev"}},
		{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
		{Destination: "/dev/pts", Type: "devpts", Source: "devpts", Options: []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620"}},
		{Destination: "/dev/shm", Type: "tmpfs", Source: "shm", Options: []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"}},
		{Destination: "/dev/mqueue", Type: "mqueue", Source: "mqueue", Options: []string{"nosuid", "noexec", "nodev"}},
		{Destination: "/sys", Type: "sysfs", Source: "sysfs", Options: []string{"nosuid", "noexec", "nodev", "ro"}},
		{Destination: "/sys/fs/cgroup", Type: "cgroup", Source: "cgroup", Options: []string{"nosuid", "noexec", "nodev", "relatime", "ro"}},
		{Destination: "/run", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
	}
}

func resolvThenDefault(resolvHost string) []Mount {
	base := defaultMounts()
	if strings.TrimSpace(resolvHost) == "" {
		return base
	}
	resolv := Mount{
		Type:        "bind",
		Source:      resolvHost,
		Destination: "/etc/resolv.conf",
		Options:     []string{"rbind", "ro", "nosuid", "nodev"},
	}
	out := make([]Mount, 0, len(base)+1)
	out = append(out, resolv)
	out = append(out, base...)
	return out
}

func appendMounts(base, extra []Mount) []Mount {
	if len(extra) == 0 {
		return base
	}
	out := make([]Mount, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

func maskedPaths() []string {
	return []string{
		"/proc/acpi", "/proc/asound", "/proc/kcore", "/proc/keys",
		"/proc/latency_stats", "/proc/timer_list", "/proc/timer_stats",
		"/proc/sched_debug", "/sys/firmware", "/proc/scsi",
	}
}

func readonlyPaths() []string {
	return []string{
		"/proc/bus", "/proc/fs", "/proc/irq", "/proc/sys", "/proc/sysrq-trigger",
	}
}

func mergeEnv(base, overrides []string) []string {
	m := make(map[string]string, len(base)+len(overrides))
	for _, e := range base {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	for _, e := range overrides {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
