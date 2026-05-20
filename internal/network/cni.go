// CNI plugin executor: conflist generation and exec-based plugin runs.
package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	cniPluginDir = "/opt/cni/bin"
	cniConfDir   = "/etc/cni/net.d"
	bridgeName   = "nyxbr0"
	subnet       = "10.88.0.0/16"
	defaultMTU   = 1500
)

// Manager manages CNI network setup/teardown for containers.
type Manager struct {
	mu      sync.Mutex
	confDir string
	binDir  string
	netName string
}

// NewManager creates a CNI network manager.
func NewManager(netName, confDir, binDir string) *Manager {
	if confDir == "" {
		confDir = cniConfDir
	}
	if binDir == "" {
		binDir = cniPluginDir
	}
	if netName == "" {
		netName = "nyx"
	}
	return &Manager{confDir: confDir, binDir: binDir, netName: netName}
}

// EnsureNetwork writes a CNI config for the nyx bridge network
// and ensures the bridge interface exists.
func (m *Manager) EnsureNetwork() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	confPath := filepath.Join(m.confDir, "10-nyx.conflist")
	if _, err := os.Stat(confPath); err == nil {
		return nil // already exists
	}

	if err := os.MkdirAll(m.confDir, 0o755); err != nil {
		return err
	}

	conf := m.buildConfList()
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(confPath, data, 0o644)
}

// Setup adds a container to the network.
// Returns the assigned IP address.
func (m *Manager) Setup(ctx context.Context, containerID, netNS string, portMappings []PortMapping, opts *SetupOptions) (string, error) {
	_ = opts // compose internal: enforce via native driver; CNI relies on plugin config
	m.mu.Lock()
	defer m.mu.Unlock()

	confPath := filepath.Join(m.confDir, "10-nyx.conflist")
	confData, err := os.ReadFile(confPath)
	if err != nil {
		return "", fmt.Errorf("cni conf: %w", err)
	}

	env := m.cniEnv(containerID, netNS, "ADD")
	result, err := m.execConfList(ctx, confData, nil, env)
	if err != nil {
		return "", fmt.Errorf("cni ADD: %w", err)
	}

	ip := extractIP(result)
	return ip, nil
}

// Teardown removes a container from the network.
func (m *Manager) Teardown(ctx context.Context, containerID, netNS string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	confPath := filepath.Join(m.confDir, "10-nyx.conflist")
	confData, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("cni conf: %w", err)
	}

	env := m.cniEnv(containerID, netNS, "DEL")
	_, err = m.execConfList(ctx, confData, nil, env)
	return err
}

