package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/pkg/oci"
)

// StartComposeUp applies desired compose specs in order (same contract as
// [Supervisor.StartSequential]): dependencies first. For each service, if the
// container is already running with an equivalent spec, it is left alone; if the
// spec changed, the container is stopped and recreated; if missing, it is started.
func (s *Supervisor) StartComposeUp(ctx context.Context, specs []ContainerSpec) error {
	_, err := s.StartComposeUpWith(ctx, specs, ComposeUpOptions{})
	return err
}

// Recreate policies for compose up (ComposeUpOptions.Recreate).
const (
	RecreateChanged = ""      // recreate a running container whose spec changed
	RecreateForce   = "force" // recreate every running container
	RecreateNever   = "never" // keep running containers as they are
)

// What compose up did with one service (ComposeResult.Action).
const (
	ComposeStarted   = "started"
	ComposeRecreated = "recreated"
	ComposeUnchanged = "up to date"
	ComposeKept      = "kept"
)

// ComposeResult is what compose up did with one container, and why.
type ComposeResult struct {
	ID      string `json:"id"`
	Service string `json:"service,omitempty"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
}

// depends_on conditions compose up waits for (the Compose spec's names).
// service_started needs no wait: dependencies start first.
const (
	DependsOnHealthy   = "service_healthy"
	DependsOnCompleted = "service_completed_successfully"
)

// DefaultComposeWaitTimeout bounds all depends_on waits of one compose up.
const DefaultComposeWaitTimeout = 5 * time.Minute

// dependencyPoll is how often a depends_on wait re-checks its dependency.
var dependencyPoll = time.Second

// ComposeDependency is one depends_on entry, by container id.
type ComposeDependency struct {
	ID        string
	Condition string
}

// ComposeUpOptions are the depends_on waits for [Supervisor.StartComposeUpWith].
type ComposeUpOptions struct {
	// Deps maps a container id to the dependencies it waits for before starting.
	Deps map[string][]ComposeDependency
	// WaitTimeout bounds all waits together; 0 means DefaultComposeWaitTimeout.
	WaitTimeout time.Duration
	// Recreate is RecreateChanged (default), RecreateForce or RecreateNever.
	Recreate string
}

// StartComposeUpWith is [Supervisor.StartComposeUp] that also honours
// depends_on conditions: before each container starts, it waits until every
// service_healthy dependency passes its health check and every
// service_completed_successfully dependency has exited 0. A failed or timed
// out wait stops the rest of the stack from starting.
func (s *Supervisor) StartComposeUpWith(ctx context.Context, specs []ContainerSpec, opts ComposeUpOptions) ([]ComposeResult, error) {
	var results []ComposeResult
	timeout := opts.WaitTimeout
	if timeout <= 0 {
		timeout = DefaultComposeWaitTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, sp := range specs {
		for _, dep := range opts.Deps[sp.ID] {
			if err := s.waitDependency(waitCtx, dep); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					err = fmt.Errorf("%s: not met within %s (compose up --wait-timeout)", dep.Condition, timeout)
				}
				return results, fmt.Errorf("start %s: dependency %s: %w", sp.ID, dep.ID, err)
			}
		}
		res, err := s.ensureComposeContainer(ctx, sp, opts.Recreate)
		if err != nil {
			return results, fmt.Errorf("start %s: %w", sp.ID, err)
		}
		results = append(results, res)
	}
	return results, nil
}

// waitDependency blocks until dep meets its condition.
func (s *Supervisor) waitDependency(ctx context.Context, dep ComposeDependency) error {
	var check func(context.Context, string) (bool, error)
	switch dep.Condition {
	case DependsOnHealthy:
		check = s.dependencyHealthy
	case DependsOnCompleted:
		check = s.dependencyCompleted
	default:
		return nil
	}
	t := time.NewTicker(dependencyPoll)
	defer t.Stop()
	for {
		ok, err := check(ctx, dep.ID)
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// dependencyHealthy reports whether id is running and healthy. The checker's
// status is used when it has one; until its first check (a container just
// started, or re-adopted after a daemon restart) a probe decides.
func (s *Supervisor) dependencyHealthy(ctx context.Context, id string) (bool, error) {
	s.mu.RLock()
	e, ok := s.containers[id]
	s.mu.RUnlock()
	if !ok {
		if rec, found := s.readExited(id); found {
			return false, fmt.Errorf("exited with code %d before becoming healthy", rec.ExitCode)
		}
		return false, fmt.Errorf("not running")
	}
	e.mu.Lock()
	chk, spec := e.checker, e.spec
	e.mu.Unlock()
	if spec.Healthcheck == nil || spec.Healthcheck.Type == health.TypeNone || spec.Healthcheck.Type == "" {
		return false, fmt.Errorf("has no healthcheck")
	}
	if chk != nil && chk.Status() == health.StatusHealthy {
		return true, nil
	}
	pctx, cancel := context.WithTimeout(ctx, spec.Healthcheck.Timeout+time.Second)
	defer cancel()
	return health.Probe(pctx, *spec.Healthcheck, id, s.rt.Binary(), s.rt.RootDir()) == nil, nil
}

// dependencyCompleted reports whether id has exited 0. A non-zero exit is an
// error; a restart policy that restarts it keeps the wait going.
func (s *Supervisor) dependencyCompleted(_ context.Context, id string) (bool, error) {
	if s.Running(id) {
		return false, nil
	}
	rec, ok := s.readExited(id)
	if !ok {
		return false, fmt.Errorf("exited, exit code unknown")
	}
	if rec.ExitCode != 0 {
		return false, fmt.Errorf("exited with code %d", rec.ExitCode)
	}
	return true, nil
}

func (s *Supervisor) ensureComposeContainer(ctx context.Context, want ContainerSpec, recreate string) (ComposeResult, error) {
	res := ComposeResult{ID: want.ID, Service: want.ComposeService}
	if want.Healthcheck != nil {
		n := health.Normalize(*want.Healthcheck)
		want.Healthcheck = &n
	}

	s.mu.RLock()
	entry, running := s.containers[want.ID]
	var cur ContainerSpec
	if running {
		cur = entry.spec
		if cur.Healthcheck != nil {
			n := health.Normalize(*cur.Healthcheck)
			cur.Healthcheck = &n
		}
	}
	s.mu.RUnlock()

	if !running {
		res.Action = ComposeStarted
		return res, s.Start(ctx, want)
	}
	changed := !bytes.Equal(composeComparableJSON(cur), composeComparableJSON(want))
	switch {
	case recreate == RecreateForce:
		res.Reason = "--force-recreate"
	case !changed:
		res.Action = ComposeUnchanged
		return res, nil
	case recreate == RecreateNever:
		res.Action, res.Reason = ComposeKept, changeReason(cur, want)+", --no-recreate"
		return res, nil
	default:
		res.Reason = changeReason(cur, want)
	}
	res.Action = ComposeRecreated
	if err := s.Stop(ctx, want.ID); err != nil {
		return res, fmt.Errorf("recreate stop: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := s.waitContainerAbsent(waitCtx, want.ID); err != nil {
		return res, fmt.Errorf("recreate wait: %w", err)
	}
	return res, s.Start(ctx, want)
}

// changeReason says what differs between a running spec and the wanted one:
// the image (another reference or other layers, e.g. after compose pull) or
// the rest of the service's configuration.
func changeReason(cur, want ContainerSpec) string {
	if cur.Image != want.Image || !slices.EqualFunc(cur.ManifestLayers, want.ManifestLayers,
		func(a, b oci.Descriptor) bool { return a.Digest == b.Digest }) {
		return "image changed"
	}
	return "config changed"
}

func (s *Supervisor) waitContainerAbsent(ctx context.Context, id string) error {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		s.mu.RLock()
		_, ok := s.containers[id]
		s.mu.RUnlock()
		if !ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("container %s still tracked after stop: %w", id, ctx.Err())
		case <-t.C:
		}
	}
}

// composeComparableJSON returns a stable JSON blob of fields that define the
// workload for compose reconcile (image/layers, command, mounts, caps, etc.).
func composeComparableJSON(s ContainerSpec) []byte {
	type portKey struct {
		Host, Container int
		Proto           string
	}
	ports := make([]portKey, 0, len(s.PortMappings))
	for _, p := range s.PortMappings {
		ports = append(ports, portKey{p.HostPort, p.ContainerPort, p.Protocol})
	}
	slices.SortFunc(ports, func(a, b portKey) int {
		if a.Container != b.Container {
			return a.Container - b.Container
		}
		if a.Host != b.Host {
			return a.Host - b.Host
		}
		return strings.Compare(a.Proto, b.Proto)
	})

	env := append([]string(nil), s.Env...)
	slices.Sort(env)

	args := append([]string(nil), s.Args...)

	capAdd := append([]string(nil), s.CapAdd...)
	slices.Sort(capAdd)
	capDrop := append([]string(nil), s.CapDrop...)
	slices.Sort(capDrop)

	mounts := mountsComparable(s.ExtraMounts)

	layers := make([]string, 0, len(s.ManifestLayers))
	for _, d := range s.ManifestLayers {
		layers = append(layers, d.Digest)
	}

	uidMaps := append([]bundle.LinuxIDMapping(nil), s.UIDMappings...)
	slices.SortFunc(uidMaps, func(a, b bundle.LinuxIDMapping) int {
		if a.ContainerID != b.ContainerID {
			return int(a.ContainerID) - int(b.ContainerID)
		}
		return int(a.HostID) - int(b.HostID)
	})
	gidMaps := append([]bundle.LinuxIDMapping(nil), s.GIDMappings...)
	slices.SortFunc(gidMaps, func(a, b bundle.LinuxIDMapping) int {
		if a.ContainerID != b.ContainerID {
			return int(a.ContainerID) - int(b.ContainerID)
		}
		return int(a.HostID) - int(b.HostID)
	})

	var user *bundle.User
	if s.User != nil {
		u := *s.User
		user = &u
	}
	var hc *health.Config
	if s.Healthcheck != nil {
		h := *s.Healthcheck
		hc = &h
	}
	var res *bundle.Resources
	if s.Resources != nil {
		r := *s.Resources
		res = &r
	}

	dns := append([]string(nil), s.DNS...)
	slices.Sort(dns)
	rlimits := append([]bundle.Rlimit(nil), s.Rlimits...)
	slices.SortFunc(rlimits, func(a, b bundle.Rlimit) int {
		return strings.Compare(a.Type, b.Type)
	})

	key := struct {
		ID                 string                  `json:"id"`
		Image              string                  `json:"image"`
		LayerDigests       []string                `json:"layer_digests"`
		Env                []string                `json:"env"`
		Args               []string                `json:"args"`
		WorkDir            string                  `json:"workdir"`
		User               *bundle.User            `json:"user,omitempty"`
		Ports              []portKey               `json:"ports"`
		ReadOnly           bool                    `json:"read_only"`
		Hostname           string                  `json:"hostname"`
		RestartPolicy      RestartPolicy           `json:"restart"`
		MaxRestarts        int                     `json:"max_restarts"`
		StopTimeout        time.Duration           `json:"stop_timeout"`
		Healthcheck        *health.Config          `json:"healthcheck,omitempty"`
		Mounts             []bundle.Mount          `json:"mounts"`
		EmbedDNS           bool                    `json:"embed_dns"`
		DNS                []string                `json:"dns,omitempty"`
		ComposeInternalNet bool                    `json:"compose_internal_net"`
		NetworkBridge      string                  `json:"network_bridge,omitempty"`
		NetworkSubnet      string                  `json:"network_subnet,omitempty"`
		NetworkGateway     string                  `json:"network_gateway,omitempty"`
		Resources          *bundle.Resources       `json:"resources,omitempty"`
		Privileged         bool                    `json:"privileged"`
		SeccompProfile     string                  `json:"seccomp_profile"`
		ApparmorProfile    string                  `json:"apparmor_profile,omitempty"`
		CapAdd             []string                `json:"cap_add"`
		CapDrop            []string                `json:"cap_drop"`
		NoNewPrivileges    *bool                   `json:"no_new_privileges,omitempty"`
		Rlimits            []bundle.Rlimit         `json:"rlimits,omitempty"`
		UIDMappings        []bundle.LinuxIDMapping `json:"uid_mappings,omitempty"`
		GIDMappings        []bundle.LinuxIDMapping `json:"gid_mappings,omitempty"`
	}{
		ID:                 s.ID,
		Image:              s.Image,
		LayerDigests:       layers,
		Env:                env,
		Args:               args,
		WorkDir:            s.WorkDir,
		User:               user,
		Ports:              ports,
		ReadOnly:           s.ReadOnly,
		Hostname:           s.Hostname,
		RestartPolicy:      s.RestartPolicy,
		MaxRestarts:        s.MaxRestarts,
		StopTimeout:        s.StopTimeout,
		Healthcheck:        hc,
		Mounts:             mounts,
		EmbedDNS:           s.EmbedDNS,
		DNS:                dns,
		ComposeInternalNet: s.ComposeInternalNet,
		NetworkBridge:      s.NetworkBridge,
		NetworkSubnet:      s.NetworkSubnet,
		NetworkGateway:     s.NetworkGateway,
		Resources:          res,
		Privileged:         s.Privileged,
		SeccompProfile:     s.SeccompProfile,
		ApparmorProfile:    s.ApparmorProfile,
		CapAdd:             capAdd,
		CapDrop:            capDrop,
		NoNewPrivileges:    s.NoNewPrivileges,
		Rlimits:            rlimits,
		UIDMappings:        uidMaps,
		GIDMappings:        gidMaps,
	}
	b, err := json.Marshal(key)
	if err != nil {
		return nil
	}
	return b
}

func mountsComparable(ms []bundle.Mount) []bundle.Mount {
	out := append([]bundle.Mount(nil), ms...)
	for i := range out {
		opts := append([]string(nil), out[i].Options...)
		slices.Sort(opts)
		out[i].Options = opts
	}
	slices.SortFunc(out, func(a, b bundle.Mount) int {
		if c := strings.Compare(a.Destination, b.Destination); c != 0 {
			return c
		}
		if c := strings.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		if c := strings.Compare(a.Type, b.Type); c != 0 {
			return c
		}
		return strings.Compare(strings.Join(a.Options, "\x00"), strings.Join(b.Options, "\x00"))
	})
	return out
}

func composeSpecsEqual(a, b ContainerSpec) bool {
	return bytes.Equal(composeComparableJSON(a), composeComparableJSON(b))
}
