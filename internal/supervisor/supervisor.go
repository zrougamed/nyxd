// Package supervisor manages container lifecycle: start, monitor, restart, stop.
// Implements restart policies: always, on-failure, unless-stopped, never.
//
// Networking is injected as [network.Backend] (native or CNI exec); see docs/networking.md.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/logs"
	"github.com/zrougamed/nyxd/internal/netdns"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/overlay"
	"github.com/zrougamed/nyxd/internal/runtime"
	"github.com/zrougamed/nyxd/pkg/oci"
)

// defaultStopGrace is used when ContainerSpec.StopTimeout is zero (compose omitting
// stop_grace_period, or nyx run without an explicit stop budget). Matches Docker's
// default `docker stop` grace (~10s) instead of blocking ~90s on workloads that ignore SIGTERM.
const defaultStopGrace = 10 * time.Second

// RestartPolicy controls when a container is restarted after exit.
type RestartPolicy string

const (
	RestartAlways        RestartPolicy = "always"
	RestartOnFailure     RestartPolicy = "on-failure"
	RestartUnlessStopped RestartPolicy = "unless-stopped"
	RestartNever         RestartPolicy = "never"
)

// ContainerSpec fully describes a container to run.
type ContainerSpec struct {
	ID             string
	Image          string
	ImageConfig    *oci.ImageConfig
	ManifestLayers []oci.Descriptor // ordered lower→upper
	BlobPaths      []string

	Env          []string
	Args         []string
	WorkDir      string
	User         *bundle.User
	Resources    *bundle.Resources
	PortMappings []network.PortMapping
	ReadOnly     bool
	Hostname     string

	RestartPolicy RestartPolicy
	MaxRestarts   int // 0 = unlimited (for always/on-failure)
	StopTimeout   time.Duration

	Healthcheck *health.Config

	// ExtraMounts are OCI bind (or other) mounts merged into the bundle after defaults.
	ExtraMounts []bundle.Mount `json:"extra_mounts,omitempty"`
	// EmbedDNS marks compose services on CNI with -dns auto that use internal-only networks,
	// so the embedded resolver registers their hostnames. Ignored for native and for -dns embedded.
	EmbedDNS bool `json:"embed_dns,omitempty"`
	// ComposeInternalNet is true when every compose network for the service has internal: true.
	// The native driver blocks IPv4 egress outside the bridge CIDR (nft forward).
	ComposeInternalNet bool `json:"compose_internal_net,omitempty"`

	Privileged      bool                    `json:"privileged,omitempty"`
	SeccompProfile  string                  `json:"seccomp_profile,omitempty"`
	CapAdd          []string                `json:"cap_add,omitempty"`
	CapDrop         []string                `json:"cap_drop,omitempty"`
	NoNewPrivileges *bool                   `json:"no_new_privileges,omitempty"`
	UIDMappings     []bundle.LinuxIDMapping `json:"uid_mappings,omitempty"`
	GIDMappings     []bundle.LinuxIDMapping `json:"gid_mappings,omitempty"`
}

// containerEntry tracks runtime state for a supervised container.
type containerEntry struct {
	spec      ContainerSpec
	netNS     string
	ip        string
	bundleDir string
	rootFS    string
	restarts  int
	stopped   bool // intentionally stopped - don't restart
	logCancel context.CancelFunc
	checker   *health.Checker
	mu        sync.Mutex
}

// Supervisor manages the full lifecycle of containers.
type Supervisor struct {
	rt       *runtime.Runtime
	ovl      *overlay.Manager
	net      network.Backend
	imgStore *image.Store // optional: re-adopt orphans via ResolvePulledImage
	logColl  *logs.Collector
	log      *slog.Logger
	baseDir  string // /var/lib/nyxd

	dns        netdns.Backend
	netDriver  string
	dnsMode    string
	dnsGateway string

	mu         sync.RWMutex
	containers map[string]*containerEntry
	wg         sync.WaitGroup
}

