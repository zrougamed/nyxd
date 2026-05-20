package compose

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/bundle"
	"github.com/zrougamed/nyxd/internal/health"
	"github.com/zrougamed/nyxd/internal/image"
	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/supervisor"
	"github.com/zrougamed/nyxd/pkg/oci"
)

// UpMeta is filesystem context for resolving compose volumes and storing named volumes.
type UpMeta struct {
	ComposeDir string
	Project    string
	DataDir    string
}

// BuildContainerSpecs resolves images (pull when missing), maps each service in
// dependency order to a [supervisor.ContainerSpec], and returns the slice suitable
// for [supervisor.Supervisor.StartComposeUp] (skips unchanged services, recreates changed ones).
//
// netDriver and dnsMode mirror nyxd flags (-net-driver, -dns): when both are cni+auto,
// EmbedDNS is set per service for internal-only compose networks (see ServiceUsesOnlyInternalNetworks).
// ComposeInternalNet is set the same way; the native driver enforces it with nftables egress drops.
func BuildContainerSpecs(ctx context.Context, stack *Stack, meta UpMeta, store *image.Store, netDriver, dnsMode string) ([]supervisor.ContainerSpec, error) {
	if store == nil {
		return nil, fmt.Errorf("image store is required")
	}
	order := TopologicalOrder(stack)
	seenImg := make(map[string]struct{})
	for _, name := range order {
		svc := stack.Services[name]
		img := strings.TrimSpace(svc.Image)
		if _, dup := seenImg[img]; dup {
			continue
		}
		seenImg[img] = struct{}{}
		if _, _, _, err := store.ResolvePulledImage(img); err != nil {
			if _, err := store.PullWithProgress(ctx, img, pullAuth(&svc), nil); err != nil {
				return nil, fmt.Errorf("service %q pull %q: %w", name, img, err)
			}
		}
	}

	out := make([]supervisor.ContainerSpec, 0, len(order))
	for _, name := range order {
		svc := stack.Services[name]
		img := strings.TrimSpace(svc.Image)
		m, cfg, paths, err := store.ResolvePulledImage(img)
		if err != nil {
			return nil, fmt.Errorf("service %q resolve %q: %w", name, img, err)
		}

		mounts, err := ServiceExtraMounts(&svc, stack, meta.ComposeDir, meta.Project, meta.DataDir)
		if err != nil {
			return nil, fmt.Errorf("service %q volumes: %w", name, err)
		}

		var portMaps []network.PortMapping
		for _, p := range svc.Ports {
			pm, err := network.ParseDockerPublish(p)
			if err != nil {
				return nil, fmt.Errorf("service %q ports %q: %w", name, p, err)
			}
			portMaps = append(portMaps, pm)
		}

		var hc *health.Config
		if svc.Healthcheck != nil {
			hc, err = HealthcheckToConfig(svc.Healthcheck)
			if err != nil {
				return nil, fmt.Errorf("service %q healthcheck: %w", name, err)
			}
		}

		u, err := parseComposeUser(svc.User)
		if err != nil {
			return nil, fmt.Errorf("service %q user: %w", name, err)
		}

		embedDNS := false
		internalNet := ServiceUsesOnlyInternalNetworks(stack, svc)
		nd := strings.ToLower(strings.TrimSpace(netDriver))
		dm := strings.ToLower(strings.TrimSpace(dnsMode))
		if dm == "" {
			dm = "auto"
		}
		if nd == "cni" && (dm == "auto" || dm == "embedded") {
			if dm == "embedded" {
				embedDNS = true
			} else {
				embedDNS = ServiceUsesOnlyInternalNetworks(stack, svc)
			}
		}

		res, err := BundleResourcesFromDeploy(svc.Deploy)
		if err != nil {
			return nil, fmt.Errorf("service %q resources: %w", name, err)
		}

		spec := supervisor.ContainerSpec{
			ID:                 composeContainerID(meta.Project, name),
			Image:              img,
			ImageConfig:        cfg,
			ManifestLayers:     m.Layers,
			BlobPaths:          paths,
			Env:                EnvMapToSlice(svc.Environment),
			Args:               composeArgs(&svc, cfg),
			User:               u,
			PortMappings:       portMaps,
			ReadOnly:           svc.ReadOnly,
			Hostname:           strings.TrimSpace(name),
			RestartPolicy:      composeRestartPolicy(svc.Restart),
			StopTimeout:        time.Duration(svc.StopTimeout.Duration),
			Healthcheck:        hc,
			ExtraMounts:        mounts,
			EmbedDNS:           embedDNS,
			ComposeInternalNet: internalNet,
			Resources:          res,
			Privileged:         svc.Privileged,
			SeccompProfile:     ResolveSeccompProfilePath(meta.ComposeDir, svc.SeccompProfile),
			CapAdd:             append([]string(nil), svc.CapAdd...),
			CapDrop:            append([]string(nil), svc.CapDrop...),
			NoNewPrivileges:    svc.NoNewPrivileges,
		}
		out = append(out, spec)
	}
	return out, nil
}

func pullAuth(svc *Service) *image.RegistryAuth {
	u := strings.TrimSpace(svc.RegistryUsername)
	if u == "" {
		return nil
	}
	return &image.RegistryAuth{Username: u, Password: svc.RegistryPassword}
}

func composeArgs(svc *Service, cfg *oci.ImageConfig) []string {
	var ep, cmd []string
	if len(svc.Entrypoint) > 0 {
		ep = append([]string(nil), svc.Entrypoint...)
	} else if cfg != nil {
		ep = append([]string(nil), cfg.Config.Entrypoint...)
	}
	if len(svc.Command) > 0 {
		cmd = append([]string(nil), svc.Command...)
	} else if cfg != nil {
		cmd = append([]string(nil), cfg.Config.Cmd...)
	}
	if len(ep) == 0 && len(cmd) == 0 {
		return nil
	}
	out := append([]string(nil), ep...)
	out = append(out, cmd...)
	return out
}

func composeRestartPolicy(r RestartPolicy) supervisor.RestartPolicy {
	switch strings.ToLower(string(r)) {
	case "", string(RestartNo):
		return supervisor.RestartNever
	case string(RestartAlways):
		return supervisor.RestartAlways
	case string(RestartOnFailure):
		return supervisor.RestartOnFailure
	case string(RestartUnlessStopped):
		return supervisor.RestartUnlessStopped
	default:
		return supervisor.RestartNever
	}
}

func composeContainerID(project, svcName string) string {
	p := sanitizePathPart(project)
	n := sanitizePathPart(svcName)
	if p == "default" {
		return n
	}
	return p + "-" + n
}

func parseComposeUser(s string) (*bundle.User, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("expected uid:gid, got %q", s)
	}
	uid64, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("uid: %w", err)
	}
	gid64, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("gid: %w", err)
	}
	return &bundle.User{UID: uint32(uid64), GID: uint32(gid64)}, nil
}
