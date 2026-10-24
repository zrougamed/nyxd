//go:build linux

package native

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureIptablesRuleInsertAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "iptables.log")
	statePath := filepath.Join(dir, "installed")
	script := `#!/bin/sh
echo "$@" >> "` + logPath + `"
comment=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--comment" ]; then comment="$a"; fi
  prev="$a"
done
case "$*" in
*-C*)
	grep -qxF "$comment" "` + statePath + `" 2>/dev/null && exit 0
	echo "Bad rule" >&2
	exit 1
	;;
*-I*)
	echo "$comment" >> "` + statePath + `"
	exit 0
	;;
esac
echo "unexpected: $*" >&2
exit 1
`
	bin := filepath.Join(dir, "iptables")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := iptablesBin
	iptablesBin = bin
	t.Cleanup(func() { iptablesBin = old })

	args := []string{"FORWARD", "-i", "nyxbr0", "-o", "nyxbr0", "-j", "ACCEPT"}
	if err := ensureIptablesRule("nyxd-fw-hairpin-nyxbr0", args...); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := ensureIptablesRule("nyxd-fw-hairpin-nyxbr0", args...); err != nil {
		t.Fatalf("second ensure: %v", err)
	}

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var inserts int
	for _, ln := range strings.Split(string(body), "\n") {
		if strings.Contains(ln, "-I FORWARD 1") {
			inserts++
			if !strings.Contains(ln, "nyxd-fw-hairpin-nyxbr0") {
				t.Fatalf("insert missing comment: %s", ln)
			}
		}
	}
	if inserts != 1 {
		t.Fatalf("want 1 insert, got %d\n%s", inserts, body)
	}
}

func TestEnsureBridgeFirewallInvokesAllRules(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "iptables.log")
	script := `#!/bin/sh
echo "$@" >> "` + logPath + `"
case "$*" in
*-C*) exit 1 ;;
*-I*) exit 0 ;;
esac
exit 1
`
	bin := filepath.Join(dir, "iptables")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := iptablesBin
	iptablesBin = bin
	t.Cleanup(func() { iptablesBin = old })

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ensureBridgeFirewall("nyxbr0", quiet)

	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"nyxd-fw-hairpin-nyxbr0",
		"nyxd-fw-egress-nyxbr0",
		"nyxd-fw-estab-nyxbr0",
		"nyxd-fw-dns-udp-nyxbr0",
		"nyxd-fw-dns-tcp-nyxbr0",
		"--dport 53",
		"RELATED,ESTABLISHED",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestEnsureIptablesRuleEmpty(t *testing.T) {
	if err := ensureIptablesRule("x"); err == nil {
		t.Fatal("expected error")
	}
}