// New constructs a Supervisor. net must implement [network.Backend]
// (typically native in-process networking or the CNI exec [network.Manager]).
// logColl may be nil (stdio is discarded and no log files are written).
// imgStore may be nil; when set, the supervisor can re-adopt running crun containers
// after restart using bundle nyxd-meta.json when supervisor JSON is missing.
// dns selects optional embedded DNS; pass nil for [netdns.Noop]. netDriver and dnsMode
// mirror nyxd -net-driver and -dns (used for compose EmbedDNS and registration policy).
// dnsGateway is the IPv4 bridge address written into resolv.conf (e.g. 10.88.0.1).
func New(rt *runtime.Runtime, ovl *overlay.Manager, net network.Backend, baseDir string, log *slog.Logger, logColl *logs.Collector, imgStore *image.Store, dns netdns.Backend, netDriver, dnsMode, dnsGateway string) *Supervisor {
	if dns == nil {
		dns = netdns.Noop{}
	}
	s := &Supervisor{
		rt:         rt,
		ovl:        ovl,
		net:        net,
		imgStore:   imgStore,
		logColl:    logColl,
		log:        log,
		baseDir:    baseDir,
		dns:        dns,
		netDriver:  strings.ToLower(strings.TrimSpace(netDriver)),
		dnsMode:    strings.ToLower(strings.TrimSpace(dnsMode)),
		dnsGateway: strings.TrimSpace(dnsGateway),
		containers: make(map[string]*containerEntry),
	}
	s.reconcilePersisted()
	s.reconcileCrunOrphans()
	return s
}

// Start launches a container according to its spec and supervises it.
func (s *Supervisor) Start(ctx context.Context, spec ContainerSpec) error {
	if spec.Healthcheck != nil {
		n := health.Normalize(*spec.Healthcheck)
		spec.Healthcheck = &n
	}
	s.mu.Lock()
	if _, exists := s.containers[spec.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("container %s already running", spec.ID)
	}

	entry := &containerEntry{spec: spec}
	s.containers[spec.ID] = entry
	s.mu.Unlock()

	fastExit, err := s.startOnce(ctx, entry)
	if err != nil {
		s.mu.Lock()
		delete(s.containers, spec.ID)
		s.mu.Unlock()
		return err
	}
	if fastExit {
		// Init exited with status 0 before we observed OCI "running" (e.g. default /bin/sh
		// with no stdin, or a very fast command). No supervisor loop; cleanup is done.
		s.mu.Lock()
		delete(s.containers, spec.ID)
		s.mu.Unlock()
		s.removePersistedState(spec.ID)
		return nil
	}

	if spec.Healthcheck != nil {
		rctx, rcancel := readinessWaitContext(ctx, spec.Healthcheck)
		err := health.WaitReady(rctx, s.log, *spec.Healthcheck, spec.ID, s.rt.Binary(), s.rt.RootDir())
		rcancel()
		if err != nil {
			s.abortAfterFailedReadiness(ctx, entry)
			s.mu.Lock()
			delete(s.containers, spec.ID)
			s.mu.Unlock()
			s.removePersistedState(spec.ID)
			return fmt.Errorf("readiness: %w", err)
		}
	}

	s.startHealthMonitor(ctx, entry)

	s.wg.Add(1)
	go s.supervise(ctx, entry)

	return nil
}

// StartSequential calls [Supervisor.Start] for each spec in order.
// Callers should pass specs ordered by dependencies (e.g. [compose.TopologicalOrder] mapped to specs)
// so dependents start after dependencies complete startup (including readiness probes).
func (s *Supervisor) StartSequential(ctx context.Context, specs []ContainerSpec) error {
	for _, sp := range specs {
		if err := s.Start(ctx, sp); err != nil {
			return fmt.Errorf("start %s: %w", sp.ID, err)
		}
	}
	return nil
}

// waitStoppedOrForceDelete waits for crun to report stopped; if that times out or fails,
// runs `crun delete --force` so a wedged foreground `crun run` cannot block shutdown forever.
func (s *Supervisor) waitStoppedOrForceDelete(waitCtx context.Context, id, op string) error {
	if err := s.rt.WaitStopped(waitCtx, id); err != nil {
		s.log.Warn("wait for crun stopped failed or timed out", "id", id, "op", op, "err", err)
		delCtx, delCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer delCancel()
		if err2 := s.rt.Delete(delCtx, id, true); err2 != nil {
			return fmt.Errorf("wait container stopped: %w; force delete: %v", err, err2)
		}
		s.log.Info("crun delete --force after wait timeout", "id", id, "op", op)
		return nil
	}
	return nil
}

