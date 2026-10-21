package supervisor

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/cgstats"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/runtime"
)

// ContainerInspect is the JSON shape for GET /v1/containers/{id}/inspect
// and `nyx inspect` (docker/podman-like low-level details).
type ContainerInspect struct {
	ID            string                `json:"id"`
	ShortID       string                `json:"short_id"`
	Image         string                `json:"image"`
	Status        string                `json:"status"`
	Pid           int                   `json:"pid,omitempty"`
	IP            string                `json:"ip,omitempty"`
	Ports         []network.PortMapping `json:"ports,omitempty"`
	PortsDisplay  string                `json:"ports_display,omitempty"`
	Env           []string              `json:"env,omitempty"`
	Args          []string              `json:"args,omitempty"`
	WorkDir       string                `json:"workdir,omitempty"`
	Hostname      string                `json:"hostname,omitempty"`
	RestartPolicy string                `json:"restart_policy,omitempty"`
	Restarts      int                   `json:"restarts"`
	Stopped       bool                  `json:"intentionally_stopped"`
	AutoRemove    bool                  `json:"auto_remove,omitempty"`
	HostNetwork   bool                  `json:"host_network,omitempty"`
	ReadOnly      bool                  `json:"read_only,omitempty"`
	Privileged    bool                  `json:"privileged,omitempty"`
	BundleDir     string                `json:"bundle_dir,omitempty"`
	RootFS        string                `json:"rootfs,omitempty"`
	NetNS         string                `json:"netns,omitempty"`
	Resources     *bundle.Resources     `json:"resources,omitempty"`
	CreatedAt     string                `json:"created_at,omitempty"` // RFC3339 from supervisor persist mtime when present
	ExitCode      *int                  `json:"exit_code,omitempty"`
}

