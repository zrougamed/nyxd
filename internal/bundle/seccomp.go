package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Seccomp is the OCI linux.seccomp object (runtime-spec).
type Seccomp struct {
	DefaultAction string         `json:"defaultAction"`
	Architectures []string       `json:"architectures,omitempty"`
	Syscalls      []SyscallRule  `json:"syscalls,omitempty"`
}

// SyscallRule is one seccomp syscall match rule.
type SyscallRule struct {
	Names  []string `json:"names"`
	Action string   `json:"action"`
}

func seccompArchitecturesForHost() []string {
	switch runtime.GOARCH {
	case "amd64":
		return []string{"SCMP_ARCH_X86_64", "SCMP_ARCH_X86", "SCMP_ARCH_X32"}
	case "arm64":
		return []string{"SCMP_ARCH_AARCH64", "SCMP_ARCH_ARM"}
	case "arm":
		return []string{"SCMP_ARCH_ARM"}
	case "386":
		return []string{"SCMP_ARCH_X86"}
	case "riscv64":
		return []string{"SCMP_ARCH_RISCV64"}
	default:
		return nil
	}
}

// defaultSeccompProfile returns a conservative deny-list profile: default allow,
// errno for syscalls that are rarely needed in containers but often abused.
// Custom strict allow-lists can be supplied via Options.SeccompProfile (JSON path).
func defaultSeccompProfile() *Seccomp {
	return &Seccomp{
		DefaultAction: "SCMP_ACT_ALLOW",
		Architectures: seccompArchitecturesForHost(),
		Syscalls: []SyscallRule{{
			Action: "SCMP_ACT_ERRNO",
			Names: []string{
				"add_key", "bpf", "delete_module", "finit_module", "init_module",
				"io_uring_setup", "keyctl", "kexec_file_load", "kexec_load",
				"open_by_handle_at", "perf_event_open", "personality", "ptrace",
				"request_key", "userfaultfd",
			},
		}},
	}
}

func loadSeccompProfile(path string) (*Seccomp, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc Seccomp
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse seccomp json: %w", err)
	}
	if sc.DefaultAction == "" {
		return nil, fmt.Errorf("seccomp profile %q: missing defaultAction", path)
	}
	return &sc, nil
}

func resolveSeccomp(opts Options) (*Seccomp, error) {
	if opts.Privileged {
		return nil, nil
	}
	p := strings.TrimSpace(opts.SeccompProfile)
	low := strings.ToLower(p)
	switch {
	case low == "" || low == "default" || low == "builtin" || low == "runtime/default":
		return defaultSeccompProfile(), nil
	case low == "unconfined" || low == "disabled":
		return nil, nil
	default:
		return loadSeccompProfile(p)
	}
}
