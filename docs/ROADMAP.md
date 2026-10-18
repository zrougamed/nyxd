# nyxd roadmap

Legend: `[x]` shipped · `[~]` partial · `[ ]` not done.

What 1.x keeps compatible is in [stability.md](stability.md).

---

## 1.0

The first stable release: Linux daemon, `nyx` on Linux, macOS and Windows, packages for every
major distribution.

- [x] **Bounded container logs** — size rotation (`-log-max-size`, `-log-max-files`; 3 × 10 MiB per
      container by default), `nyx logs --tail` reads from the end, `nyx logs -f` follows across
      rotations, lines over 64 KiB are split instead of ending the log stream.
- [x] **No leftovers** — bundle directories are removed when a container exits or is removed;
      `nyx rm` deletes the logs of an exited container.
- [x] **Load and failure testing** — `scripts/stress.sh` / `make test-stress` and the **Stress**
      workflow: 50 containers at once, 200 short-lived ones, a log flood, an OOM kill, a daemon
      crash with containers running. See [stress-testing.md](stress-testing.md).
- [x] **Compatibility promise** — [stability.md](stability.md): `/v1` API, CLI, flags, data,
      log format, packages; deprecation and support policy.
- [x] **Packages and repositories** — deb, rpm, apk, Arch; signed apt, dnf/yum, zypper, apk and
      pacman repositories at `pkgs.nyxd.io`; one-line installers for Linux, macOS and Windows.

---

## Runtime (crun)

- [x] **Lifecycle** — create / start / run / kill / delete / state / list around one `Runtime`
      type; dedicated `--root` state dir; idempotent delete when state is already gone.
- [x] **Exit status** — taken from the foreground `crun run` process (128+N for signal N); stop and
      kill wait for that exit, so shutdown handlers get their full grace period.
- [x] **Exec** — `POST /v1/containers/{id}/exec`, `nyx exec` (TTY, stdin).
- [~] **Exit wait after a daemon restart** — re-adopted containers are polled; containers the
      daemon started itself are waited on directly.

## Supervisor

- [x] **Restart policies** — always / on-failure / unless-stopped / no, exponential backoff with
      jitter (capped at 30 s), `MaxRestarts` gate.
- [x] **Health checks** — exec / HTTP / TCP with interval, timeout, retries, start period;
      readiness before `run` returns; unhealthy → restart when the policy allows.
- [x] **Survives daemon restarts** — running containers are re-adopted from
      `supervisor/containers/*.json`, or from the bundle metadata when that is missing; the
      service units use `KillMode=process`.
- [x] **Ordered start** — Compose `depends_on` order, sequential start.
- [~] **Shutdown budget** — each parallel stop has its own 45 s timeout; there is no global
      deadline beyond the caller's.
- [ ] **`restart: on-failure:N`** — surface `MaxRestarts` in `nyx run` and Compose with the
      Docker syntax.

## Container logs

- [x] **JSON lines per container** with rotation, tail from the end, follow across rotations
      (see 1.0).
- [x] **Follow API** — `GET /v1/containers/{id}/logs?follow=1&tail=…`; `nyx logs -f`, foreground
      `nyx run`.
- [ ] **Other sinks** — journald / syslog forwarding.
- [ ] **Per-container log options** — rotation settings per container or Compose `logging:`.

## Images

- [x] **Pull** — registry v2, manifest lists with platform selection (`-pull-platform`),
      concurrent layer download with digest verification, atomic writes, resumable downloads
      (`Range`), stall watchdog, streamed progress.
- [x] **Credentials** — Bearer/Basic challenges, `nyx login` / `logout`, podman `auth.json` and
      docker `config.json` (credential helpers included); nyxd stores no credentials.
- [x] **Build** — `nyx build`: own Dockerfile parser, multi-stage, `COPY`/`ADD` (`--from`,
      `--chown`, `--chmod`, URLs with `--checksum`), build cache, `.dockerignore`, rootless.
- [x] **Share** — `nyx tag`, `push`, `save` (OCI layout + docker `manifest.json`), `load` (nyx,
      docker and podman archives).
- [x] **Prune** — `nyx image prune` (`--dry-run`), unreferenced blobs removed.
- [x] **Layers** — in-process engine (`internal/layer`, no host `tar`), confined writes,
      whiteouts and opaque directories, ownership and modes kept, layers shared across images.
- [ ] **`RUN --mount`** (cache / secret / bind) and heredocs in Dockerfiles.
- [ ] **Reproducible image IDs** (`SOURCE_DATE_EPOCH`).
- [ ] **zstd layers** — rejected with a clear error today.
- [ ] **Signature verification** — cosign / policy before `run`; today blobs are digest-checked.
- [ ] **Registry mirrors** — `--registry-mirror` for restricted or air-gapped networks.
- [ ] **OAuth identity tokens** for registry logins.

## Networking

- [x] **Native driver** (default) — in-process bridge, veth, IPAM, nftables port publishing; no
      CNI plugins needed. See [networking.md](networking.md).
- [x] **CNI driver** (`-net-driver=cni`) and **host** networking (default rootless).
- [x] **Embedded DNS** — container and Compose service names resolve on the bridge gateway;
      other names are forwarded to the host's resolvers (`-dns`).
