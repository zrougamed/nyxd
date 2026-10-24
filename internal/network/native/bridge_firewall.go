//go:build linux

package native

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
)

// iptablesBin is the iptables binary used to install host filter rules that
// sit ahead of UFW/firewalld policy DROP. Overridable in tests.
var iptablesBin = "/usr/sbin/iptables"

// ensureBridgeFirewallMu serialises iptables probes/inserts across bridges.
var ensureBridgeFirewallMu sync.Mutex

// ensureBridgeFirewall installs ACCEPT rules so host firewalls (notably UFW
// with DEFAULT_FORWARD_POLICY=DROP) do not black-hole nyxd bridge traffic.
//
// Background: nyxd requires net.bridge.bridge-nf-call-iptables=1, so L2-bridged
// packets still traverse the filter FORWARD hook. Embedded DNS listens on the
// bridge gateway (UDP/TCP 53); those queries hit INPUT. Without explicit
// ACCEPT rules ahead of UFW's jumps, container↔container and gateway DNS time
// out — Docker installs equivalent rules for docker0; nyxd must do the same.
//
// Best-effort: failures are logged; EnsureNetwork still succeeds so a host
// without iptables is not bricked (nft-only / custom firewalls can add rules
// manually — see docs/networking.md).
func ensureBridgeFirewall(bridge string, log *slog.Logger) {
	if bridge == "" {
		bridge = BridgeName
	}
	ensureBridgeFirewallMu.Lock()
	defer ensureBridgeFirewallMu.Unlock()

	type rule struct {
		comment string
		args    []string
	}
	rules := []rule{
		{
			comment: "nyxd-fw-hairpin-" + bridge,
			args:    []string{"FORWARD", "-i", bridge, "-o", bridge, "-j", "ACCEPT"},
		},
		{
			comment: "nyxd-fw-egress-" + bridge,
			args:    []string{"FORWARD", "-i", bridge, "-j", "ACCEPT"},
		},
		{
			comment: "nyxd-fw-estab-" + bridge,
			args: []string{
				"FORWARD", "-o", bridge,
				"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED",
				"-j", "ACCEPT",
			},
		},
		{
			comment: "nyxd-fw-dns-udp-" + bridge,
			args:    []string{"INPUT", "-i", bridge, "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
		},
		{
			comment: "nyxd-fw-dns-tcp-" + bridge,
			args:    []string{"INPUT", "-i", bridge, "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
		},
	}

	for _, r := range rules {
		if err := ensureIptablesRule(r.comment, r.args...); err != nil {
			log.Warn("bridge firewall", "bridge", bridge, "comment", r.comment, "err", err)
			continue
		}
	}
}

// ensureIptablesRule inserts chainArgs at the head of the named chain when the
// commented rule is not already present. chainArgs must start with the chain
// name (FORWARD / INPUT / …).
func ensureIptablesRule(comment string, chainArgs ...string) error {
	if len(chainArgs) < 1 {
		return fmt.Errorf("iptables: empty rule")
	}
	withComment := append(append([]string{}, chainArgs...), "-m", "comment", "--comment", comment)

	check := append([]string{"-C"}, withComment...)
	if err := runIptables(check...); err == nil {
		return nil // already present
	}

	// Insert at head so we sit before UFW's ufw-before-* jumps / policy DROP.
	insert := append([]string{"-I", chainArgs[0], "1"}, withComment[1:]...)
	if err := runIptables(insert...); err != nil {
		return err
	}
	return nil
}

func runIptables(args ...string) error {
	cmd := exec.Command(iptablesBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("iptables %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("iptables %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}
