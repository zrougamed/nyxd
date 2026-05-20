# nyxd — Kernel & System Requirements

## Kernel Version

| Requirement | Minimum | Recommended |
|---|---|---|
| Kernel | 5.11 | **6.1 LTS** |
| overlayfs in user namespaces | 5.11 | 6.1 |
| cgroup v2 (resource limits) | 5.2 | 6.1 |
| Seccomp notify (crun) | 5.6 | 6.1 |
| nftables masquerade | 4.9 | 6.1 |

---

## Required Kernel Modules

Create `/etc/modules-load.d/nyxd.conf`:

```
# nyxd — required kernel modules
# Load at boot via systemd-modules-load.service

# Container rootfs (overlayfs)
overlay

# Container networking
bridge
veth
br_netfilter

# iptables / nftables (used by native network plugin)
ip_tables
iptable_nat
iptable_filter
nf_nat
nf_conntrack
nft_masq
nft_nat
nft_chain_nat
```

Seccomp / Seccomp BPF are **built into the kernel** (`CONFIG_SECCOMP`, `CONFIG_SECCOMP_FILTER`). There is **no** loadable module named `seccomp`; `modprobe seccomp` is wrong on typical kernels. crun still applies seccomp profiles via the prctl/seccomp syscalls when those options are enabled.

Verify after boot (note: the file often reports **size 0** to `stat`; use `cat`, not `test -s`):

```bash
test -r /proc/sys/kernel/seccomp/actions_avail && cat /proc/sys/kernel/seccomp/actions_avail
```

Load immediately (without rebooting):

```bash
modprobe overlay bridge veth br_netfilter \
         ip_tables iptable_nat iptable_filter \
         nf_nat nf_conntrack nft_masq nft_nat nft_chain_nat
```

Verify loadable modules (seccomp will not appear — it is not a module):

```bash
lsmod | grep -E 'overlay|bridge|veth|br_netfilter|ip_tables|nf_nat|nf_conntrack|nft'
```

---

## Required sysctls

Create `/etc/sysctl.d/99-nyxd.conf`:

```ini
# IP forwarding — containers need to route to the internet
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1

# Bridge netfilter — iptables/nftables must see bridged container traffic
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.bridge.bridge-nf-call-arptables = 1

# Asymmetric routing — don't drop valid container reply packets
net.ipv4.conf.all.rp_filter = 0
net.ipv4.conf.default.rp_filter = 0

# Connection tracking table size (tune to your workload)
net.netfilter.nf_conntrack_max = 131072

# Increase local port range for port-mapped containers
net.ipv4.ip_local_port_range = 1024 65535

# Disable IPv6 router advertisements on container bridge
net.ipv6.conf.nyxbr0.accept_ra = 0
```

Apply immediately:

```bash
sysctl --system
# or specifically:
sysctl -p /etc/sysctl.d/99-nyxd.conf
```

---

## crun Requirements

### Version

| | Value |
|---|---|
| **Minimum version** | 1.4.4 |
| **Recommended version** | **1.19+** |
| **CVE history** | 2 CVEs total, both patched years ago |

### CVE history (complete — this is why we use crun over runc)

| CVE | Description | Fixed |
|---|---|---|
| CVE-2022-27650 | Non-empty inheritable Linux capabilities on container start | crun 1.4.4 |
| CVE-2019-18837 | Symlink path escape via crafted image | crun 0.10.5 |

Compare: `runc` had **3 critical container-escape CVEs in November 2025 alone**
(CVE-2025-31133, CVE-2025-52565, CVE-2025-52881). crun's minimal C codebase and
active security review make it a solid choice for **small Linux lab hosts** running containers with minimal attack surface.

### Install