- [x] **Compose `internal: true` networks** — traffic leaving the bridge is dropped (native).
- [x] **Rootless published ports** — forwarded by nyxd (TCP and UDP); `/etc/hosts` with the
      stack's service names.
- [ ] **IPv6**.
- [ ] **Separate networks** — Compose `networks:` map to one shared bridge; per-network bridges
      and isolation between stacks are not done.
- [ ] **Isolated rootless networks** — `pasta` as an optional backend.

## Compose

- [x] **File format subset** — services, `image`, `build:`, `command` / `entrypoint`,
      `environment` / `env_file`, `ports`, `volumes` (bind and named), `depends_on`,
      `healthcheck`, `restart`, `deploy.resources` (memory, CPU, pids, OOM score), `read_only`,
      `user`, `cap_add` / `cap_drop`, `privileged`, `seccomp_profile`, `labels`,
      `stop_grace_period`, top-level `name:`, variable substitution and `.env`.
- [x] **Lifecycle** — `nyx compose up` (builds missing images, `--build`), `stop`, `down`
      (`-v` removes named volumes no longer in use).
- [ ] **Implicit named volumes** — `name:/path` without a top-level `volumes:` entry.
- [ ] **Volume commands** — `nyx volume ls` / `rm`.
- [ ] **Unsupported keys** — `expose`, `tmpfs`, `security_opt`, `depends_on.condition` are
      ignored; warn about keys nyxd does not support.

## Security

- [x] **Container defaults** — dropped capabilities, masked and read-only paths,
      `noNewPrivileges`, default seccomp profile (`seccomp_profile: unconfined` per Compose
      service to opt out).
- [x] **Rootless daemon** — user namespace with subordinate IDs, overlay `userxattr`,
      `nyxd unshare`. See [rootless.md](rootless.md).
- [x] **`-userns-remap`** — rootful daemon whose containers run as unprivileged host IDs.
- [x] **Socket access** — `0660` with `-socket-group`; the `nyx` group set up by the packages.
- [x] **Supply chain** — every release signed (GPG); govulncheck, Trivy and Grype in CI; pinned
      actions and tool versions.
- [ ] **Read-only root filesystem by default** — with tmpfs for `/tmp` and `/run`.
- [x] **AppArmor** profile (`apparmor_profile` compose key; skipped when AppArmor is unavailable).
- [ ] **Docker API shim** on a socket for legacy `docker.sock` clients (today: mount host Docker socket or DinD; see [docker-socket.md](docker-socket.md)).
- [ ] **Rootless resource limits** — cgroup v2 delegation (limits are dropped rootless today).
- [ ] **Socket peer checks** — beyond file permissions.

## Daemon and API

- [x] **HTTP API on a Unix socket** — containers, Compose, images, build, push, save/load; OpenAPI
      in [openapi.yaml](openapi.yaml).
- [x] **Service files** for systemd (system and user), OpenRC, runit and SysV, set up by
      `nyxd-service`, the packages and the installers.
- [ ] **Metrics** — `internal/telemetry` exists but is not wired to an endpoint.
- [ ] **Exec errors** — a failed exec can start with `200` and report the error in the stream.
- [ ] **API examples** — small Go, Python and curl clients kept in sync with the OpenAPI file.
- [ ] **Web UI** for containers, stacks, logs and metrics.

## macOS and Windows

- [x] **Windows** — `nyx.exe` (x64, arm64) with `nyx machine` on WSL 2: release-matching distro
      (static nyxd and crun, no shell), no TCP listener, Windows paths in Compose and builds. See
      [windows.md](windows.md).
- [x] **macOS** — `nyx machine` runs nyxd in an Alpine VM on Apple's Virtualization.framework
      (vfkit): `/Users` shared, published ports on localhost, launchd autostart, upgrades keep
      data. See [macos.md](macos.md).
- [ ] **UDP on localhost (macOS)** — published UDP ports answer on the VM address only.
- [ ] **QEMU + WHPX** on Windows where WSL is not available.

## eBPF

Use eBPF where it beats polling or duplicate netfilter accounting, and fit in with hosts that
already run signed BPF programs.

- [ ] **Prerequisites** — BTF, `CAP_BPF` / `CAP_PERFMON`, locked memory; fail closed when missing.
- [ ] **Per-container traffic accounting** — cgroup / tc counters instead of `/proc/net/dev`.
- [ ] **Published-port flow statistics** alongside the nftables rules.
- [ ] **Memory pressure and OOM events** per container.
- [ ] **Host policy** — reuse the host's pinning paths and loader policy; only approved objects.
- [ ] **XDP rate limiting** for host-facing services (opt-in).

## Testing

- [x] **Unit tests** with a statement coverage minimum in CI (91.5%).
- [x] **End-to-end** — rootless, build, transfer, WSL, macOS VM boot, installers for every init
      system, package repositories on 8 distributions, real containers in user namespaces.
- [x] **Stress** — weekly load and failure test, rootful and rootless.

---

*Last reviewed on 2026-10-15. Update `[x]` / `[~]` / `[ ]` when merging features.*
