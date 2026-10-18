# Rootless nyxd

nyxd runs without root. Start it as a normal user and it re-executes itself inside its own **user namespace**,
where that user is root. Containers run inside that namespace too: root in a container is your user on the host,
never real root.

Compared with rootful Linux and macOS/Windows machines: [Platform matrix](/docs/getting-started/platform-matrix).

```bash
nyxd                                   # as your user: rootless mode is automatic
nyx pull alpine:3.20
nyx run alpine:3.20 id                 # uid=0(root) inside, your uid on the host
```

`nyx` finds the rootless daemon on its own (see [Socket](#paths)).

## Requirements

| Need | Why | Check |
|------|-----|-------|
| Linux ≥ 5.11 | overlayfs inside a user namespace (`userxattr`) | `uname -r` |
| Unprivileged user namespaces | the daemon creates one | `unshare -Ur true` |
| `crun` | OCI runtime | `crun --version` |
| `newuidmap` / `newgidmap` + a range in `/etc/subuid` and `/etc/subgid` | map more than one ID (recommended) | `grep "^$USER:" /etc/subuid /etc/subgid` |

On Ubuntu 24.04 and later, AppArmor blocks unprivileged user namespaces by default. Allow them:

```bash
sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
echo 'kernel.apparmor_restrict_unprivileged_userns=0' | sudo tee /etc/sysctl.d/60-nyxd-userns.conf
```

Add a subordinate ID range if your user has none (the `uidmap` package provides `newuidmap`/`newgidmap`):

```bash
sudo usermod --add-subuids 100000-165535 --add-subgids 100000-165535 "$USER"
```

## How it works

```
nyxd (your uid)          host process: writes the ID maps, forwards signals, waits
 └─ nyxd (root in userns) the daemon: overlay mounts, image store, control socket
     └─ crun → container  same user namespace; own pid / mount / ipc / uts namespaces
```

1. `nyxd` starts as your user and spawns itself with a new user + mount namespace.
2. It maps the namespace with `newuidmap`/`newgidmap`: `0` → your uid, `1…65536` → your subordinate range.
3. The child re-executes itself once mapped, so it starts as root **in the namespace** with full capabilities
   there (and none on the host).
4. Mounts are made private to the namespace; overlayfs is mounted with `userxattr`.

The host process only waits and forwards `SIGTERM`/`SIGINT`, so `systemctl --user stop` and Ctrl+C shut down
cleanly.

**After a crash.** Containers keep running when the daemon is killed, in the user namespace of the daemon that
started them. A new `nyxd` finds them through crun's pid files and joins that namespace (with util-linux
`nsenter` and `unshare`, and a new mount namespace) instead of creating one, so `nyx exec`, `stop` and `rm` work on
the re-adopted containers. Without `nsenter` or `unshare`, it creates a new namespace; the containers are re-adopted
but, on kernels that restrict access across user namespaces, `nyx exec` into them fails with `Permission denied`.

### ID mapping modes

| Mode | When | Effect |
|------|------|--------|
| **subordinate IDs** | `newuidmap`, `newgidmap` and ranges in `/etc/subuid` and `/etc/subgid` | Image file ownership is kept (e.g. `/etc/shadow` group `shadow`). Recommended. |
| **single ID** | any of the above missing (nyxd prints a warning) | Only your uid is mapped. Every file in an image is owned by `0`, and container processes keep your host supplementary groups (shown as `nobody`) because an unprivileged process cannot call `setgroups`. |

## `-userns-remap`: root without root containers

When `nyxd` must start as root (a system service, or the [Windows WSL distro](/docs/getting-started/windows)), `-userns-remap` gives
it the same isolation as rootless mode:

```bash
sudo nyxd -userns-remap=default          # = 100000:65536
sudo nyxd -userns-remap=200000:65536     # START:COUNT
```

Root writes the ID maps itself (no `newuidmap`/`newgidmap`, no `/etc/subuid` entry): `0…COUNT-1` in the namespace
→ `START…START+COUNT-1` on the host. The daemon and its containers then run in that namespace, so container root
is uid `START` on the host, with no host capabilities. Image file ownership is kept.

Compared with rootless mode:

- Paths keep their rootful defaults (`/var/lib/nyxd`, `/run/nyxd/nyxd.sock`); nyxd hands both directories to the
  mapped range.
- `-net-driver` defaults to `host`; `native` is rejected, as in rootless mode.
- No cgroup manager, so no resource limits, and no cgroup v2 requirement.
- Pick a range that no other user or service uses (`/etc/subuid`).

## Paths

Defaults change in rootless mode; any flag you pass explicitly wins.

| Setting | Rootful default | Rootless default |
|---------|-----------------|------------------|
| `-base-dir` | `/var/lib/nyxd` | `$XDG_DATA_HOME/nyxd` (usually `~/.local/share/nyxd`) |
| `-socket` | `/run/nyxd/nyxd.sock` | `$XDG_RUNTIME_DIR/nyxd/nyxd.sock` |
| `-net-driver` | `native` | `host` |

`nyx` uses `NYXD_SOCKET` when set. Otherwise, for a non-root user, it uses `$XDG_RUNTIME_DIR/nyxd/nyxd.sock` when
that socket exists, and falls back to `/run/nyxd/nyxd.sock`.

## Networking

Rootless nyxd uses the **`host`** network driver: containers share the host network namespace, so every container
sees the same `localhost` and the same ports.

- **Published ports work.** `-p 8443:1443` / Compose `ports: ["8443:1443"]`: the workload listens on 1443 and nyxd
  forwards host port 8443 to it (TCP and UDP), like Docker's rootless userland proxy. When both numbers match
  (`8080:8080`) nothing is forwarded: the workload's own socket is the published port. Forwards stop with the
  container and come back when nyxd restarts and re-adopts it. A host port another process holds fails the start with
  "host port … is already in use".
- **Service names resolve.** Each container gets an `/etc/hosts`: the host's own entries, plus its stack's service and
  container names on `127.0.0.1` (`db`, `cache`, `webapp-db`, …). `postgres://db:5432` and `reverse_proxy app:8080`
  work as they do on a Compose network.
- **Ports below 1024** (`net.ipv4.ip_unprivileged_port_start`, 1024 by default) cannot be bound by a non-root user on
  the host network, by the workload or by nyxd's forwarder. A container that declares one fails before it starts,
  with the fix: `sudo sysctl net.ipv4.ip_unprivileged_port_start=80` (persist it in `/etc/sysctl.d/`), or use a port
  from 1024 up. A program that picks its own port regardless (Caddy's automatic HTTPS binds 80 and 443) needs its
  config changed too, for example Caddy's `{ http_port 1080  https_port 1443 }`.
- **One shared network.** Two services that listen on the same port collide, and `internal: true` networks cannot be
  isolated: `nyx compose up` prints a warning for both. Run nyxd as root for separate networks.
- `/etc/resolv.conf` is the host's, so DNS works as on the host (including `127.0.0.53`). The embedded DNS
  (`<name>.nyxd.local`) is not used.

`-net-driver=native` needs real root and is rejected in rootless mode. Isolated rootless networks (through `pasta`)
are planned.

## Limits compared to rootful

- **No resource limits.** The daemon's namespace owns no cgroup, so crun runs with `--cgroup-manager=disabled`.
  `cpus`/`memory`/`pids` settings are dropped with a warning. (cgroup v2 delegation is planned.)
- **No host-only privileges.** Containers cannot load kernel modules, change host network settings, or create
  device nodes, even with `--privileged`; those need real root.
- **Files are owned by subordinate IDs on disk.** You cannot delete everything under `~/.local/share/nyxd` with a
  plain `rm`. Use `nyxd unshare`.

## `nyxd unshare`

Runs a command inside the rootless namespace, as root there. Use it to inspect or clean up rootless data:

```bash
nyxd unshare ls -ln ~/.local/share/nyxd/overlay/_layers
nyxd unshare rm -rf ~/.local/share/nyxd       # full reset (stop nyxd first)
nyxd unshare                                  # interactive shell
```

## systemd user service

```bash
install -Dm755 bin/nyxd ~/.local/bin/nyxd
install -Dm644 packaging/linux/nyxd-user.service ~/.config/systemd/user/nyxd.service
systemctl --user daemon-reload
systemctl --user enable --now nyxd
loginctl enable-linger "$USER"     # keep running after logout
```

`NoNewPrivileges` must stay off in the unit: `newuidmap`/`newgidmap` are setuid helpers.

## Testing

```bash
make test-rootless      # builds, starts nyxd as your user, pulls, runs, execs, stops
```

CI runs the same script on GitHub's Ubuntu runners.

## Security notes

- The daemon has **no capabilities on the host**. A container escape lands in a namespace where "root" is your user.
- The only privileged code involved is the distribution's `newuidmap`/`newgidmap` (setuid), used once at startup.
- Container defaults are unchanged: seccomp profile, reduced capabilities, `noNewPrivileges`, masked `/proc` paths.
