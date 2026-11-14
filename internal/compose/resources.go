package compose

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zrougamed/nyxd/internal/bundle"
)

// BundleResourcesFromDeploy maps compose deploy.resources into OCI linux.resources.
func BundleResourcesFromDeploy(d *Deploy) (*bundle.Resources, error) {
	if d == nil {
		return nil, nil
	}
	return resourcesFrom(d.Resources.Limits, d.Resources.Reservations, func(f string) string {
		return "deploy.resources." + f
	})
}

// RunLimits are the resource flags of `nyx run` (--memory, --cpus, ...).
type RunLimits struct {
	Memory      string `json:"memory,omitempty"`
	MemorySwap  string `json:"memory_swap,omitempty"`
	CPUs        string `json:"cpus,omitempty"`
	PidsLimit   int    `json:"pids_limit,omitempty"`
	OomScoreAdj *int   `json:"oom_score_adj,omitempty"`
}

// BundleResourcesFromRun maps `nyx run` resource flags into OCI
// linux.resources, with the same parsing and defaults as compose.
func BundleResourcesFromRun(l RunLimits) (*bundle.Resources, error) {
	lim := ResourceSpec{
		Memory:      l.Memory,
		MemorySwap:  l.MemorySwap,
		CPUs:        l.CPUs,
		OomScoreAdj: l.OomScoreAdj,
	}
	if l.PidsLimit != 0 {
		if l.PidsLimit < 0 {
			return nil, fmt.Errorf("pids_limit: must be positive, got %d", l.PidsLimit)
		}
		lim.PidsLimit = &l.PidsLimit
	}
	if l.OomScoreAdj != nil && (*l.OomScoreAdj < -1000 || *l.OomScoreAdj > 1000) {
		return nil, fmt.Errorf("oom_score_adj: must be between -1000 and 1000, got %d", *l.OomScoreAdj)
	}
	return resourcesFrom(lim, ResourceSpec{}, func(f string) string {
		return strings.TrimPrefix(f, "limits.")
	})
}

// resourcesFrom builds linux.resources; field names a setting in errors.
func resourcesFrom(lim, res ResourceSpec, field func(string) string) (*bundle.Resources, error) {

	var mem *bundle.MemoryRes
	var cpu *bundle.CPURes
	var pids *bundle.PidsRes
	var blk *bundle.BlockIORes

	if lim.Memory != "" {
		b, err := parseByteSize(lim.Memory)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field("limits.memory"), err)
		}
		if b > 0 {
			mem = &bundle.MemoryRes{Limit: b}
		}
	}
	if lim.MemorySwap != "" {
		b, err := parseByteSize(lim.MemorySwap)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field("limits.memory_swap"), err)
		}
		if b > 0 {
			if mem == nil {
				mem = &bundle.MemoryRes{}
			}
			// Swap is memory+swap total; set before withSwapLimit so an
			// explicit value equal to Limit means no extra swap.
			mem.Swap = b
		}
	}
	if res.Memory != "" {
		b, err := parseByteSize(res.Memory)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field("reservations.memory"), err)
		}
		if b > 0 {
			if mem == nil {
				mem = &bundle.MemoryRes{}
			}
			mem.Reservation = b
		}
	}
	if lim.OomKillDisable != nil {
		if mem == nil {
			mem = &bundle.MemoryRes{}
		}
		mem.OomKillDisable = lim.OomKillDisable
	}
	if lim.OomScoreAdj != nil {
		if mem == nil {
			mem = &bundle.MemoryRes{}
		}
		mem.OomScoreAdj = lim.OomScoreAdj
	}

	if lim.CPUs != "" {
		q, period, err := parseCPUQuota(lim.CPUs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field("limits.cpus"), err)
		}
		if q > 0 && period > 0 {
			cpu = &bundle.CPURes{Quota: q, Period: period}
		}
	}
	if lim.CpusetCpus != "" || lim.CpusetMems != "" {
		if cpu == nil {
			cpu = &bundle.CPURes{}
		}
		cpu.Cpus = strings.TrimSpace(lim.CpusetCpus)
		cpu.Mems = strings.TrimSpace(lim.CpusetMems)
	}

	if lim.PidsLimit != nil && *lim.PidsLimit > 0 {
		pids = &bundle.PidsRes{Limit: int64(*lim.PidsLimit)}
	}

	if lim.BlkioWeight != nil && *lim.BlkioWeight > 0 {
		w := *lim.BlkioWeight
		if w > 0xffff {
			w = 0xffff
		}
		blk = &bundle.BlockIORes{Weight: uint16(w)}
	}

	if mem == nil && cpu == nil && pids == nil && blk == nil {
		return nil, nil
	}
	return &bundle.Resources{
		Memory:  mem,
		CPU:     cpu,
		Pids:    pids,
		BlockIO: blk,
	}, nil
}

// ResolveSeccompProfilePath turns a compose seccomp_profile value into a path or token
// understood by [bundle.Options.SeccompProfile].
func ResolveSeccompProfilePath(composeDir, profile string) string {
	p := strings.TrimSpace(profile)
	if p == "" {
		return ""
	}
	low := strings.ToLower(p)
	if low == "unconfined" || low == "default" || low == "builtin" ||
		low == "runtime/default" || low == "disabled" {
		return p
	}
	if filepath.IsAbs(p) {
		return p
	}
	if composeDir == "" {
		return p
	}
	return filepath.Clean(filepath.Join(composeDir, p))
}

func parseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, nil
	}
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	numStr, suf := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i:])
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	var mult int64 = 1
	switch suf {
	case "", "b":
		mult = 1
	case "k", "kb", "kib":
		mult = 1024
	case "m", "mb", "mib":
		mult = 1024 * 1024
	case "g", "gb", "gib":
		mult = 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown suffix in %q", s)
	}
	v := int64(n * float64(mult))
	if v < 0 {
		return 0, fmt.Errorf("negative size %q", s)
	}
	return v, nil
}

func parseCPUQuota(s string) (quota int64, period uint64, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, 0, err
	}
	if f <= 0 {
		return 0, 0, fmt.Errorf("cpus must be positive, got %v", f)
	}
	const p = uint64(100000)
	q := int64(f*float64(p) + 0.5)
	if q < 1000 {
		q = 1000
	}
	return q, p, nil
}