```bash
# Debian/Ubuntu
apt-get install -y crun

# Verify version
crun --version
# crun version 1.19
# commit: ...
# spec: 1.0.2
# +SYSTEMD +SELINUX +APPARMOR +CAP +SECCOMP +EBPF +CRIU +LIBKRUN +WASM:wasmedge
# OCI bundled: True

# If distro version is too old, build from source:
apt-get install -y gcc libsystemd-dev libcap-dev libseccomp-dev \
                   python3 libyajl-dev libtool autoconf automake
cd /tmp
git clone https://github.com/containers/crun
cd crun
git checkout 1.19
./autogen.sh
./configure --with-systemd --with-seccomp
make -j$(nproc)
install -m 755 crun /usr/local/bin/crun
```

### Required crun capabilities on the host

nyxd invokes `crun` as root. crun needs these Linux capabilities on the **host process**:

```
CAP_SYS_ADMIN      # mount, unshare namespaces
CAP_NET_ADMIN      # veth creation, bridge setup, netns ops
CAP_MKNOD          # create device nodes in container /dev
CAP_SETUID         # drop to container user
CAP_SETGID         # drop to container group
CAP_SYS_CHROOT     # pivot_root into container rootfs
CAP_DAC_OVERRIDE   # read/write across container rootfs layers
```

All of these are available to root by default. nyxd must run as root.

---

## Filesystem Requirements

```
/var/lib/nyxd/        # base data dir (ext4 or xfs recommended, not tmpfs)
/run/nyxd/            # runtime state (tmpfs is fine, cleared on reboot)
/run/netns/           # network namespace bind-mounts
/opt/cni/bin/         # only with nyxd -net-driver=cni (see docs/networking.md)
```

See **[docs/networking.md](networking.md)** for default native networking vs optional CNI.

Disk space: allow **10 GB minimum** for image blob cache under `/var/lib/nyxd/images/`.

overlayfs requires the underlying filesystem to support `d_type` (directory entry type).
**ext4 and xfs both support this.** tmpfs does NOT support overlayfs as a lower layer.

Verify:

```bash
# Must print "true"
xfs_info /var/lib/nyxd | grep ftype   # xfs: ftype=1 means d_type enabled
# or
tune2fs -l /dev/sdX | grep "Filesystem features" | grep dir_index  # ext4
```

---

## Full pre-flight check script

```bash
#!/bin/bash
# nyxd-preflight.sh — run before first nyxd deployment
set -e

ERRORS=0

check() {
  local desc="$1"; shift
  if "$@" &>/dev/null; then
    echo "  ✓ $desc"
  else
    echo "  ✗ $desc"
    ERRORS=$((ERRORS + 1))
  fi
}

echo "=== nyxd pre-flight check ==="

echo ""
echo "[ Kernel ]"
KVER=$(uname -r | cut -d. -f1-2 | tr -d '.')
check "kernel >= 5.11" test "$KVER" -ge 511

echo ""
echo "[ Kernel modules ]"
for mod in overlay bridge veth br_netfilter ip_tables iptable_nat nf_nat nf_conntrack; do
  check "$mod loaded" modinfo "$mod"
done

echo ""
echo "[ Seccomp ]"
check "seccomp (actions_avail has content)" bash -c 'a=$(cat /proc/sys/kernel/seccomp/actions_avail 2>/dev/null); test -n "${a//[[:space:]]/}"'

echo ""
echo "[ sysctls ]"
check "ip_forward=1"                test "$(sysctl -n net.ipv4.ip_forward)" = "1"
check "bridge-nf-call-iptables=1"  test "$(sysctl -n net.bridge.bridge-nf-call-iptables 2>/dev/null)" = "1"

echo ""
echo "[ Binaries ]"
check "crun installed"  command -v crun
check "crun >= 1.4.4"  bash -c 'crun --version | grep -qE "version [1-9]\.[4-9]|version [2-9]"'
check "tar installed"   command -v tar

echo ""
echo "[ Filesystem ]"
check "/var/lib/nyxd writable" test -w /var/lib/nyxd || mkdir -p /var/lib/nyxd

echo ""
if [ "$ERRORS" -eq 0 ]; then
  echo "All checks passed. nyxd is ready to run."
else
  echo "$ERRORS check(s) failed. Fix the above before running nyxd."
  exit 1
fi
```
