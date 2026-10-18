// Package compose parses a nyxd compose.yaml into a typed Stack.
// Intentionally a strict subset of compose-spec — no legacy fields.
package compose

import "time"

// Stack is the top-level parsed compose file.
type Stack struct {
	// Name is the project name (top-level name:); --project overrides it.
	Name     string             `yaml:"name"`
	Version  string             `yaml:"version"`
	Services map[string]Service `yaml:"services"`
	Networks map[string]Network `yaml:"networks"`
	Volumes  map[string]Volume  `yaml:"volumes"`
}

// Service describes a single container.
type Service struct {
	// Image ref: registry/repo:tag or registry/repo@sha256:...
	Image string `yaml:"image"`

	// Build makes the image from a Dockerfile (nyx compose up builds it when
	// missing; the daemon never pulls an image a service builds).
	Build *Build `yaml:"build"`

	// Optional registry credentials for pulls of this service's image (after .env substitution).
	RegistryUsername string `yaml:"registry_username,omitempty"`
	RegistryPassword string `yaml:"registry_password,omitempty"`

	// Command overrides entrypoint args.
	Command []string `yaml:"command"`

	// Entrypoint overrides OCI image entrypoint.
	Entrypoint []string `yaml:"entrypoint"`

	// Environment variables.
	Environment map[string]string `yaml:"environment"`

	// EnvFile lists files of KEY=VALUE lines loaded before environment:.
	EnvFile EnvFiles `yaml:"env_file"`

	// Volumes: host:container[:ro]
	Volumes []string `yaml:"volumes"`

	// Networks this service is attached to.
	Networks []string `yaml:"networks"`

	// Ports: host:container[/proto]
	Ports []string `yaml:"ports"`

	// Restart policy: no | always | on-failure | unless-stopped
	Restart RestartPolicy `yaml:"restart"`

	// Healthcheck configuration.
	Healthcheck *Healthcheck `yaml:"healthcheck"`

	// Resource limits.
	Deploy *Deploy `yaml:"deploy"`

	// DependsOn service names (list or Compose long-form map with condition:).
	DependsOn DependsOn `yaml:"depends_on"`

	// Privileged mode (avoid — sets no-new-privs=false).
	Privileged bool `yaml:"privileged"`

	// ReadOnly root filesystem.
	ReadOnly bool `yaml:"read_only"`

	// User override: "uid:gid"
	User string `yaml:"user"`

	// Labels applied to the container state.
	Labels map[string]string `yaml:"labels"`

	// CapAdd / CapDrop for fine-grained capability control.
	CapAdd  []string `yaml:"cap_add"`
	CapDrop []string `yaml:"cap_drop"`

	// SeccompProfile: empty or "default" = built-in profile; "unconfined" disables seccomp;
	// otherwise path to JSON (absolute or relative to the compose file directory).
	SeccompProfile string `yaml:"seccomp_profile"`

	// ApparmorProfile is a host-loaded AppArmor profile name applied via
	// process.apparmorProfile before the entrypoint runs. Empty keeps the
	// current default (no profile).
	ApparmorProfile string `yaml:"apparmor_profile"`

	// DNS is an optional list of IPv4 nameserver addresses written into the
	// container's /etc/resolv.conf. When set, the gateway resolver file is not
	// mounted. A volume whose destination is /etc/resolv.conf wins over this.
	DNS []string `yaml:"dns"`

	// Ulimits overrides default process rlimits for this service (e.g. nofile).
	Ulimits map[string]Ulimit `yaml:"ulimits"`

	// NoNewPrivileges sets no-new-privs in OCI spec.
	NoNewPrivileges *bool `yaml:"no_new_privileges"`

	// StopTimeout before SIGKILL during shutdown.
	StopTimeout Duration `yaml:"stop_grace_period"`
}

// Ulimit is a soft/hard resource limit pair (compose ulimits.<name>).
// Accepts either `{soft: N, hard: M}` or a bare integer (soft = hard = N).
type Ulimit struct {
	Soft uint64 `yaml:"soft"`
	Hard uint64 `yaml:"hard"`
}

func (u *Ulimit) UnmarshalYAML(unmarshal func(any) error) error {
	var n uint64
	if err := unmarshal(&n); err == nil {
		u.Soft, u.Hard = n, n
		return nil
	}
	var raw struct {
		Soft uint64 `yaml:"soft"`
		Hard uint64 `yaml:"hard"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	u.Soft, u.Hard = raw.Soft, raw.Hard
	return nil
}

// RestartPolicy controls container restart behavior.
type RestartPolicy string

const (
	RestartNo            RestartPolicy = "no"
	RestartAlways        RestartPolicy = "always"
	RestartOnFailure     RestartPolicy = "on-failure"
	RestartUnlessStopped RestartPolicy = "unless-stopped"
)

// Healthcheck describes how to probe container liveness.
type Healthcheck struct {
	// Test: ["CMD", "curl", "-f", "http://localhost/health"]
	Test     []string `yaml:"test"`
	Interval Duration `yaml:"interval"`
	Timeout  Duration `yaml:"timeout"`
	Retries  int      `yaml:"retries"`
	// StartPeriod: grace time before failures count.
	StartPeriod Duration `yaml:"start_period"`
}

// Deploy holds resource limit configuration.
type Deploy struct {
	Resources Resources `yaml:"resources"`
}

// Resources defines CPU/memory limits and reservations.
type Resources struct {
	Limits       ResourceSpec `yaml:"limits"`
	Reservations ResourceSpec `yaml:"reservations"`
}

// ResourceSpec is a single limit/reservation set.
type ResourceSpec struct {
	// CPUs: "0.5" = 50% of one core (maps to cgroup v2 cpu.max quota/period).
	CPUs string `yaml:"cpus"`
	// Memory: "128m", "1g"
	Memory string `yaml:"memory"`
	// MemorySwap is memory plus swap (same suffixes as Memory). Equal to Memory
	// means no extra swap. Omit to keep the runtime default (2× memory limit).
	MemorySwap string `yaml:"memory_swap,omitempty"`
	// PidsLimit caps the number of tasks in the cgroup (maps to pids.max).
	PidsLimit *int `yaml:"pids_limit,omitempty"`
	// OomKillDisable maps to memory.oom_control (OCI disableOOMKiller).
	OomKillDisable *bool  `yaml:"oom_kill_disable,omitempty"`
	OomScoreAdj    *int   `yaml:"oom_score_adj,omitempty"`
	CpusetCpus     string `yaml:"cpuset_cpus,omitempty"`
	CpusetMems     string `yaml:"cpuset_mems,omitempty"`
	BlkioWeight    *int   `yaml:"blkio_weight,omitempty"`
}

// Network describes a CNI-backed or native bridge network.
type Network struct {
	// Driver: bridge | host | none. Default: bridge.
	Driver string `yaml:"driver"`
	// Bridge is the host bridge interface name for the native driver.
	// With Subnet, services attached to this network use that bridge instead
	// of nyxbr0. Empty keeps the daemon default bridge.
	Bridge string `yaml:"bridge"`
	// Subnet in CIDR notation.
	Subnet string `yaml:"subnet"`
	// Internal: no external routing.
	Internal bool `yaml:"internal"`
}

// Volume describes a named volume mount.
type Volume struct {
	// Driver: local only for now.
	Driver string `yaml:"driver"`
}

// Duration is a yaml-parseable time.Duration.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}