// Stop gracefully stops a container.
func (s *Supervisor) Stop(ctx context.Context, id string) error {
	s.mu.RLock()
	entry, ok := s.containers[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("container %s not found", id)
	}

	entry.mu.Lock()
	entry.stopped = true
	entry.mu.Unlock()

	waitBudget := entry.spec.StopTimeout
	if waitBudget == 0 {
		waitBudget = defaultStopGrace
	}

	sig := "TERM"
	if cfg := entry.spec.ImageConfig; cfg != nil && cfg.Config.StopSignal != "" {
		sig = cfg.Config.StopSignal
	}

	s.log.Info("stopping container", "id", id, "signal", sig)

	killCtx, kcancel := context.WithTimeout(context.Background(), 20*time.Second)
	err := s.rt.Kill(killCtx, id, sig)
	kcancel()
	if err != nil {
		if runtime.CrunContainerAbsent(err) {
			s.log.Warn("crun stop: OCI state missing on disk", "id", id, "err", err)
		} else {
			s.log.Warn("graceful stop failed, forcing", "id", id, "err", err)
			killCtx2, kcancel2 := context.WithTimeout(context.Background(), 20*time.Second)
			err2 := s.rt.Kill(killCtx2, id, "KILL")
			kcancel2()
			if err2 != nil && !runtime.CrunContainerAbsent(err2) {
				entry.mu.Lock()
				entry.stopped = false
				entry.mu.Unlock()
				return fmt.Errorf("kill container: %w", err2)
			}
			if err2 != nil {
				s.log.Warn("crun KILL: OCI state missing on disk", "id", id, "err", err2)
			}
		}
	}

	// Foreground `crun run` (see startOnce) can stay attached to stdio pipes; without
	// tearing it down, `crun state` may never reach stopped and WaitStopped times out.
	s.resetLogIO(entry)

	waitCtx, wcancel := context.WithTimeout(context.Background(), waitBudget)
	defer wcancel()
	if err := s.waitStoppedOrForceDelete(waitCtx, id, "stop"); err != nil {
		entry.mu.Lock()
		entry.stopped = false
		entry.mu.Unlock()
		return err
	}
	s.log.Info("container stopped", "id", id)
	return nil
}

// Kill sends a signal (default KILL) and waits for crun to report stopped.
// Sets stopped=true so restart policies do not respawn the container.
func (s *Supervisor) Kill(_ context.Context, id string, signal string) error {
	if signal == "" {
		signal = "KILL"
	}
	s.mu.RLock()
	entry, ok := s.containers[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("container %s not found", id)
	}

	entry.mu.Lock()
	entry.stopped = true
	entry.mu.Unlock()

	killCtx, kcancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer kcancel()
	s.log.Info("kill signal to container", "id", id, "signal", signal)
	err := s.rt.Kill(killCtx, id, signal)
	if err != nil {
		if !runtime.CrunContainerAbsent(err) {
			entry.mu.Lock()
			entry.stopped = false
			entry.mu.Unlock()
			return fmt.Errorf("kill container: %w", err)
		}
		s.log.Warn("crun kill: OCI state missing on disk; cancelling foreground run", "id", id, "err", err)
	}

	// Unblock the supervisor's foreground `crun run` (CommandContext(logCtx)) so
	// runtime state can move to stopped and network teardown can proceed.
	s.resetLogIO(entry)

	waitCtx, wcancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer wcancel()
	if err := s.waitStoppedOrForceDelete(waitCtx, id, "kill"); err != nil {
		entry.mu.Lock()
		entry.stopped = false
		entry.mu.Unlock()
		return err
	}
	s.log.Info("container stopped after kill", "id", id, "signal", signal)
	return nil
}

