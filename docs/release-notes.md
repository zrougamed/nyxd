# Release notes

## 1.1.3

### Fixed

- macOS: vfkit exited immediately (`the VM stopped on its own`) when the machine
  lived under `~/Library/Application Support/…` — spaces break vfkit’s `unix://`
  vsock URLs. Machines now live in `~/Library/nyx/machines` (legacy dirs are
  moved on first use). `nyx machine logs` also shows `vfkit.log`.

## 1.1.2

### Fixed

- macOS: after a failed start or an upgrade, a leftover `run.pid` made `nyx machine run`
  exit with "already running", so vfkit never restarted and `nyx` fell back to
  `/run/nyxd/nyxd.sock`. `nyx machine start` and upgrades now replace a supervisor that
  is not answering.

## 1.1.1

### Fixed

- macOS: `nyx machine start` timed out with `nyxd did not answer within 120s` even when
  the VM console showed nyxd ready. vfkit was started with vsock `listen` (guest dials
  host) while the VM image listens on vsock; it now uses `connect` so the Mac can reach
  the API and shutdown ports. Reinstall or replace `nyx` only — the VM image is unchanged.

## 1.1.0

Compose and native-network controls for multi-bridge stacks, plus a macOS VM init fix
and docs for what works in each environment. Compatible with 1.0; see
[Stability and compatibility](stability.md).

### New

- **Compose `apparmor_profile`**: applied when the host has AppArmor and the named profile
  is loaded; otherwise logged and skipped so the same file works on SELinux-only or Alpine
  hosts.
- **Compose `dns` and `/etc/resolv.conf`**: set nameservers from the compose file, or keep a
  caller-owned resolv.conf bind mount without nyxd rewriting it.
- **Compose `ulimits` and `memory_swap`**: `ulimits` map to OCI rlimits; deploy memory swap
  follows Docker-style defaults when omitted.
- **`GET /v1/network`**: returns the active driver, bridge, gateway and subnet over the
  control socket.
- **Per-network native bridges**: compose `networks` with `bridge` + `subnet` get their own
  `nyxbr*` bridge, IPAM, NAT, cross-bridge isolation and embedded DNS zone; services that
  omit `networks` stay on `nyxbr0`.
- **Platform matrix**: feature availability for Linux rootful/rootless/userns-remap and
  macOS/Windows machines — [platform-matrix.md](platform-matrix.md).
- **Docker socket docs**: mounting a host `docker.sock` or running DinD under nyxd —
  [docker-socket.md](docker-socket.md).

### Changed

- Socket-group access (`--socket-group`) is documented as root-equivalent for container
  operations (same trust model as the Docker group).
- Custom native bridge names must start with `nyxbr`; foreign host interfaces are not
  adopted.

### Fixed

- macOS: `nyx machine init` failed with `VM image: rootfs.img missing` because release
  tarballs pack that file as a GNU sparse member.
- Embedded DNS could race or deadlock under concurrent zone updates and shutdown.
- Reject unsafe container IDs on `run` so paths cannot escape the data directory.
- IPAM no longer panics on `/31` or `/32` subnets; tiny prefixes are rejected with an error.
- Website dependency advisory on the old Faker 5 package used by the OpenAPI explorer.

## 1.0.0

The first stable release. From here on, nyxd follows [Semantic Versioning](https://semver.org): the
`/v1` API, the `nyx` CLI and its flags, daemon flags, on-disk data, the log format and the packages
stay compatible for all of 1.x. What is covered, and what is not, is in
[Stability and compatibility](stability.md).

### New

- **Bounded container logs**: size rotation with `-log-max-size` and `-log-max-files` (3 × 10 MiB
  per container by default). `nyx logs --tail` reads from the end of the file, `nyx logs -f`
  follows across rotations, and lines over 64 KiB are split instead of ending the stream.
- **No leftovers**: bundle directories are removed when a container exits or is removed, and
  `nyx rm` deletes the logs of an exited container.
- **Load and failure testing**: `scripts/stress.sh` (`make test-stress`) and the **Stress** workflow
  run 50 containers at once, 200 short-lived ones, a log flood, an OOM kill and a daemon crash with
  containers running. See [Load and failure testing](stress-testing.md).
- **Package repositories**: signed apt, dnf/yum, zypper, apk and pacman repositories at
  `pkgs.nyxd.io`, and one-line installers for Linux, macOS and Windows. See
  [Get started](https://nyxd.io/get-started).
- **`make help`**: `make` with no target lists every target.
- **`nyx --version`** (`-v`) prints the client version without needing the daemon.
- **Clearer errors when nyxd can't be reached**: `nyx` says whether the daemon isn't running or
  you lack access to its socket (join the `nyx` group), instead of a raw `dial unix` error.

### Changed

Check these before upgrading from 0.9:

- **`nyx run` no longer restarts containers by default.** Without `--restart`, the policy is now
  `no`, as in Docker and in `nyx compose`. Before, it was `unless-stopped`. Add
  `--restart unless-stopped` to containers that should come back after they exit.
- **A memory limit now limits swap too.** A container with a memory limit and no swap value can
  use at most twice the limit in memory plus swap (Docker's default). Before, swap was unlimited,
  so a container over its limit swapped instead of being OOM-killed.
- **Container logs are rotated.** Logs used to grow without bound; they now keep 3 × 10 MiB per
  container unless you set `-log-max-size` / `-log-max-files`.

### Fixed

- `nyx rm` of a container that had just stopped could leave its log files behind.
- The last output of a container that exits right after writing it could be missing from its
  log.
- Rootless: after the daemon was killed, `nyx exec` into the containers it re-adopted could fail with
  `Permission denied`. The new daemon now joins the user namespace those containers run in.
- A foreground `nyx run` of a very short command could hang while its container restarted in a
  loop.
