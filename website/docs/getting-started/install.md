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
| **Go 1.27+** (build only) | On the target host or cross-compile from a dev machine. |

Networking defaults to **`-net-driver=native`** (no `/opt/cni/bin`). See [networking](/docs/operations/networking).

## Quick install

One command per platform. It downloads the release, checks it against the signed `checksums.txt`, and sets
`nyxd` up as a service that starts at boot.

```bash
# Linux and macOS
curl -fsSL https://github.com/zrougamed/nyxd/releases/latest/download/install.sh | sh
```

```powershell
# Windows (PowerShell, no admin rights needed once WSL is enabled)
irm https://github.com/zrougamed/nyxd/releases/latest/download/install.ps1 | iex
```

What it does:

| Platform | Installs | Service |
|----------|----------|---------|
| Debian, Ubuntu, Fedora, RHEL, openSUSE, Arch, Alpine | the native package (`.deb`, `.rpm`, `.pkg.tar.zst`, `.apk`), so upgrades and removal go through your package manager; `crun` comes as a dependency | enabled and started for the init system in use |
| Any other Linux (Void, Gentoo, Slackware, ...) | `nyxd`, `nyx` in `/usr/local/bin`; installs `crun` when it can | same |
| Linux, `--rootless` | `nyxd`, `nyx` in `~/.local/bin`, no root | systemd **user** service |
| macOS | `nyx` in `/usr/local/bin`, vfkit (Homebrew), the nyx VM that runs `nyxd` | launchd agent runs `nyx machine run` at login ([macos.md](/docs/getting-started/macos)) |
| Windows | `nyx.exe` in `%LOCALAPPDATA%\Programs\nyx` (added to `PATH`), the nyx WSL2 distro | logon task "nyx machine" runs `nyx machine start` |

The service is set up for **systemd**, **OpenRC**, **runit** or **SysV init**, whichever the machine runs (no
running init, as in a container: it says how to start `nyxd` by hand). Options:

```bash
curl -fsSL .../install.sh | sh -s -- --version v0.10.0     # a given release
curl -fsSL .../install.sh | sh -s -- --method binary       # skip the native package
curl -fsSL .../install.sh | sh -s -- --rootless            # per user, no root daemon
curl -fsSL .../install.sh | sh -s -- --no-start            # set up and enable, do not start
curl -fsSL .../install.sh | sh -s -- --uninstall [--purge] # --purge also deletes images and containers
```

Afterwards, let your user talk to the daemon without sudo (log in again afterwards):

```bash
sudo usermod -aG nyx "$USER"      # Alpine: sudo addgroup "$USER" nyx
nyx ping
```

## Install from a release package

Each [release](https://github.com/zrougamed/nyxd/releases) has signed packages for amd64 and arm64.
They install `nyxd` and `nyx` to `/usr/bin`, pull in `crun`, create the `nyx` group, and **install, enable and
start the service** for your init system (an upgrade restarts it, removal stops and disables it). Check the
signature first ([verifying downloads](https://github.com/zrougamed/nyxd/blob/main/docs/packaging.md#verifying-downloads-users)).

```bash
sudo apt install ./nyxd_<version>_amd64.deb                 # Debian, Ubuntu
sudo dnf install ./nyxd-<version>-1.x86_64.rpm              # Fedora, RHEL, Rocky, Alma
sudo pacman -U nyxd-<version>-1-x86_64.pkg.tar.zst          # Arch Linux
sudo apk add --allow-untrusted ./nyxd_<version>_x86_64.apk  # Alpine (after checking its .sig)
```

To install without starting it (images built for later, configuration first), put `NYXD_NO_START=1` in
`/etc/default/nyxd` before installing.

## Managing the service

Settings live in **`/etc/default/nyxd`** for every init system; restart after a change:

```bash
NYXD_OPTS="-log-level info -net-driver native"   # any nyxd flags (nyxd -h)
NYXD_NO_START=0                                  # 1: package installs/upgrades do not (re)start it
```

| Init system | Status / restart | Logs |
|-------------|------------------|------|
| systemd | `systemctl status nyxd` / `systemctl restart nyxd` | `journalctl -u nyxd` |
| OpenRC | `rc-service nyxd status` / `rc-service nyxd restart` | `/var/log/nyxd.log` |
| runit | `sv status nyxd` / `sv restart nyxd` | runit's log (stderr) |
| SysV | `/etc/init.d/nyxd status` / `/etc/init.d/nyxd restart` | `/var/log/nyxd.log` |

`/usr/lib/nyxd/nyxd-service` (`/usr/local/lib/nyxd/` for `install.sh` binary installs) does the same on any of
them: `nyxd-service status|restart|setup|teardown|detect`.

## Install to `/usr/bin`

From a repo build:

```bash
sudo make install-usr
# equivalent: sudo make install BINDIR=/usr/bin
```

## Rootless (no root at all)

Run **`nyxd` as your normal user** and it switches to rootless mode on its own: it creates a user namespace,
keeps data in `~/.local/share/nyxd`, and listens on `$XDG_RUNTIME_DIR/nyxd/nyxd.sock`, which `nyx` finds
automatically. Requirements, limits and the systemd user unit are in **[Rootless](/docs/operations/rootless)**.

## Windows

On Windows, `nyx.exe` runs `nyxd` in its own small WSL2 distro: `nyx machine init` then `nyx machine start`.
See **[Windows](/docs/getting-started/windows)**.

## Use `nyx` without sudo (rootful daemon)

With a **rootful** `nyxd`, the **`nyx` client** can run as a normal user if it can open the control socket.

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
