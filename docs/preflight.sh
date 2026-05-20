#!/bin/bash
# nyxd-preflight.sh — verify the host is ready to run nyxd.
# Run as root before first deployment (Linux only).
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "nyxd preflight: must run as root (e.g. sudo bash docs/preflight.sh or sudo make preflight)" >&2
  exit 1
fi

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ERRORS=0; WARNINGS=0

pass()  { echo -e "  ${GREEN}✓${NC} $1"; }
fail()  { echo -e "  ${RED}✗${NC} $1"; ERRORS=$((ERRORS+1)); }
warn()  { echo -e "  ${YELLOW}!${NC} $1"; WARNINGS=$((WARNINGS+1)); }

check_cmd() {
  local desc="$1"; shift
  if "$@" &>/dev/null 2>&1; then pass "$desc"; else fail "$desc"; fi
}

echo ""
echo "══════════════════════════════════════"
echo "  nyxd pre-flight check"
echo "══════════════════════════════════════"

# ── OS & architecture ─────────────────────
echo ""
echo "[ OS ]"
if [ "$(uname -s)" != "Linux" ]; then
  echo "nyxd preflight: Linux required (found: $(uname -s))" >&2
  exit 1
fi
pass "Linux $(uname -r)"
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64|aarch64|arm64)
    pass "architecture $ARCH (supported)"
    ;;
  armv7l|armv6l)
    warn "architecture $ARCH — 32-bit ARM is not a primary target; prefer aarch64/ARM64"
    ;;
  *)
    fail "architecture $ARCH not supported (use x86_64/amd64 or aarch64/arm64)"
    ;;
esac
pass "running as root"

# ── Networking (defaults) ─────────────────
echo ""
echo "[ Networking ]"
pass "default is in-process native networking (-net-driver=native); /opt/cni/bin not required"
if [ -x /opt/cni/bin/bridge ] || [ -x /opt/cni/bin/host-local ]; then
  warn "CNI plugins under /opt/cni/bin present — only needed for -net-driver=cni, not the default"
else
  pass "no external CNI plugins required for default native driver"
fi

# ── Kernel version ────────────────────────
echo ""
echo "[ Kernel ]"
KVER_RAW=$(uname -r)
KMAJ=$(echo "$KVER_RAW" | cut -d. -f1)
KMIN=$(echo "$KVER_RAW" | cut -d. -f2)
KNUM=$((KMAJ * 100 + KMIN))
if [ "$KNUM" -ge 601 ]; then
  pass "kernel $KVER_RAW (≥ 6.1 recommended)"
elif [ "$KNUM" -ge 511 ]; then
  warn "kernel $KVER_RAW (works but 6.1 LTS recommended for full security fixes)"
else
  fail "kernel $KVER_RAW (minimum 5.11 required)"
fi

# ── Kernel modules ────────────────────────
echo ""
echo "[ Kernel modules ]"
REQUIRED_MODS="overlay bridge veth br_netfilter ip_tables iptable_nat nf_nat nf_conntrack"
for mod in $REQUIRED_MODS; do
  if modinfo "$mod" &>/dev/null || lsmod | grep -q "^${mod} "; then
    pass "$mod"
  else
    fail "$mod not available (run: modprobe $mod)"
  fi
done

# Seccomp is a built-in kernel facility (CONFIG_SECCOMP_FILTER), not a loadable module
# named "seccomp" — modprobe seccomp fails on typical distros even when crun works fine.
# Many /proc/sys files report st_size 0; never use test -s — read content instead.
echo ""
echo "[ Seccomp ]"
_sa=""
if [ -r /proc/sys/kernel/seccomp/actions_avail ]; then
  _sa=$(cat /proc/sys/kernel/seccomp/actions_avail 2>/dev/null | tr -d '\r\n')
fi
if [ -z "${_sa// /}" ]; then
  _sa=$(sysctl -n kernel.seccomp.actions_avail 2>/dev/null | tr -d '\r\n' || true)
fi
if [ -n "${_sa// /}" ]; then
  pass "seccomp syscall filtering available (built-in; not the same as tracing/XDP eBPF)"
