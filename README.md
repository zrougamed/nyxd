# nyxd

[![CI](https://github.com/zrougamed/nyxd/actions/workflows/ci.yml/badge.svg)](https://github.com/zrougamed/nyxd/actions/workflows/ci.yml)
[![Release](https://github.com/zrougamed/nyxd/actions/workflows/release.yml/badge.svg)](https://github.com/zrougamed/nyxd/actions/workflows/release.yml)

Minimal OCI container orchestrator for **Linux** (x86_64, arm64, and similar) — **`crun`** plus a small control plane, no Docker/Podman/containerd. It started as a lightweight way to run containers on a home **Raspberry Pi** without a heavy stack; the same design runs on **any** capable Linux host (VPS, laptop, edge box, lab VM).

**Zero Docker. Zero Podman. Zero containerd.**

Builds images from Dockerfiles with **`nyx build`** (no Docker/BuildKit; see **[docs/build.md](docs/build.md)**) and moves them with **`nyx push` / `save` / `load`** (see **[docs/images.md](docs/images.md)**). Runs as **root** or **rootless** (start `nyxd` as a normal user; see **[docs/rootless.md](docs/rootless.md)**). On **Windows**, `nyx.exe` drives `nyxd` in a minimal WSL2 distro (see **[docs/windows.md](docs/windows.md)**); on **macOS**, in a small Linux VM on vfkit (see **[docs/macos.md](docs/macos.md)**). Uses `crun` as the OCI runtime. **Networking** defaults to in-process **native** mode; optional **CNI** plugins are supported. See **[docs/networking.md](docs/networking.md)** for `-net-driver`, the startup log line, and `nyx run` / stop behavior.

**Docs index:** **[docs/README.md](docs/README.md)** — install, usage, OpenAPI, QEMU Alpine, kernel notes.
**Security:** **[SECURITY.md](SECURITY.md)** · **[docs/security.md](docs/security.md)** (reporting, threat model, operator guidance).

## Architecture

```
nyxd
├── image/        OCI image puller (raw HTTP, no registry SDK)
├── layer/        OCI layer apply/diff in-process (no host tar)
├── overlay/      overlayfs rootfs management (syscall direct)
├── rootless/     user-namespace re-exec for running nyxd without root
├── network/      Networking: native (default) or CNI plugin executor
├── network/native/  In-process bridge + veth + IPAM (no /opt/cni/bin)
├── bundle/       OCI runtime-spec config.json generator
├── runtime/      crun CLI wrapper
├── supervisor/   restart policy + lifecycle management
├── health/       exec/http/tcp healthchecks
├── log/          container log streaming
├── compose/      Compose subset: parse, env/subst, mounts, stack → supervisor specs
└── build/        Dockerfile builds (parser, RUN via crun, COPY/ADD, cache) — no BuildKit
```

## Dependencies (external)

| Dep | Why | CVE surface |
|-----|-----|-------------|
| `crun` | OCI runtime | Contained, rootless-capable |
| CNI plugins | Only if you pass **`-net-driver=cni`** | Static binaries under `/opt/cni/bin` |
| `nft` | Port NAT rules (native driver) | System `nft` binary |

Go modules (see `go.mod`): OCI spec packages, `gopkg.in/yaml.v3`, `golang.org/x/sys`, **`github.com/hedzr/progressbar`** (nyx pull UI), plus transitive deps.

## Networking (native vs CNI)

- **Default:** `nyxd --net-driver=native` (implicit if omitted). No `/opt/cni/bin` required. Startup logs include `"network backend","driver":"native"`.
- **Optional CNI:** `nyxd -net-driver=cni -cni-bin-dir=/opt/cni/bin ...` after installing plugins (e.g. `make install-cni`).
- **Client:** `nyx` does not choose the driver; restart **`nyxd`** after changing flags.
- **`nyx run`:** foreground streams logs; **Ctrl+C** sends **SIGKILL** via the control API. Use **`nyx run -d`** for detach, **`nyx stop <id>`** for graceful stop, or **`nyx logs <id> -f`** from another shell.

Full detail: **[docs/networking.md](docs/networking.md)**.

## Build

```bash
# Standard (Linux or cross-compiling from your dev machine)
make build
make build-nyx

# Static Linux amd64 binary (useful for minimal rootfs)
make build-static

# Cross-compile daemon for Linux arm64 (e.g. Raspberry Pi 64-bit, Graviton)
make build-arm64
```

## Releases (GitHub Actions)

Create a tag whose name starts with `v` (for example `v0.2.0`) and push it to GitHub. The **Release** workflow runs the same checks as **CI**, then publishes a GitHub Release with `nyxd` and `nyx` binaries for **linux/amd64** and **linux/arm64**, plus `sha256` sidecars.

## Install and usage

```bash
curl -fsSL https://pkgs.nyxd.io/install.sh | sh    # Linux, macOS
```
```powershell
irm https://pkgs.nyxd.io/install.ps1 | iex         # Windows
```

Adds the nyxd package repository for your distribution (apt, dnf, zypper, apk, pacman) and installs from
it, or the binaries elsewhere, and sets `nyxd` up as a service for systemd,
OpenRC, runit or SysV init; on Windows it sets up the WSL2 machine and on macOS the nyx VM, and starts it at login.

See **[docs/INSTALL.md](docs/INSTALL.md)** (Linux install, systemd, cross-build) and **[docs/USAGE.md](docs/USAGE.md)** (`nyx` + `curl`).

API contract: **[docs/openapi.yaml](docs/openapi.yaml)**.

## Daemon state (restart)

The supervisor keeps a **hot map in memory** and also writes one **JSON file per container** under `{baseDir}/supervisor/containers/<id>.json` after a container reaches **running** (full `ContainerSpec` plus netns, bundle, overlay merged path, and assigned IP).

- **Graceful `nyxd` shutdown** (SIGTERM, etc.): `Shutdown` stops supervised containers; when they exit, those JSON files are **removed**. A clean restart therefore starts with an empty supervisor list unless something is re-adopted.
- **Unclean stop** (e.g. `SIGKILL` to nyxd while crun keeps the workload running): on the **next** start, **`reconcilePersisted`** reloads `supervisor/containers/*.json` when crun still reports **running**. If that JSON is missing or corrupt, **`reconcileCrunOrphans`** falls back to **`bundles/<id>/nyxd-meta.json`** plus netns/overlay/crun state so **`nyx ps`** can still recover the container (requires the image to still be resolvable in the local store).

There is **no separate KV database** (Bolt, SQLite, …)—only these JSON records plus crun’s state under `{baseDir}/run/crun`.

Containers started **before** this persistence shipped have **no** JSON record until they are started again at least once while running a build that writes state; until then, re-adoption after a crash cannot find metadata.

## License

This project is licensed under the **[PolyForm Noncommercial License 1.0.0](LICENSE)**.  
**Commercial use** outside the license’s permitted noncommercial purposes **is not allowed** without a **separate written agreement** with the copyright holder(s).

## Contributing

See **[CONTRIBUTING.md](CONTRIBUTING.md)** and the **[Code of Conduct](CODE_OF_CONDUCT.md)**. GitHub provides **bug report** and **feature request** issue forms under **New issue**.

## Security scans (repo checkout)

```bash
make scan          # Trivy + Grype (tools must be on PATH)
make scan-trivy
make scan-grype
```

## Try in QEMU first

**[docs/qemu-alpine.md](docs/qemu-alpine.md)** and **`scripts/qemu-alpine-nyxd.sh`** — boot **Alpine 3.23** virt media under QEMU to try nyxd safely before bare metal (e.g. an SBC SD card).

**[benchmarks/README.md](benchmarks/README.md)** — methodology and tables for comparing **nyxd** vs **Docker** vs **Podman** on three matching Alpine 3.23 QEMU guests under the same workload.

## Security posture

- `NoNewPrivileges=true` in every container spec
- Minimal capability set (no CAP_SYS_ADMIN, no CAP_SYS_PTRACE)
- `/proc`, `/sys` masked and read-only paths enforced
- Network namespace per container (bridge + NAT; native driver by default)
- overlayfs read-only lower layers
- Digest verification on every pulled blob (sha256)
- Atomic writes everywhere (write-to-tmp, rename)
- No goroutine leaks: all goroutines tied to context
- No memory leaks: explicit cleanup on container removal

## Data layout

```
/var/lib/nyxd/
├── images/
│   ├── blobs/sha256/<hex>        # compressed layer blobs
│   └── images/<repo>/<tag>/     # manifest.json + config.json
├── overlay/
│   ├── _layers/<sha256-hex>/       # extracted layers, shared by all containers (lowerdirs)
│   └── <containerID>/
│       ├── diff/                   # writable upper layer
│       ├── work/                   # overlayfs workdir
│       └── merged/                 # container rootfs (mount point)
├── bundles/<containerID>/
│   ├── config.json               # OCI runtime-spec
│   └── nyxd-meta.json            # image + publish + IP (re-adopt if supervisor JSON missing)
├── supervisor/containers/
│   └── <containerID>.json        # persisted spec + paths (re-adopt after unclean restart)
├── build/
│   ├── cache/<key>.json          # build cache: step key → layer digest
│   └── contexts/, bundles/       # temporary build contexts and RUN bundles
├── run/
│   ├── crun/                     # crun --root state
│   └── nyxd-daemon.lock          # exclusive flock: one nyxd per base-dir
└── logs/<containerID>.log        # JSONL container logs

/run/nyxd/netns/<containerID>  # network namespace bind-mounts
```