// KillForRestart force-stops the workload without marking it intentionally stopped,
// so the supervisor loop can apply the restart policy after a health failure.
func (s *Supervisor) KillForRestart(ctx context.Context, id string) error {
	s.mu.RLock()
	entry, ok := s.containers[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("container %s not found", id)
	}

	s.resetLogIO(entry)

	killCtx, kcancel := context.WithTimeout(ctx, 20*time.Second)
	defer kcancel()
	s.log.Info("health restart: kill container", "id", id)
	err := s.rt.Kill(killCtx, id, "KILL")
	if err != nil && !runtime.CrunContainerAbsent(err) {
		return fmt.Errorf("kill container: %w", err)
	}

	waitCtx, wcancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer wcancel()
	if err := s.waitStoppedOrForceDelete(waitCtx, id, "health_restart"); err != nil {
		return err
	}
	s.log.Info("container stopped for health restart", "id", id)
	return nil
}

// Remove stops and removes a container and all its resources.
func (s *Supervisor) Remove(ctx context.Context, id string) error {
	s.mu.RLock()
	_, exists := s.containers[id]
	s.mu.RUnlock()
	if !exists {
		return fmt.Errorf("container %s not found", id)
	}

	if err := s.Stop(ctx, id); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		s.log.Warn("stop before remove", "id", id, "err", err)
	}

	s.rt.Delete(ctx, id, true) //nolint:errcheck

	s.mu.Lock()
	entry, ok := s.containers[id]
	delete(s.containers, id)
	s.mu.Unlock()

	if ok {
		s.cleanup(entry)
	}
	return nil
}

// IsBindSourceInUse reports whether any supervised container has an extra mount whose
// Source is dir or a path under dir (e.g. a named volume directory still referenced).
func (s *Supervisor) IsBindSourceInUse(dir string) bool {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return false
	}
	sep := string(filepath.Separator)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.containers {
		for _, m := range e.spec.ExtraMounts {
			src := filepath.Clean(m.Source)
			if src == dir || strings.HasPrefix(src, dir+sep) {
				return true
			}
		}
	}
	return false
}

// List returns IDs of all supervised containers.
func (s *Supervisor) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.containers))
	for id := range s.containers {
		ids = append(ids, id)
	}
	return ids
}

// ContainerInfo is a stable JSON shape for list/ps APIs.
type ContainerInfo struct {
	ID      string `json:"id"`
	ShortID string `json:"short_id"`
	Image   string `json:"image"`
	IP      string `json:"ip,omitempty"`
	Ports   string `json:"ports,omitempty"`
	Status  string `json:"status"`
}

func formatPortMappings(pm []network.PortMapping) string {
	if len(pm) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range pm {
		if i > 0 {
			b.WriteString(", ")
		}
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}
		fmt.Fprintf(&b, "0.0.0.0:%d->%d/%s", p.HostPort, p.ContainerPort, proto)
	}
	return b.String()
}

// ListInfo returns supervised containers with runtime status and network IP.
func (s *Supervisor) ListInfo(_ context.Context) []ContainerInfo {
	s.mu.RLock()
	type snap struct {
		id, img, ip, ports string
	}
	var snaps []snap
	for id, e := range s.containers {
		e.mu.Lock()
		snaps = append(snaps, snap{
			id:    id,
			img:   e.spec.Image,
			ip:    e.ip,
			ports: formatPortMappings(e.spec.PortMappings),
		})
		e.mu.Unlock()
	}
	s.mu.RUnlock()

	sort.Slice(snaps, func(i, j int) bool { return snaps[i].id < snaps[j].id })

	// Do not use the HTTP request context for crun: if the client disconnects,
	// we still want accurate status instead of "unknown" from a cancelled state call.
	stateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out := make([]ContainerInfo, 0, len(snaps))
	for _, sn := range snaps {
		info := ContainerInfo{
			ID:      sn.id,
			ShortID: DisplayID(sn.id),
			Image:   sn.img,
			IP:      sn.ip,
			Ports:   sn.ports,
			Status:  "unknown",
		}
		if st, err := s.rt.State(stateCtx, sn.id); err == nil && st != nil {
			info.Status = st.Status
		} else if err != nil && runtime.CrunContainerAbsent(err) {
			info.Status = "absent"
		}
		out = append(out, info)
	}
	return out
}

// BaseDir returns the daemon data directory (e.g. /var/lib/nyxd).
func (s *Supervisor) BaseDir() string { return s.baseDir }

