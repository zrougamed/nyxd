package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/health"
)

// StartComposeUp applies desired compose specs in order (same contract as
// [Supervisor.StartSequential]): dependencies first. For each service, if the
// container is already running with an equivalent spec, it is left alone; if the
// spec changed, the container is stopped and recreated; if missing, it is started.
func (s *Supervisor) StartComposeUp(ctx context.Context, specs []ContainerSpec) error {
	for _, sp := range specs {
		if err := s.ensureComposeContainer(ctx, sp); err != nil {
			return fmt.Errorf("start %s: %w", sp.ID, err)
		}
	}
	return nil
}

func (s *Supervisor) ensureComposeContainer(ctx context.Context, want ContainerSpec) error {
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
		return s.Start(ctx, want)
	}
	if bytes.Equal(composeComparableJSON(cur), composeComparableJSON(want)) {
		return nil
	}
	if err := s.Stop(ctx, want.ID); err != nil {
		return fmt.Errorf("recreate stop: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := s.waitContainerAbsent(waitCtx, want.ID); err != nil {
		return fmt.Errorf("recreate wait: %w", err)
	}
	return s.Start(ctx, want)
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
		ComposeInternalNet bool                    `json:"compose_internal_net"`
		Resources          *bundle.Resources       `json:"resources,omitempty"`
		Privileged         bool                    `json:"privileged"`
		SeccompProfile     string                  `json:"seccomp_profile"`
		CapAdd             []string                `json:"cap_add"`
		CapDrop            []string                `json:"cap_drop"`
		NoNewPrivileges    *bool                   `json:"no_new_privileges,omitempty"`
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
		ComposeInternalNet: s.ComposeInternalNet,
		Resources:          res,
		Privileged:         s.Privileged,
		SeccompProfile:     s.SeccompProfile,
		CapAdd:             capAdd,
		CapDrop:            capDrop,
		NoNewPrivileges:    s.NoNewPrivileges,
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
