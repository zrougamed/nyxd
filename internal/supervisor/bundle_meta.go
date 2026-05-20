package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zrougamed/nyxd/internal/network"
)

const bundleMetaFileName = "nyxd-meta.json"

// bundleRunMeta is written next to config.json so we can re-adopt a container
// after nyxd restarts without supervisor JSON (e.g. persist marshal failed).
type bundleRunMeta struct {
	Image    string   `json:"image"`
	IP       string   `json:"ip,omitempty"`
	Env      []string `json:"env,omitempty"`
	Args     []string `json:"args,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	Restart  string   `json:"restart,omitempty"`
	Publish  []string `json:"publish,omitempty"`
}

func portMappingsToPublishStrings(pm []network.PortMapping) []string {
	if len(pm) == 0 {
		return nil
	}
	var out []string
	for _, p := range pm {
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}
		out = append(out, fmt.Sprintf("%d:%d/%s", p.HostPort, p.ContainerPort, proto))
	}
	return out
}

func writeBundleRunMeta(bundleDir string, meta bundleRunMeta) error {
	if bundleDir == "" {
		return fmt.Errorf("bundle dir empty")
	}
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(bundleDir, bundleMetaFileName)
	tmp, err := os.CreateTemp(bundleDir, bundleMetaFileName+".*.tmp")
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
	return os.Rename(tmpPath, final)
}

func readBundleRunMeta(bundleDir string) (bundleRunMeta, error) {
	var meta bundleRunMeta
	data, err := os.ReadFile(filepath.Join(bundleDir, bundleMetaFileName))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	if strings.TrimSpace(meta.Image) == "" {
		return meta, fmt.Errorf("bundle meta: missing image")
	}
	return meta, nil
}

func removeBundleMeta(bundleDir string) {
	if bundleDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(bundleDir, bundleMetaFileName))
}

func restartPolicyFromString(s string) RestartPolicy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "unless-stopped", "unless_stopped":
		return RestartUnlessStopped
	case "always":
		return RestartAlways
	case "on-failure", "on_failure":
		return RestartOnFailure
	case "no", "never":
		return RestartNever
	default:
		return RestartNever
	}
}