// ImageRefsInUse returns image references held by supervised containers (exact ContainerSpec.Image strings).
func (s *Supervisor) ImageRefsInUse() map[string]struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]struct{})
	for _, e := range s.containers {
		if r := strings.TrimSpace(e.spec.Image); r != "" {
			out[r] = struct{}{}
		}
	}
	return out
}

// Shutdown stops all containers gracefully.
func (s *Supervisor) Shutdown(ctx context.Context) {
	s.mu.RLock()
	ids := make([]string, 0, len(s.containers))
	for id := range s.containers {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			stopCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			s.Stop(stopCtx, id) //nolint:errcheck
		}(id)
	}
	wg.Wait()
	s.wg.Wait()
	s.dns.Shutdown()
}

func readinessWaitContext(parent context.Context, cfg *health.Config) (context.Context, context.CancelFunc) {
	if cfg == nil {
		return parent, func() {}
	}
	tick := 500 * time.Millisecond
	attempts := cfg.Retries + 8
	if attempts < 10 {
		attempts = 10
	}
	budget := cfg.StartPeriod + time.Duration(attempts)*(cfg.Timeout+tick)
	if budget < 45*time.Second {
		budget = 45 * time.Second
	}
	if budget > 15*time.Minute {
		budget = 15 * time.Minute
	}
	return context.WithTimeout(parent, budget)
}

func (s *Supervisor) abortAfterFailedReadiness(ctx context.Context, e *containerEntry) {
	id := e.spec.ID
	log := s.log.With("id", id)
	log.Warn("tearing down container after readiness failure")
	s.resetLogIO(e)
	killCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.rt.Kill(killCtx, id, "KILL"); err != nil && !runtime.CrunContainerAbsent(err) {
		log.Warn("readiness abort kill", "err", err)
	}
	waitCtx, wcancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer wcancel()
	if err := s.waitStoppedOrForceDelete(waitCtx, id, "readiness_abort"); err != nil {
		log.Warn("readiness abort wait", "err", err)
	}
	s.teardownNetwork(ctx, id)
	s.ovl.Remove(id) //nolint:errcheck
	_ = s.rt.Delete(ctx, id, true)
}

func (s *Supervisor) startHealthMonitor(parent context.Context, e *containerEntry) {
	if e.spec.Healthcheck == nil {
		return
	}
	hc := *e.spec.Healthcheck
	if hc.Type == health.TypeNone || hc.Type == "" {
		return
	}
	onUnhealthy := func(id string) {
		tmp := &containerEntry{spec: e.spec}
		if !s.shouldRestart(tmp, 1) {
			s.log.Info("healthcheck unhealthy; restart policy does not restart", "id", id, "policy", e.spec.RestartPolicy)
			return
		}
		go func(id string) {
			killCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := s.KillForRestart(killCtx, id); err != nil {
				s.log.Warn("health-driven restart failed", "id", id, "err", err)
			}
		}(id)
	}
	chk := health.New(e.spec.ID, hc, s.rt.Binary(), s.rt.RootDir(), s.log, onUnhealthy)
	e.mu.Lock()
	e.checker = chk
	e.mu.Unlock()
	chk.Start(parent)
}

func (e *containerEntry) stopHealth() {
	e.mu.Lock()
	c := e.checker
	e.checker = nil
	e.mu.Unlock()
	if c != nil {
		c.Stop()
	}
}

// ─── Internal ──────────────────────────────────────────────────────────────────

