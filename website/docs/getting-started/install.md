# Installing nyxd on Linux

**nyxd** runs on **any** Linux host that meets the kernel and tooling requirements below (desktop, VM, VPS, edge box, ARM or x86). It was first written for a home **Raspberry Pi** so containers could run without Docker or a heavy daemon; that same footprint applies everywhere you want a minimal stack.

Typical setups: run **nyxd** on the host and **nyx** locally, or call the Unix-socket HTTP API from another machine you trust.

## What you need on each Linux host

| Requirement | Notes |
|-------------|--------|
| **Linux** with **overlayfs**, **namespaces**, **cgroups** | Debian, Fedora, Ubuntu, Raspberry Pi OS 64-bit, cloud images, etc. |
| **Root** for `nyxd` | Overlay, netns, and crun state require root today. |
| **crun** | `apt install crun`, `dnf install crun`, or build from source. |
| **Kernel modules** | See [kernel requirements](/docs/operations/kernel-requirements) — `overlay`, `bridge`, `veth`, `br_netfilter`, nftables NAT, etc. |
| **nft** (optional but recommended) | Used for port maps with the **native** network driver. |
| **Go 1.22+** (build only) | On the target host or cross-compile from a dev machine. |

Networking defaults to **`-net-driver=native`** (no `/opt/cni/bin`). See [networking](/docs/operations/networking).

## Install to `/usr/bin`

From a repo build:

```bash
sudo make install-usr
# equivalent: sudo make install BINDIR=/usr/bin
```

## Use `nyx` without sudo

**nyxd** must still run as **root**. Only the **`nyx` client** can run as a normal user if it can open the control socket.

1. **Create a POSIX group** (once), e.g. `nyxd`:

   ```bash
   sudo getent group nyxd >/dev/null || sudo groupadd --system nyxd
   ```

2. **Add your user** to that group, then **log out and back in** (or `newgrp nyxd`) so `id` lists `nyxd` among your groups:

   ```bash
   sudo usermod -aG nyxd "$USER"
   ```

3. Start **nyxd** with **`--socket-group=nyxd`**. The daemon keeps **`/run/nyxd/nyxd.sock`** at mode **`0660`** and **`chown`s it to `root:nyxd`**, so anyone in group `nyxd` can connect.

   ```bash
   sudo nyxd --base-dir=/var/lib/nyxd --socket-group=nyxd
   ```

   With **systemd**, append `--socket-group=nyxd` to `ExecStart=` in **`nyxd.service`** (the group must exist before the unit starts).

4. Install **`nyx`** (and **`nyxd`**) to **`PATH`**, e.g. **`/usr/bin`** — the binary stays world-executable `755`; access control is the socket, not the file mode on `nyx`:

   ```bash
   sudo make install-usr
   ```

5. As your user: `nyx ping`, `nyx ps`, `nyx run -d …` without `sudo`.

This does **not** grant root on the box—only what the HTTP API allows. The daemon still performs privileged container operations.

## Build on the target host

```bash
git clone https://github.com/zrougamed/nyxd.git
cd nyxd
make build build-nyx
sudo make install              # default: /usr/local/bin
# or system-wide:
sudo make install-usr          # nyxd + nyx → /usr/bin
# or:   sudo make install BINDIR=/usr/bin
```

## Cross-compile (e.g. arm64 from x86_64)

```bash
make build-arm64
# Example: copy to an arm64 host (replace user/hostname/path)
scp bin/nyxd-arm64 myarm64:/tmp/nyxd && ssh myarm64 'sudo install -m 755 /tmp/nyxd /usr/local/bin/nyxd'
```

Build the **nyx** client for `GOOS=linux GOARCH=arm64` the same way (add a Makefile target if you want both binaries in one shot).

## Optional: CNI plugins

Only if you set **`nyxd -net-driver=cni`**:

```bash
sudo make install-cni
```

## systemd

```bash
sudo cp nyxd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now nyxd
```

The unit in this repo uses **`Type=notify`** but the daemon does not call `sd_notify` yet — if systemd complains, switch the unit to **`Type=simple`**. See comments in `nyxd.service`.

## Data directories

Default **`--base-dir=/var/lib/nyxd`**. Ensure the filesystem is persistent (SD card or USB disk), not a tiny tmpfs-only root.

Only **one nyxd** may use a given base-dir at a time: the daemon takes an exclusive non-blocking flock on **`{base-dir}/run/nyxd-daemon.lock`**. A second start exits with an error instead of corrupting state.

## Next steps

- **[Usage](/docs/getting-started/usage)** — `nyx` commands and `curl` examples  
- **[OpenAPI spec](https://raw.githubusercontent.com/zrougamed/nyxd/main/docs/openapi.yaml)** — HTTP API contract  
- **[Networking](/docs/operations/networking)** — native vs CNI, `nyx run` / stop behavior  
- **[QEMU Alpine](/docs/operations/qemu-alpine)** — try nyxd inside QEMU with Alpine 3.23 before touching bare metal  

## Security scanning (dev machine)

From the repo root (install [Trivy](https://aquasecurity.github.io/trivy/) and [Grype](https://github.com/anchore/grype) first):

```bash
make scan-trivy
make scan-grype
# or both:
make scan
```