// ContainerStats is one resource-usage snapshot for `nyx stats`.
type ContainerStats struct {
	ID         string  `json:"id"`
	ShortID    string  `json:"short_id"`
	Name       string  `json:"name"` // canonical id (docker NAME column)
	Status     string  `json:"status"`
	CPUPerc    float64 `json:"cpu_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
	MemPerc    float64 `json:"mem_percent"`
	NetRx      uint64  `json:"net_rx"`
	NetTx      uint64  `json:"net_tx"`
	BlockRead  uint64  `json:"block_read"`
	BlockWrite uint64  `json:"block_write"`
	PIDs       uint64  `json:"pids"`
	Source     string  `json:"source,omitempty"` // cgroup|proc
	ReadAt     string  `json:"read_at"`          // RFC3339
	Err        string  `json:"error,omitempty"`  // non-fatal: stats unavailable
}

// Inspect returns low-level details for a supervised container.
func (s *Supervisor) Inspect(ctx context.Context, id string) (*ContainerInspect, error) {
	s.mu.RLock()
	e, ok := s.containers[id]
	if !ok {
		s.mu.RUnlock()
		return nil, fmt.Errorf("container %q not found", id)
	}
	e.mu.Lock()
	spec := e.spec
	ip := e.ip
	bundleDir := e.bundleDir
	rootFS := e.rootFS
	netNS := e.netNS
	restarts := e.restarts
	stopped := e.stopped
	e.mu.Unlock()
	s.mu.RUnlock()

	out := &ContainerInspect{
		ID:            spec.ID,
		ShortID:       DisplayID(spec.ID),
		Image:         spec.Image,
		Status:        "unknown",
		IP:            ip,
		Ports:         append([]network.PortMapping(nil), spec.PortMappings...),
		PortsDisplay:  formatPortMappings(spec.PortMappings),
		Env:           append([]string(nil), spec.Env...),
		Args:          append([]string(nil), spec.Args...),
		WorkDir:       spec.WorkDir,
		Hostname:      spec.Hostname,
		RestartPolicy: string(spec.RestartPolicy),
		Restarts:      restarts,
		Stopped:       stopped,
		AutoRemove:    spec.AutoRemove,
		HostNetwork:   spec.HostNetwork || s.onHostNetwork(spec),
		ReadOnly:      spec.ReadOnly,
		Privileged:    spec.Privileged,
		BundleDir:     bundleDir,
		RootFS:        rootFS,
		NetNS:         netNS,
		Resources:     spec.Resources,
	}
	if st, err := os.Stat(s.persistPath(id)); err == nil {
		out.CreatedAt = st.ModTime().UTC().Format(time.RFC3339)
	}

	stateCtx := ctx
	if stateCtx == nil {
		stateCtx = context.Background()
	}
	stateCtx, cancel := context.WithTimeout(stateCtx, 10*time.Second)
	defer cancel()
	if st, err := s.rt.State(stateCtx, id); err == nil && st != nil {
		out.Status = st.Status
		out.Pid = st.Pid
		out.ExitCode = st.ExitCode
		if out.BundleDir == "" {
			out.BundleDir = st.Bundle
		}
	} else if err != nil && runtime.CrunContainerAbsent(err) {
		out.Status = "absent"
	}
	return out, nil
}

// Stats returns resource-usage snapshots for the given ids (empty = all supervised).
// CPU percent needs two samples ~interval apart; interval of 0 uses 500ms.
func (s *Supervisor) Stats(ctx context.Context, ids []string, interval time.Duration) ([]ContainerStats, error) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	targets, err := s.statsTargets(ids)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return []ContainerStats{}, nil
	}

	type snap struct {
		id, status string
		pid        int
		sample     cgstats.Sample
		sampleErr  error
	}
	first := make([]snap, len(targets))
	for i, id := range targets {
		first[i].id = id
		pid, status := s.runtimePID(ctx, id)
		first[i].pid = pid
		first[i].status = status
		if pid > 0 {
			first[i].sample, first[i].sampleErr = cgstats.Read(pid)
		} else {
			first[i].sampleErr = fmt.Errorf("no pid")
		}
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(interval):
	}

	out := make([]ContainerStats, 0, len(targets))
	for i, id := range targets {
		st := ContainerStats{
			ID:      id,
			ShortID: DisplayID(id),
			Name:    id,
			Status:  first[i].status,
			ReadAt:  time.Now().UTC().Format(time.RFC3339Nano),
		}
		pid := first[i].pid
		if pid <= 0 {
			st.Err = "not running"
			out = append(out, st)
			continue
		}
		cur, err := cgstats.Read(pid)
		if err != nil {
			st.Err = err.Error()
			out = append(out, st)
			continue
		}
		prev := first[i].sample
		if first[i].sampleErr != nil {
			prev = cur // CPU% unknown on first good sample
		}
		st.CPUPerc = cgstats.CPUPercent(prev, cur)
		st.MemUsage = cur.MemCurrent
		st.MemLimit = cur.MemLimit
		st.MemPerc = cgstats.MemPercent(cur)
		st.NetRx = cur.NetRx
		st.NetTx = cur.NetTx
		st.BlockRead = cur.BlockRead
		st.BlockWrite = cur.BlockWrite
		st.PIDs = cur.PIDs
		st.Source = cur.Source
		st.ReadAt = cur.Timestamp.Format(time.RFC3339Nano)
		out = append(out, st)
	}
	return out, nil
}

func (s *Supervisor) statsTargets(ids []string) ([]string, error) {
	if len(ids) == 0 {
		all := s.List()
		sort.Strings(all)
		return all, nil
	}
	seen := make(map[string]struct{}, len(ids))
	var out []string
	for _, id := range ids {
		s.mu.RLock()
		_, ok := s.containers[id]
		s.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("container %q not found", id)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func (s *Supervisor) runtimePID(ctx context.Context, id string) (int, string) {
	stateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := s.rt.State(stateCtx, id)
	if err != nil {
		if runtime.CrunContainerAbsent(err) {
			return 0, "absent"
		}
		return 0, "unknown"
	}
	if st == nil {
		return 0, "unknown"
	}
	if st.Status != "running" || st.Pid <= 0 {
		return 0, st.Status
	}
	return st.Pid, st.Status
}