// startOnce performs a single container start: overlay → network → bundle → crun.
// If the init process exits with code 0 before the supervisor observes OCI status "running",
// fastExit is true and runtime/network/overlay are already torn down; the caller must not
// start supervise (Start) or must treat the restart as an immediate exit (supervise).
func (s *Supervisor) startOnce(ctx context.Context, e *containerEntry) (fastExit bool, err error) {
	spec := e.spec
	log := s.log.With("id", spec.ID)

	// 1. Overlay: mount rootfs.
	rootFS, err := s.ovl.Prepare(spec.ID, spec.BlobPaths, spec.ImageConfig)
	if err != nil {
		return false, fmt.Errorf("overlay prepare: %w", err)
	}
	e.rootFS = rootFS

	// 2. Network namespace + host networking (native or CNI plugins).
	nsPath, err := network.CreateNetNS(spec.ID)
	if err != nil {
		s.ovl.Remove(spec.ID) //nolint:errcheck
		return false, fmt.Errorf("netns: %w", err)
	}
	e.netNS = nsPath

	var netOpts *network.SetupOptions
	if spec.ComposeInternalNet {
		netOpts = &network.SetupOptions{Internal: true}
	}
	ip, err := s.net.Setup(ctx, spec.ID, nsPath, spec.PortMappings, netOpts)
	if err != nil {
		network.DeleteNetNS(spec.ID) //nolint:errcheck
		s.ovl.Remove(spec.ID)        //nolint:errcheck
		return false, fmt.Errorf("network setup: %w", err)
	}
	e.ip = ip
	log.Info("network assigned", "ip", ip)

	// 3. Generate OCI bundle.
	bundleDir := fmt.Sprintf("%s/bundles/%s", s.baseDir, spec.ID)
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		s.teardownNetwork(ctx, spec.ID)
		s.ovl.Remove(spec.ID) //nolint:errcheck
		return false, fmt.Errorf("bundle dir: %w", err)
	}
	resolvPath, err := s.writeBundleResolv(bundleDir, spec)
	if err != nil {
		s.teardownNetwork(ctx, spec.ID)
		s.ovl.Remove(spec.ID) //nolint:errcheck
		return false, fmt.Errorf("bundle resolv: %w", err)
	}
	_, err = bundle.Generate(bundleDir, bundle.Options{
		ContainerID:     spec.ID,
		RootFS:          rootFS,
		NetNS:           nsPath,
		ImageConfig:     spec.ImageConfig,
		Env:             spec.Env,
		Args:            spec.Args,
		WorkDir:         spec.WorkDir,
		User:            spec.User,
		Resources:       spec.Resources,
		ReadOnly:        spec.ReadOnly,
		Hostname:        spec.Hostname,
		ExtraMounts:     spec.ExtraMounts,
		ResolvConfPath:  resolvPath,
		SeccompProfile:  spec.SeccompProfile,
		CapAdd:          spec.CapAdd,
		CapDrop:         spec.CapDrop,
		Privileged:      spec.Privileged,
		NoNewPrivileges: spec.NoNewPrivileges,
		UIDMappings:     spec.UIDMappings,
		GIDMappings:     spec.GIDMappings,
	})
	if err != nil {
		s.teardownNetwork(ctx, spec.ID)
		s.ovl.Remove(spec.ID) //nolint:errcheck
		return false, fmt.Errorf("bundle: %w", err)
	}
	e.bundleDir = bundleDir

	meta := bundleRunMeta{
		Image:    spec.Image,
		IP:       ip,
		Env:      spec.Env,
		Args:     spec.Args,
		Hostname: spec.Hostname,
		Restart:  string(spec.RestartPolicy),
		Publish:  portMappingsToPublishStrings(spec.PortMappings),
	}
	if err := writeBundleRunMeta(bundleDir, meta); err != nil {
		log.Warn("write bundle meta", "err", err)
	}

	s.ensureCrunIDFreeForFreshStart(ctx, spec.ID, log)

	// 4. crun run (foreground): stdio piped to log collector JSONL files.
	e.mu.Lock()
	if e.logCancel != nil {
		e.logCancel()
		e.logCancel = nil
	}
	logCtx, logCancel := context.WithCancel(ctx)
	e.logCancel = logCancel
	e.mu.Unlock()

	soR, soW, err := os.Pipe()
	if err != nil {
		logCancel()
		s.teardownNetwork(ctx, spec.ID)
		s.ovl.Remove(spec.ID) //nolint:errcheck
		return false, fmt.Errorf("stdio pipe: %w", err)
	}
	seR, seW, err := os.Pipe()
	if err != nil {
		_ = soR.Close()
		_ = soW.Close()
		logCancel()
		s.teardownNetwork(ctx, spec.ID)
		s.ovl.Remove(spec.ID) //nolint:errcheck
		return false, fmt.Errorf("stdio pipe: %w", err)
	}

	if s.logColl != nil {
		go func() {
			defer soR.Close()
			s.logColl.Stream(logCtx, spec.ID, "stdout", soR)
		}()
		go func() {
			defer seR.Close()
			s.logColl.Stream(logCtx, spec.ID, "stderr", seR)
		}()
	} else {
		go func() {
			defer soR.Close()
			_, _ = io.Copy(io.Discard, soR)
		}()
		go func() {
			defer seR.Close()
			_, _ = io.Copy(io.Discard, seR)
		}()
	}

	runErr := make(chan error, 1)
	go func() {
		defer soW.Close()
		defer seW.Close()
		runErr <- s.rt.RunForeground(logCtx, spec.ID, bundleDir, soW, seW)
	}()

	startDeadline := time.NewTimer(30 * time.Second)
	defer startDeadline.Stop()
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case err := <-runErr:
			if err != nil {
				logCancel()
				s.teardownNetwork(ctx, spec.ID)
				s.ovl.Remove(spec.ID) //nolint:errcheck
				return false, fmt.Errorf("crun run: %w", err)
			}
			// Foreground crun returned while we never ticked "running": init exited with 0
			// (short-lived workload or shell with no stdin). Same teardown as a normal exit.
			s.finishForegroundFastExit(ctx, e)
			return true, nil
		case <-tick.C:
			st, err2 := s.rt.State(ctx, spec.ID)
			if err2 == nil && st != nil && st.Status == "running" {
				log.Info("container started", "image", spec.Image, "ip", ip)
				s.registerEmbeddedDNS(spec, ip)
				if err := s.persistContainerEntry(e); err != nil {
					log.Warn("persist supervisor state", "err", err)
				}
				return false, nil
			}
		case <-startDeadline.C:
			logCancel()
			s.teardownNetwork(ctx, spec.ID)
			s.ovl.Remove(spec.ID) //nolint:errcheck
			return false, fmt.Errorf("crun run: timeout waiting for running state")
		}
	}
}

