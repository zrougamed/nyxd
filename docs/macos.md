# nyx on macOS (the nyx VM)

Linux images need a Linux kernel, so on macOS `nyxd` runs in a small **Linux VM** and `nyx` talks to it from
the Mac. The VM runs on Apple's Virtualization.framework through [vfkit](https://github.com/crc-org/vfkit).
There is no Docker Desktop, no TCP listener, and no admin right needed.

Feature availability vs Linux rootful/rootless: [platform-matrix.md](platform-matrix.md).

```bash
nyx machine init          # create the VM (downloads the image matching this nyx)
nyx machine start         # boot it; nyx now talks to the nyxd inside
nyx run alpine:3.20 uname -a
```

## Requirements

| Need | Notes |
|------|-------|
| macOS 13 (Ventura) or later, Apple silicon or Intel | |
| vfkit | `brew install vfkit` (the installer does this when Homebrew is there) |

## Install

```bash
curl -fsSL https://pkgs.nyxd.io/install.sh | sh
```

It installs `nyx` in `/usr/local/bin`, installs vfkit with Homebrew if it is missing, creates the VM and makes
it start at login. Run it as your user, not with `sudo`: the VM belongs to you. Running it again upgrades
`nyx` and the VM's system; images, containers and volumes are kept. `--no-start` sets everything up without
starting the VM; `--uninstall` stops it and removes `nyx`, keeping the VM's data; `--uninstall --purge`
also deletes the VM.

## `nyx machine`

| Command | What it does |
|---------|--------------|
| `nyx machine init [--image FILE\|URL] [--cpus N] [--memory MiB] [--disk-size GiB]` | Create the VM (default 2 GiB memory, half the CPUs, a 64 GiB data disk), or upgrade its system to this release when it exists, keeping data and settings. `--disk-size` can grow the data disk later, never shrink it. |
| `nyx machine start` / `stop` | Boot the VM / shut it down cleanly. |
| `nyx machine status` | Whether nyxd answers, the VM's size, address and image version. |
| `nyx machine logs` | The VM console: boot messages and nyxd's log. |
| `nyx machine autostart on\|off` | Start the VM at login (a launchd agent), and restart it if it fails. |
| `nyx machine rm` | Stop the VM and delete it with all its images, containers and volumes. |
| `nyx machine run` | Run the VM in the foreground (what `start` and the launchd agent run). |

`--name NAME` picks a machine other than the default `nyx`. When a VM exists, `nyx` uses its socket by
default; `NYXD_SOCKET` or `-socket` still win.

## How it works

The machine lives in `~/Library/Application Support/nyx/machines/<name>/`:

| File | |
|------|---|
| `vmlinuz`, `initramfs`, `rootfs.img` | The VM's system: Alpine Linux with OpenRC, a static `nyxd` and `crun`. Replaced on upgrade. |
| `data.img` | `/var/lib/nyxd` in the VM: images, containers, volumes. Sparse, so it only takes the space nyxd writes. Kept on upgrade. |
| `nyxd.sock` | nyxd's API, carried out of the VM over vsock by vfkit. |
| `console.log`, `vfkit.log`, `run.log` | Logs (`nyx machine logs` shows the console). |

`nyx machine run` starts vfkit and stays with it: it forwards published container ports to `localhost` on the
Mac, and on `stop` asks the VM to shut down cleanly: over vsock, then with the virtual power button, then a
hard stop after 70 seconds.

Inside the VM, nyxd runs as root with native networking, so each container gets its own address and
containers do not share ports, as on a Linux server. The VM is the isolation boundary between containers and
the Mac.

## Paths and bind mounts

`/Users` is shared into the VM at the same path (virtio-fs), so build contexts and Compose bind mounts under
your home directory work unchanged:

```bash
nyx build -t app .                # the context is read from the Mac
nyx compose up                    # volumes: - ./Caddyfile:/etc/caddy/Caddyfile:ro works
```

Paths outside `/Users` (for example `/tmp` or `/opt`) are not shared; they refer to the VM's own filesystem.

## Networking

Containers reach the internet through the Mac's NAT. A port published with `-p 8080:80` answers on
`localhost:8080` on the Mac (TCP); it is also reachable on the VM's address (`nyx machine status`). UDP ports
are reachable on the VM's address only.

## Building from source

```bash
GOOS=darwin go build -o nyx ./cmd/nyx
make vm-image                           # bin/nyx-vm-arm64.tar.gz (needs docker or podman, and binfmt on amd64)
make vm-image VM_ARCH=amd64             # Intel Macs
nyx machine init --image bin/nyx-vm-arm64.tar.gz
```

A development build of `nyx` has no matching release, so `init` needs `--image`.

## End-to-end tests on Linux

`make test-vm` builds the amd64 image and boots it under QEMU/KVM with the devices vfkit gives it (virtio
disks, virtio-net, a virtio console, vsock). It checks that nyxd answers over vsock, that a published port
answers, that the data disk survives a shutdown and grows with `--disk-size`, and both shutdown paths.
`scripts/vm-smoke.sh bin/nyx-vm-arm64.tar.gz` does the same for arm64, emulated (about 10 minutes). It needs
`qemu-system-x86_64` (or `-aarch64`), access to `/dev/kvm` and `/dev/vhost-vsock`, and `socat`.

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `nyx machine needs vfkit` | `brew install vfkit`. |
| `this nyx is a development build` | Pass `--image` with an image from `make vm-image`. |
| `nyxd did not answer within 120s` | `nyx machine logs` shows the VM console. |
| A bind mount is empty | Only `/Users` is shared; move the files under your home directory. |
| `machine directory ... is too deep for a Unix socket` | macOS limits socket paths to 104 bytes; use a shorter `--name`. |
