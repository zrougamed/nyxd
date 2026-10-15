# nyx on Windows (WSL2)

Linux images need a Linux kernel, so on Windows `nyxd` runs in a small **WSL2 distro** and `nyx.exe` talks to
it from Windows. There is no Docker Desktop, no TCP listener, and no admin right needed once WSL is enabled.

```powershell
nyx machine init          # import the nyx distro (downloads the release matching nyx.exe)
nyx machine start         # start nyxd in it
nyx run alpine:3.20 uname -a
```

## Requirements

| Need | Notes |
|------|-------|
| Windows 10 2004+ or Windows 11, x64 or ARM64 | |
| WSL2 | Once, as administrator: `wsl --install --no-distribution`, then reboot. After that, nothing needs admin. |

No other distro is needed: nyx brings its own.

## Install

One command in PowerShell (no administrator rights needed once WSL is enabled):

```powershell
irm https://pkgs.nyxd.io/install.ps1 | iex
```

It downloads `nyx.exe` for your CPU (x64 or ARM64), checks it against `checksums.txt`, puts it in
`%LOCALAPPDATA%\Programs\nyx` and on your `PATH`, imports the nyx WSL2 distro (`nyx machine init`), starts
`nyxd`, and registers a logon task, **"nyx machine"**, that runs `nyx machine start` at every sign-in (hidden;
the Startup folder is used instead where policy forbids user tasks). If WSL is not enabled yet it says so:
run `wsl --install --no-distribution` as administrator, restart, and run the installer again.

Running it again **upgrades**: a new `nyx.exe`, and the `nyxd` inside the existing distro is replaced in place, so
images and containers are kept. With options, download the script first:

```powershell
.\install.ps1 -Version v0.10.0     # a given release
.\install.ps1 -NoAutostart         # no logon task
.\install.ps1 -NoMachine           # nyx.exe only (for a nyxd elsewhere)
.\install.ps1 -Uninstall           # remove nyx.exe, the task and the PATH entry; keep the distro
.\install.ps1 -Uninstall -Purge    # also unregister the distro, deleting images and containers
```

### Manual install

