package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zrougamed/nyxd/internal/network"
	"github.com/zrougamed/nyxd/internal/runtime"
)

// On-disk layout: {baseDir}/supervisor/containers/{id}.json
// This is not a separate KV database; it is a small JSON record so the daemon can
// re-register containers after an unclean restart while crun still reports "running".

const supervisorPersistVersion = 1

type containerPersistRecord struct {
	Version   int           `json:"v"`
	Spec      ContainerSpec `json:"spec"`
	NetNS     string        `json:"netNS"`
	IP        string        `json:"ip"`
	BundleDir string        `json:"bundleDir"`
	RootFS    string        `json:"rootFS"`
}

func (s *Supervisor) supervisorStateDir() string {
	return filepath.Join(s.baseDir, "supervisor", "containers")
}

func (s *Supervisor) persistPath(id string) string {
	return filepath.Join(s.supervisorStateDir(), id+".json")
}

func (s *Supervisor) persistContainerEntry(e *containerEntry) error {
	if e == nil {
		return nil
	}
	dir := s.supervisorStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	rec := containerPersistRecord{
		Version:   supervisorPersistVersion,
		Spec:      e.spec,
		NetNS:     e.netNS,
		IP:        e.ip,
		BundleDir: e.bundleDir,
		RootFS:    e.rootFS,
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	id := e.spec.ID
	if id == "" {
		return fmt.Errorf("persist: empty container id")
	}
	tmp, err := os.CreateTemp(dir, id+".*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	final := s.persistPath(id)
	if err := os.Rename(tmpPath, final); err != nil {
		return err
	}
	return nil
}

func (s *Supervisor) removePersistedState(id string) {
	if id == "" {
		return
	}
	_ = os.Remove(s.persistPath(id))
}

// reconcilePersisted loads JSON records and re-attaches containers that are still
// running under crun (e.g. nyxd was killed with SIGKILL while workloads stayed up).
func (s *Supervisor) reconcilePersisted() {
	dir := s.supervisorStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.log.Warn("supervisor state dir", "dir", dir, "err", err)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.log.Warn("supervisor state reconcile: readdir", "dir", dir, "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(de.Name(), ".json")
		path := filepath.Join(dir, de.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec containerPersistRecord
		if err := json.Unmarshal(data, &rec); err != nil || rec.Version != supervisorPersistVersion {
			s.log.Warn("supervisor state: skipping corrupt record", "path", path, "err", err)
			continue
		}
		if rec.Spec.ID != id {
			s.log.Warn("supervisor state: id mismatch", "file", id, "spec.id", rec.Spec.ID)
			continue
		}
		st, err := s.rt.State(ctx, id)
		if err != nil {
			if runtime.CrunContainerAbsent(err) {
				if err := os.Remove(path); err == nil {
					s.log.Info("supervisor state: removed record (crun state gone)", "id", id)
				}
			} else {
				s.log.Warn("supervisor state: skip reconcile (crun state error)", "id", id, "err", err)
			}
			continue
		}
		if st == nil || st.Status != "running" {
			if err := os.Remove(path); err == nil {
				if st != nil {
					s.log.Info("supervisor state: removed stale record", "id", id, "status", st.Status)
				} else {
					s.log.Info("supervisor state: removed stale record", "id", id, "reason", "nil state")
				}
			}
			continue
		}

		s.mu.Lock()
		if _, exists := s.containers[id]; exists {
			s.mu.Unlock()
			continue
		}
		entry := &containerEntry{
			spec:      rec.Spec,
			netNS:     rec.NetNS,
			ip:        rec.IP,
			bundleDir: rec.BundleDir,
			rootFS:    rec.RootFS,
		}
		s.containers[id] = entry
		s.mu.Unlock()

		s.log.Info("re-adopted supervised container from disk", "id", id, "image", rec.Spec.Image, "ip", rec.IP)
		s.startHealthMonitor(context.Background(), entry)
		s.registerEmbeddedDNS(rec.Spec, rec.IP)
		s.wg.Add(1)
		// Daemon-lifetime context: reconcile's short-lived ctx must not cancel supervision.
		go s.supervise(context.Background(), entry)
	}
}

// reconcileCrunOrphans picks up containers that are still running in crun after
// an unclean nyxd restart but have no (or unusable) supervisor JSON — typically
// when nyxd-meta.json exists under the bundle dir and overlay/netns are present.
func (s *Supervisor) reconcileCrunOrphans() {
	if s.imgStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	states, err := s.rt.List(ctx)
	if err != nil {
		s.log.Warn("crun orphan reconcile: list", "err", err)
		return
	}

	for _, st := range states {
		if st.Status != "running" {
			continue
		}
		id := strings.TrimSpace(st.ID)
		if id == "" {
			continue
		}

		s.mu.RLock()
		_, exists := s.containers[id]
		s.mu.RUnlock()
		if exists {
			continue
		}

		bundleDir := strings.TrimSpace(st.Bundle)
		if bundleDir == "" {
			bundleDir = filepath.Join(s.baseDir, "bundles", id)
		}
		meta, err := readBundleRunMeta(bundleDir)
		if err != nil {
			s.log.Debug("crun orphan reconcile: skip (no bundle meta)", "id", id, "err", err)
			continue
		}

		nsPath := filepath.Join("/run/nyxd/netns", id)
		if _, err := os.Stat(nsPath); err != nil {
			s.log.Debug("crun orphan reconcile: skip (no netns)", "id", id)
			continue
		}
		merged := filepath.Join(s.baseDir, "overlay", id, "merged")
		if _, err := os.Stat(merged); err != nil {
			s.log.Debug("crun orphan reconcile: skip (no overlay merged)", "id", id)
			continue
		}

		m, cfg, paths, err := s.imgStore.ResolvePulledImage(meta.Image)
		if err != nil {
			s.log.Warn("crun orphan reconcile: resolve image", "id", id, "image", meta.Image, "err", err)
			continue
		}

		var portMaps []network.PortMapping
		for _, pub := range meta.Publish {
			p, err := network.ParseDockerPublish(pub)
			if err != nil {
				s.log.Warn("crun orphan reconcile: publish", "id", id, "pub", pub, "err", err)
				continue
			}
			portMaps = append(portMaps, p)
		}

		spec := ContainerSpec{
			ID:             id,
			Image:          meta.Image,
			ImageConfig:    cfg,
			ManifestLayers: m.Layers,
			BlobPaths:      paths,
			Env:            meta.Env,
			Args:           meta.Args,
			Hostname:       meta.Hostname,
			RestartPolicy:  restartPolicyFromString(meta.Restart),
			ReadOnly:       false,
			PortMappings:   portMaps,
			EmbedDNS:       false,
		}

		ip := strings.TrimSpace(meta.IP)
		entry := &containerEntry{
			spec:      spec,
			netNS:     nsPath,
			ip:        ip,
			bundleDir: bundleDir,
			rootFS:    merged,
		}

		s.mu.Lock()
		if _, exists := s.containers[id]; exists {
			s.mu.Unlock()
			continue
		}
		s.containers[id] = entry
		s.mu.Unlock()

		s.log.Info("re-adopted container from crun+bundle meta", "id", id, "image", meta.Image, "ip", ip)
		if err := s.persistContainerEntry(entry); err != nil {
			s.log.Warn("crun orphan reconcile: persist", "id", id, "err", err)
		}
		s.registerEmbeddedDNS(entry.spec, ip)
		s.wg.Add(1)
		go s.supervise(context.Background(), entry)
	}
}