// ensureCrunIDFreeForFreshStart removes OCI state for id if crun still has it.
// After an unclean shutdown the supervisor may have no record while crun does,
// which makes `crun run` fail with "already exists" and leaves `nyx ps` empty.
func (s *Supervisor) ensureCrunIDFreeForFreshStart(ctx context.Context, id string, log *slog.Logger) {
	_, err := s.rt.State(ctx, id)
	if err != nil {
		if runtime.CrunContainerAbsent(err) {
			return
		}
		log.Debug("crun state probe before run", "id", id, "err", err)
		return
	}
	log.Warn("crun id already in runtime; deleting stale state before run", "id", id)
	if err := s.rt.Delete(ctx, id, true); err != nil && !runtime.CrunContainerAbsent(err) {
		log.Warn("stale crun delete before run", "id", id, "err", err)
	}
}

// finishForegroundFastExit tears down after crun's foreground `run` returns with exit
// status 0 before we observed OCI "running" (race with the status ticker).
func (s *Supervisor) finishForegroundFastExit(ctx context.Context, e *containerEntry) {
	s.resetLogIO(e)
	s.teardownNetwork(ctx, e.spec.ID)
	s.ovl.Remove(e.spec.ID)            //nolint:errcheck
	s.rt.Delete(ctx, e.spec.ID, false) //nolint:errcheck
}