1. Download `nyx-windows-amd64.exe` (or `-arm64.exe` for Windows on ARM) and `checksums.txt` from the
   [releases](https://github.com/zrougamed/nyxd/releases), check it, rename it to `nyx.exe` and put it on your `PATH`:

   ```powershell
   (Get-FileHash nyx-windows-amd64.exe -Algorithm SHA256).Hash.ToLower()   # compare with its line in checksums.txt
   gpg --verify nyx-windows-amd64.exe.sig nyx-windows-amd64.exe             # optional, with Gpg4win
   ```

2. Create and start the machine:

   ```powershell
   nyx machine init
   nyx machine start
   nyx ping
   ```

`nyx machine init` downloads `nyx-wsl-<arch>.tar.gz` from the release with the same version as `nyx.exe`,
checks it against the published `.sha256`, and imports it with `wsl --import ... --version 2`.

## `nyx machine`

| Command | Effect |
|---------|--------|
| `init [--image FILE\|URL]` | Import the distro. `--image` takes a local tarball (`.tar` or `.tar.gz`) or an `http(s)` URL (a `.sha256` must sit next to it). |
| `start` | Start `nyxd` in the distro (detached; waits up to 60 s for it to answer). |
| `stop [--terminate]` | Stop `nyxd` gracefully. `--terminate` also shuts the distro down. |
| `rm` | Stop `nyxd` and unregister the distro. **Deletes its images and containers.** |
| `status` | Show whether `nyxd` answers. |
| `logs` | Print the daemon log. |

Every command takes `--name NAME` (default `nyx`) to run more than one machine.

Data lives in `%LOCALAPPDATA%\nyx\machines\<name>\`: the WSL disk (`disk\ext4.vhdx`) and `nyxd.log`.

## How it works

```
nyx.exe ── wsl.exe -d nyx --exec nyxd stdio-proxy ──▶ /run/nyxd/nyxd.sock ──▶ nyxd (user namespace)
   (Windows)                (one per API connection)          (inside the WSL2 VM)      └─ crun → containers
```

- **Endpoint.** On Windows the default endpoint is `wsl://nyx`. For each API connection, `nyx` runs
  `nyxd stdio-proxy` in the distro and speaks HTTP over its stdin/stdout. Use `-socket wsl://NAME` or
  `NYXD_SOCKET=wsl://NAME` for another machine.
- **The distro** contains only a static `nyxd`, a static `crun` (pinned version, checksum-verified at build
  time), the Mozilla CA bundle (pinned, for registry TLS), `/etc/wsl.conf` and a few account files. There is
  **no shell and no package manager**. Root's login shell is `/usr/sbin/nologin` (nyxd under another name),
  which only prints a hint, so a plain `wsl -d nyx` cannot start a daemon outside the remapped namespace.
  `wsl.conf` turns off Windows interop, so nothing in the VM can start Windows programs.
- **No root containers.** `nyx machine start` runs `nyxd -userns-remap=default`: the daemon and its containers
  run in a user namespace where container root is uid 100000 of the WSL VM. See
  [rootless.md](rootless.md#-userns-remap-root-without-root-containers).

## Paths and bind mounts

Windows drives are mounted in the VM as `/mnt/<drive>`. `nyx` translates paths it sends to the daemon:

| On Windows | Seen by nyxd |
|------------|--------------|
| `C:\Users\me\app` | `/mnt/c/Users/me/app` |
| `D:\data` | `/mnt/d/data` |
| `\\server\share` | not supported: copy the files to a local drive |

`nyx compose` passes the compose file this way, so relative bind mounts (`./data:/data`) resolve under its
directory. Absolute Windows bind sources inside a compose file are translated by nyxd the same way:
`C:\data:/data:ro` and `C:/data:/data` mount `/mnt/c/data`. `nyx build` sends the context as a tar stream,
so it works from any directory; files get mode `0755` (Windows has no Unix permission bits; same as
`docker build` on Windows). Use `COPY --chmod` where a mode matters.

Files under `/mnt/c` cross the Windows/Linux file system boundary and are slow for heavy I/O. Keep databases and
build caches in named volumes or in the image.

## Networking

`nyxd` in the distro uses the `host` network driver: containers share the network of the WSL2 VM.
WSL forwards ports opened in the VM to `localhost` on Windows (default `localhostForwarding=true`), so a container
listening on port 8080 is reachable at `http://localhost:8080`. `-p/--publish` is ignored with a warning, as in
[rootless mode](rootless.md#networking).

## Limits

- No resource limits (`cpus`, `memory`, `pids` are dropped with a warning). Limit the whole VM in `.wslconfig`.
- `host` networking only (see above).
- Native Windows containers (Windows images) are not supported; only Linux images.

## Building from source

On Linux (or in any WSL distro):

```bash
make build-windows                 # bin/nyx-windows-amd64.exe, bin/nyx-windows-arm64.exe
make wsl-rootfs                    # bin/nyx-wsl-amd64.tar.gz (+ .sha256)
make wsl-rootfs WSL_ARCH=arm64     # Windows on ARM
```

A development build of `nyx.exe` does not know which release to download; pass the tarball:

```powershell
nyx machine init --image .\nyx-wsl-amd64.tar.gz
```

The tarball is reproducible (sorted entries, numeric owner `0`, mtime from the last commit or
`SOURCE_DATE_EPOCH`). CI builds both architectures and fails on known vulnerabilities in the distro (Grype).
Grype cannot identify the static `crun` binary, so its version is pinned in `scripts/build-wsl-rootfs.sh` and
updated by hand (as is the CA bundle date).

## End-to-end tests on Linux

```bash
make test-wsl        # builds nyx + the distro tarball, runs scripts/wsl-smoke.sh
```

`nyx` drives `scripts/fake-wsl.sh` (through `NYX_WSL_EXE`) instead of `wsl.exe`. The fake boots the imported
tarball the way WSL2 boots a distro: its own user, mount and pid namespace shared by every `--exec`, the tarball
as the namespace root (no host files, no shell), `/proc`, `/sys`, `/dev`, a `/run` tmpfs, `/mnt/c`, and a
generated `/etc/resolv.conf`. VM root is your user and VM uids 100000–165535 are your subordinate range, so
`nyxd -userns-remap=default` runs unchanged. No root needed; the requirements are those of
[rootless mode](rootless.md#requirements) plus `nsenter` and `python3`.

The script checks: `machine init` from a URL (checksum mismatch rejected), no shell and the login-shell hint,
`start`/`status`/`logs`, pull and run with container root at VM uid 100000, `exec`, a build whose 5 MB context
must arrive intact, compose with `C:\` and relative binds, `stop` (daemon only), restart with data kept,
`stop --terminate`, a second machine (`--name`, local `--image`) with its own data, and `rm`.


## Troubleshooting

| Symptom | Check |
|---------|-------|
| `start wsl.exe: ... (is WSL installed?)` | `wsl --status`; run `wsl --install --no-distribution` as administrator. |
| `nyxd did not answer within 60s` | `nyx machine logs` |
| `This distro runs nyxd only and has no shell.` | You ran `wsl -d nyx` without `--exec`; use `nyx.exe`. |
| `this nyx is a development build` | Pass `--image` with a tarball from `make wsl-rootfs`. |
| Version of the daemon | `wsl -d nyx --exec /usr/local/bin/nyxd -version` |
| Start over | `nyx machine rm`, then `nyx machine init` |

The distro has no shell: always run commands in it with `wsl -d nyx --exec ...`.
