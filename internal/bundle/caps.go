package bundle

import (
	"slices"
	"strings"
)

// allCapabilityNames is the set of Linux capability names nyxd grants in privileged mode.
// Kept in sync with common CAP_LAST_CAP values on recent kernels.
func allCapabilityNames() []string {
	return []string{
		"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_DAC_READ_SEARCH", "CAP_FOWNER", "CAP_FSETID",
		"CAP_KILL", "CAP_SETGID", "CAP_SETUID", "CAP_SETPCAP", "CAP_LINUX_IMMUTABLE",
		"CAP_NET_BIND_SERVICE", "CAP_NET_BROADCAST", "CAP_NET_ADMIN", "CAP_NET_RAW",
		"CAP_IPC_LOCK", "CAP_IPC_OWNER", "CAP_SYS_MODULE", "CAP_SYS_RAWIO", "CAP_SYS_CHROOT",
		"CAP_SYS_PTRACE", "CAP_SYS_PACCT", "CAP_SYS_ADMIN", "CAP_SYS_BOOT", "CAP_SYS_NICE",
		"CAP_SYS_RESOURCE", "CAP_SYS_TIME", "CAP_SYS_TTY_CONFIG", "CAP_MKNOD", "CAP_LEASE",
		"CAP_AUDIT_WRITE", "CAP_AUDIT_CONTROL", "CAP_SETFCAP", "CAP_MAC_OVERRIDE", "CAP_MAC_ADMIN",
		"CAP_SYSLOG", "CAP_WAKE_ALARM", "CAP_BLOCK_SUSPEND", "CAP_AUDIT_READ", "CAP_PERFMON",
		"CAP_BPF", "CAP_CHECKPOINT_RESTORE",
	}
}

func allCaps() *Caps {
	names := append([]string(nil), allCapabilityNames()...)
	slices.Sort(names)
	cp := append([]string(nil), names...)
	return &Caps{
		Bounding:    append([]string(nil), names...),
		Effective:   append([]string(nil), cp...),
		Permitted:   append([]string(nil), cp...),
		Inheritable: append([]string(nil), cp...),
		Ambient:     append([]string(nil), cp...),
	}
}

func normalizeCap(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	u := strings.ToUpper(s)
	if u == "ALL" {
		return "ALL"
	}
	if strings.HasPrefix(u, "CAP_") {
		return u
	}
	return "CAP_" + u
}

// mergeCapabilities computes OCI capability sets from baseline + cap_add − cap_drop.
// If cap_drop contains ALL, the baseline is cleared before adds (Docker-compatible).
func mergeCapabilities(baseline []string, capAdd, capDrop []string) *Caps {
	dropAll := false
	dropSet := make(map[string]struct{})
	for _, d := range capDrop {
		n := normalizeCap(d)
		if n == "ALL" {
			dropAll = true
			continue
		}
		if n != "" {
			dropSet[n] = struct{}{}
		}
	}

	capset := make(map[string]struct{})
	if !dropAll {
		for _, b := range baseline {
			capset[b] = struct{}{}
		}
	}
	for _, a := range capAdd {
		n := normalizeCap(a)
		if n == "" || n == "ALL" {
			continue
		}
		capset[n] = struct{}{}
	}
	for d := range dropSet {
		delete(capset, d)
	}
	out := make([]string, 0, len(capset))
	for c := range capset {
		out = append(out, c)
	}
	slices.Sort(out)
	cp := append([]string(nil), out...)
	return &Caps{
		Bounding:    append([]string(nil), out...),
		Effective:   append([]string(nil), cp...),
		Permitted:   append([]string(nil), cp...),
		Inheritable: []string{},
		Ambient:     []string{},
	}
}

func minimalCapNames() []string {
	return []string{
		"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_FSETID", "CAP_FOWNER",
		"CAP_SETGID", "CAP_SETUID", "CAP_SETFCAP", "CAP_SETPCAP",
		"CAP_NET_BIND_SERVICE", "CAP_NET_RAW", "CAP_KILL", "CAP_AUDIT_WRITE",
	}
}
