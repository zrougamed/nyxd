# Release notes

## 1.0.0

The first stable release. From here on, nyxd follows [Semantic Versioning](https://semver.org): the
`/v1` API, the `nyx` CLI and its flags, daemon flags, on-disk data, the log format and the packages
stay compatible for all of 1.x. What is covered, and what is not, is in
[Stability and compatibility](/docs/reference/stability).

### New

- **Bounded container logs**: size rotation with `-log-max-size` and `-log-max-files` (3 × 10 MiB
  per container by default). `nyx logs --tail` reads from the end of the file, `nyx logs -f`
  follows across rotations, and lines over 64 KiB are split instead of ending the stream.
- **No leftovers**: bundle directories are removed when a container exits or is removed, and
  `nyx rm` deletes the logs of an exited container.
- **Load and failure testing**: `scripts/stress.sh` (`make test-stress`) and the **Stress** workflow
  run 50 containers at once, 200 short-lived ones, a log flood, an OOM kill and a daemon crash with
  containers running. See [Load and failure testing](/docs/operations/stress-testing).
- **Package repositories**: signed apt, dnf/yum, zypper, apk and pacman repositories at
  `pkgs.nyxd.io`, and one-line installers for Linux, macOS and Windows. See
  [Get started](/get-started).
- **`make help`**: `make` with no target lists every target.

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