else
  fail "seccomp syscall filtering unavailable — need CONFIG_SECCOMP_FILTER for typical crun defaults (tracing/eBPF alone is not a substitute)"
fi

# nft modules (warn not fail — kernel may have them built-in)
NFT_MODS="nft_masq nft_nat nft_chain_nat"
for mod in $NFT_MODS; do
  if modinfo "$mod" &>/dev/null || lsmod | grep -q "^${mod} "; then
    pass "$mod"
  else
    warn "$mod not found — port mappings may not work (run: modprobe $mod)"
  fi
done

# ── sysctls ───────────────────────────────
echo ""
echo "[ sysctls ]"
check_sysctl() {
  local key="$1" expected="$2"
  local val
  val=$(sysctl -n "$key" 2>/dev/null || echo "missing")
  if [ "$val" = "$expected" ]; then
    pass "$key=$val"
  else
    fail "$key=$val (expected $expected) — add to /etc/sysctl.d/99-nyxd.conf"
  fi
}
check_sysctl net.ipv4.ip_forward 1
check_sysctl net.bridge.bridge-nf-call-iptables 1 || warn "load br_netfilter first: modprobe br_netfilter"

# ── Binaries ──────────────────────────────
echo ""
echo "[ Required binaries ]"
check_cmd "crun installed" command -v crun
if command -v crun &>/dev/null; then
  CRUN_VER=$(crun --version 2>&1 | grep -oP 'version \K[\d.]+' | head -1)
  CRUN_MAJ=$(echo "$CRUN_VER" | cut -d. -f1)
  CRUN_MIN=$(echo "$CRUN_VER" | cut -d. -f2)
  CRUN_NUM=$((CRUN_MAJ * 1000 + CRUN_MIN))
  if [ "$CRUN_NUM" -ge 1019 ]; then
    pass "crun $CRUN_VER (≥ 1.19 recommended)"
  elif [ "$CRUN_NUM" -ge 1004 ]; then
    warn "crun $CRUN_VER (works but 1.4.4+ required for CVE-2022-27650 fix)"
  else
    fail "crun $CRUN_VER too old — CVE-2022-27650 unpatched (upgrade to ≥ 1.4.4)"
  fi
fi
check_cmd "tar installed" command -v tar
check_cmd "nft installed (for port mappings)" command -v nft

# ── Filesystem ────────────────────────────
echo ""
echo "[ Filesystem ]"
for dir in /var/lib/nyxd /run/nyxd /var/lib/nyxd/ipam; do
  if mkdir -p "$dir" 2>/dev/null; then pass "$dir writable"; else fail "$dir not writable"; fi
done

# Check overlayfs works on the data dir filesystem.
TMPDIR_TEST=$(mktemp -d /var/lib/nyxd/.ovl-test-XXXX)
mkdir -p "$TMPDIR_TEST"/{lower,upper,work,merged}
echo "test" > "$TMPDIR_TEST/lower/file"
if mount -t overlay overlay \
  -o "lowerdir=$TMPDIR_TEST/lower,upperdir=$TMPDIR_TEST/upper,workdir=$TMPDIR_TEST/work" \
  "$TMPDIR_TEST/merged" 2>/dev/null; then
  pass "overlayfs works on /var/lib/nyxd filesystem"
  umount "$TMPDIR_TEST/merged"
else
  fail "overlayfs mount failed on /var/lib/nyxd — ensure overlay module is loaded and filesystem supports d_type"
fi
rm -rf "$TMPDIR_TEST"

# ── Summary ───────────────────────────────
echo ""
echo "══════════════════════════════════════"
if [ "$ERRORS" -eq 0 ] && [ "$WARNINGS" -eq 0 ]; then
  echo -e "  ${GREEN}All checks passed. nyxd is ready.${NC}"
elif [ "$ERRORS" -eq 0 ]; then
  echo -e "  ${YELLOW}$WARNINGS warning(s). nyxd will run but review warnings above.${NC}"
else
  echo -e "  ${RED}$ERRORS error(s), $WARNINGS warning(s). Fix errors before running nyxd.${NC}"
  exit 1
fi
echo "══════════════════════════════════════"
echo ""