// supervise watches a container and applies restart policy.
func (s *Supervisor) supervise(ctx context.Context, e *containerEntry) {
	defer s.wg.Done()

	log := s.log.With("id", e.spec.ID)

	for {
		// Wait for container exit.
		exitCode, err := s.rt.WaitForExit(ctx, e.spec.ID)
		if err != nil {
			if ctx.Err() != nil {
				return // daemon shutting down
			}
			log.Warn("wait error", "err", err)
		}

		e.stopHealth()

		e.mu.Lock()
		intentionallyStopped := e.stopped
		e.mu.Unlock()

		log.Info("container exited", "exitCode", exitCode, "intentional", intentionallyStopped)

		s.resetLogIO(e)

		// Cleanup network + overlay.
		s.teardownNetwork(ctx, e.spec.ID)
		s.ovl.Remove(e.spec.ID) //nolint:errcheck

		if intentionallyStopped {
			s.rt.Delete(ctx, e.spec.ID, false) //nolint:errcheck
			s.mu.Lock()
			delete(s.containers, e.spec.ID)
			s.mu.Unlock()
			s.removePersistedState(e.spec.ID)
			return
		}

		// Apply restart policy.
		if !s.shouldRestart(e, exitCode) {
			log.Info("not restarting", "policy", e.spec.RestartPolicy, "restarts", e.restarts)
			s.rt.Delete(ctx, e.spec.ID, false) //nolint:errcheck
			s.mu.Lock()
			delete(s.containers, e.spec.ID)
			s.mu.Unlock()
			s.removePersistedState(e.spec.ID)
			return
		}

		e.restarts++
		delay := backoff(e.restarts)
		log.Info("restarting", "attempt", e.restarts, "delay", delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

	outer:
		for {
			fastExit, err := s.startOnce(ctx, e)
			if err != nil {
				log.Error("restart failed", "err", err)
				time.Sleep(5 * time.Second)
				continue outer
			}
			if !fastExit {
				continue outer
			}
			// Init exited with 0 before "running" again; cleanup already done in startOnce.
			exitCode := 0
			e.mu.Lock()
			intentionallyStopped := e.stopped
			e.mu.Unlock()
			log.Info("container exited", "exitCode", exitCode, "intentional", intentionallyStopped)
			if intentionallyStopped {
				s.mu.Lock()
				delete(s.containers, e.spec.ID)
				s.mu.Unlock()
				s.removePersistedState(e.spec.ID)
				return
			}
			if !s.shouldRestart(e, exitCode) {
				log.Info("not restarting", "policy", e.spec.RestartPolicy, "restarts", e.restarts)
				s.mu.Lock()
				delete(s.containers, e.spec.ID)
				s.mu.Unlock()
				s.removePersistedState(e.spec.ID)
				return
			}
			e.restarts++
			delay := backoff(e.restarts)
			log.Info("restarting", "attempt", e.restarts, "delay", delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}
}

func (s *Supervisor) shouldRestart(e *containerEntry, exitCode int) bool {
	spec := e.spec

	if spec.MaxRestarts > 0 && e.restarts >= spec.MaxRestarts {
		return false
	}

	switch spec.RestartPolicy {
	case RestartAlways:
		return true
	case RestartOnFailure:
		return exitCode != 0
	case RestartUnlessStopped:
		return true
	case RestartNever, "":
		return false
	default:
		return false
	}
}

func (s *Supervisor) teardownNetwork(ctx context.Context, id string) {
	nsPath := fmt.Sprintf("/run/nyxd/netns/%s", id)
	s.mu.RLock()
	entry, ok := s.containers[id]
	s.mu.RUnlock()
	if ok {
		s.dnsDeregisterForSpec(entry.spec)
	}
	if err := s.net.Teardown(ctx, id, nsPath); err != nil {
		s.log.Warn("network teardown", "id", id, "err", err)
	}
	if err := network.DeleteNetNS(id); err != nil {
		s.log.Warn("netns delete", "id", id, "err", err)
	}
}

func (s *Supervisor) cleanup(e *containerEntry) {
	e.stopHealth()
	s.resetLogIO(e)
	if s.logColl != nil {
		s.logColl.Remove(e.spec.ID)
	}
	if e.netNS != "" {
		s.dnsDeregisterForSpec(e.spec)
		s.net.Teardown(context.Background(), e.spec.ID, e.netNS) //nolint:errcheck
		network.DeleteNetNS(e.spec.ID)                           //nolint:errcheck
	}
	s.ovl.Remove(e.spec.ID) //nolint:errcheck
	removeBundleMeta(e.bundleDir)
	s.removePersistedState(e.spec.ID)
}

func (s *Supervisor) resetLogIO(e *containerEntry) {
	e.mu.Lock()
	c := e.logCancel
	e.logCancel = nil
	e.mu.Unlock()
	if c != nil {
		c()
	}
}

// backoff returns an exponential backoff delay capped at 30s, with small jitter.
func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * time.Duration(attempt) * 100 * time.Millisecond
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	jitter := time.Duration(rand.Int64N(int64(d/10 + 1))) // up to ~10% extra
	return d + jitter
}
