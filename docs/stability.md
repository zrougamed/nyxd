# Stability and compatibility

From **1.0.0**, nyxd follows [Semantic Versioning](https://semver.org): within a major
version (1.x), upgrades do not break what this page lists as stable. Minor releases (1.1, 1.2)
add features; patch releases (1.0.1) only fix bugs and security issues.

## Stable in 1.x

### HTTP API (`/v1`)

The control API on the Unix socket, as described in [openapi.yaml](openapi.yaml)
(`/v1/containers…`, `/v1/compose…`, `/v1/images…`, `/v1/build`, `/v1/ping`, `/v1/version`).

- Routes, request fields and response fields are not removed or renamed, and do not change type
  or meaning.
- New routes, new optional request fields and new response fields can appear in any minor
  release. **Clients must ignore response fields they do not know.**
- HTTP status classes are stable (`2xx` success, `404` unknown container or image, `4xx` bad
  request, `5xx` daemon-side failure). The text of error messages is not; do not parse it.
- Streaming responses (pull progress, build output, `logs?follow=1`, `exec`) keep their framing.

A breaking change to the API needs a new prefix (`/v2`); `/v1` then stays available for the
rest of 1.x.

### `nyx` CLI

- Commands and flags documented in [USAGE.md](USAGE.md) keep working with the same meaning.
- Machine-readable output is stable: `--json`, `-q` / `--quiet`, and `nyx logs` with the
  container's own lines. Tables and progress output are for people and can change.
- Exit status: `0` on success, non-zero on failure.

### `nyxd` daemon

- Command-line flags keep their name and meaning; `/etc/default/nyxd` (`NYXD_OPTS`) keeps
  working.
- Defaults can become safer in a minor release (for example tighter container defaults); such
  changes are listed in the release notes with the flag that restores the old behavior.
- The control socket path (`/run/nyxd/nyxd.sock` rootful, `$XDG_RUNTIME_DIR/nyxd/nyxd.sock`
  rootless) and the `nyx` group stay the same.

### Data and upgrades

- Upgrading within 1.x keeps pulled and built images, named volumes and running containers:
  nyxd re-adopts running containers when it starts again.
- Container log files keep their format: one JSON object per line with `time`, `container_id`,
  `stream` and `log` (more fields may be added), rotated as `<id>.log`, `<id>.log.1`, …
- The rest of the data directory (`/var/lib/nyxd`, or `$XDG_DATA_HOME/nyxd` rootless) is
  internal: use the API or `nyx` rather than reading or editing it.
- Downgrading to an older minor release is not supported; back up the data directory first if
  you may need to.

### Compose

The supported subset of the Compose file format (see [USAGE.md](USAGE.md#compose-stacks-nyx-compose))
keeps working the same way. Support for more keys is added in minor releases.

### Packages and installers

- The `nyxd` package name, the repositories at `https://pkgs.nyxd.io`, the one-line installers
  (`install.sh`, `install.ps1`) and their flags, and the service names stay the same.
- The release signing key (fingerprint `C910 E8A2 2680 CCCB BB7E  6ED2 F0DC FE8A 242B 1E99`) stays
  the same; a new key is announced in a release before packages are signed with it.

## Not covered

- Go packages in this repository (`internal/…`, `pkg/…`): they are not a library API.
- Human-readable output: tables, progress bars, log messages of the daemon itself.
- Features marked **experimental** in their documentation; they can change in a minor release.
- Behavior that contradicts the documentation: that is a bug, and fixing it is not a breaking
  change.

## Deprecation

A stable feature is first **deprecated** in a minor release: the release notes say so, the
feature keeps working and, where possible, warns when used. It is removed no earlier than the
next major release.

## Supported releases

The latest minor release receives bug fixes. Security fixes go to the latest two minor
releases. The package repositories keep the last five releases.

## Supported platforms

| | |
|-|-|
| Linux | kernel and cgroup v2 requirements in [kernel-requirements.md](kernel-requirements.md); x86_64 and arm64 |
| Distributions with packages | Debian, Ubuntu, Fedora, RHEL and compatible, openSUSE / SLES, Alpine, Arch (see [INSTALL.md](INSTALL.md)) |
| macOS | `nyx` with `nyx machine` (Apple silicon and Intel), see [macos.md](macos.md) |
| Windows | `nyx` with `nyx machine` on WSL 2 (x64 and arm64), see [windows.md](windows.md) |