// clearNetNSPath removes a leftover netns bind-mount or file under nsPath.
// After an unclean nyxd stop (SIGINT/SIGKILL), the mount can remain; a plain os.Create
// then fails with EPERM because the path is still a mount point.
func clearNetNSPath(nsPath string) error {
	_ = detachUnmount(nsPath)
	if err := os.Remove(nsPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// CreateNetNS creates a new network namespace and returns its path.
func CreateNetNS(containerID string) (string, error) {
	nsDir := "/run/nyxd/netns"
	if err := os.MkdirAll(nsDir, 0o700); err != nil {
		return "", err
	}
	nsPath := filepath.Join(nsDir, containerID)

	if _, err := os.Stat(nsPath); err == nil {
		if err := clearNetNSPath(nsPath); err != nil {
			return "", fmt.Errorf("clear stale netns path %q: %w", nsPath, err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	// Create an empty file to bind-mount the netns into.
	f, err := os.Create(nsPath)
	if err != nil {
		return "", err
	}
	f.Close()

	// Unshare a new network namespace and bind-mount it to nsPath.
	// This is equivalent to: ip netns add <name>
	cmd := exec.Command("unshare", "--net="+nsPath, "true")
	if err := cmd.Run(); err != nil {
		os.Remove(nsPath)
		return "", fmt.Errorf("create netns: %w", err)
	}
	return nsPath, nil
}

// DeleteNetNS unmounts and removes a network namespace.
func DeleteNetNS(containerID string) error {
	nsPath := filepath.Join("/run/nyxd/netns", containerID)
	return clearNetNSPath(nsPath)
}

// PortMapping defines a host:container port mapping.
type PortMapping struct {
	HostPort      int
	ContainerPort int
	Protocol      string // "tcp" or "udp"
}

// ─── Internal ──────────────────────────────────────────────────────────────────

// execConfList runs all plugins in a CNI conflist in order (ADD) or reverse (DEL).
func (m *Manager) execConfList(ctx context.Context, confListData, prevResult []byte, env []string) ([]byte, error) {
	var confList struct {
		Name    string            `json:"name"`
		Plugins []json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(confListData, &confList); err != nil {
		return nil, err
	}

	isDel := false
	for _, e := range env {
		if strings.HasPrefix(e, "CNI_COMMAND=DEL") {
			isDel = true
			break
		}
	}

	plugins := confList.Plugins
	if isDel {
		// Reverse order for teardown.
		for i, j := 0, len(plugins)-1; i < j; i, j = i+1, j-1 {
			plugins[i], plugins[j] = plugins[j], plugins[i]
		}
	}

	var result []byte
	for _, pluginConf := range plugins {
		var meta struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(pluginConf, &meta); err != nil {
			return nil, err
		}

		// Inject prevResult for chaining.
		if prevResult != nil {
			var m map[string]json.RawMessage
			json.Unmarshal(pluginConf, &m)
			m["prevResult"] = prevResult
			pluginConf, _ = json.Marshal(m)
		}

		out, err := m.execPlugin(ctx, meta.Type, pluginConf, env)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", meta.Type, err)
		}
		if len(out) > 0 {
			prevResult = out
			result = out
		}
	}
	return result, nil
}

func (m *Manager) execPlugin(ctx context.Context, pluginType string, conf []byte, env []string) ([]byte, error) {
	binPath := filepath.Join(m.binDir, pluginType)
	if _, err := os.Stat(binPath); err != nil {
		return nil, fmt.Errorf("cni plugin not found: %s", binPath)
	}

	cmd := exec.CommandContext(ctx, binPath)
	cmd.Stdin = bytes.NewReader(conf)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if ok := false; !ok {
			_ = exitErr
		}
		return nil, fmt.Errorf("exec %s: %w stderr=%s", pluginType, err, string(out))
	}
	return out, nil
}

func (m *Manager) cniEnv(containerID, netNS, command string) []string {
	return []string{
		"CNI_COMMAND=" + command,
		"CNI_CONTAINERID=" + containerID,
		"CNI_NETNS=" + netNS,
		"CNI_IFNAME=eth0",
		"CNI_PATH=" + m.binDir,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
}

func (m *Manager) buildConfList() map[string]any {
	return map[string]any{
		"cniVersion": "1.0.0",
		"name":       m.netName,
		"plugins": []map[string]any{
			{
				"type":         "bridge",
				"bridge":       bridgeName,
				"isGateway":    true,
				"ipMasq":       true,
				"hairpinMode":  true,
				"forceAddress": false,
				"mtu":          defaultMTU,
				"ipam": map[string]any{
					"type": "host-local",
					"ranges": [][]map[string]any{{{
						"subnet":  subnet,
						"gateway": firstIP(subnet),
					}}},
					"routes": []map[string]any{{"dst": "0.0.0.0/0"}},
				},
			},
			{
				"type":         "portmap",
				"capabilities": map[string]bool{"portMappings": true},
			},
			{
				"type": "firewall",
			},
			{
				"type": "tuning",
				"sysctl": map[string]string{
					"net.ipv4.conf.all.arp_announce": "2",
				},
			},
		},
	}
}

func extractIP(result []byte) string {
	if result == nil {
		return ""
	}
	var r struct {
		IPs []struct {
			Address string `json:"address"`
		} `json:"ips"`
	}
	if err := json.Unmarshal(result, &r); err != nil || len(r.IPs) == 0 {
		return ""
	}
	ip, _, _ := net.ParseCIDR(r.IPs[0].Address)
	if ip == nil {
		return r.IPs[0].Address
	}
	return ip.String()
}

func firstIP(cidr string) string {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "10.88.0.1"
	}
	ip := network.IP
	ip[3]++ // .1
	return ip.String()
}

var _ Backend = (*Manager)(nil)
