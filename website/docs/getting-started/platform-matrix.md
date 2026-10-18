# Platform and environment feature matrix

Where each piece of nyxd runs, and which features apply.

## How to read this

| Symbol | Meaning |
|--------|---------|
| **Yes** | Supported in that environment |
| **Partial** | Works with documented limits |
| **No** | Not available (rejected, ignored, or not applicable) |
| **N/A** | Does not apply to that environment |

**Environments**

| Environment | What actually runs containers |
|-------------|-------------------------------|
| **Linux rootful** | `nyxd` as root on the host (system service or `sudo nyxd`) |
| **Linux rootless** | `nyxd` as your user (user namespace); automatic when not root |
| **Linux `-userns-remap`** | Rootful daemon; containers mapped to subordinate host UIDs |
| **macOS (`nyx machine`)** | `nyx` on the Mac; `nyxd`+`crun` inside an Alpine **VM** (vfkit) |
| **Windows (`nyx machine`)** | `nyx.exe` on Windows; `nyxd`+`crun` inside a **WSL 2** distro |

On macOS and Windows the **CLI is native**; the **runtime is always Linux** inside the machine. Compose, AppArmor, bridges, and cgroups are evaluated on that Linux side.

## Client and install

| Feature | Linux rootful | Linux rootless | Linux userns-remap | macOS machine | Windows machine |
|---------|:-------------:|:--------------:|:------------------:|:-------------:|:---------------:|
| `nyx` CLI | Yes | Yes | Yes | Yes | Yes (`nyx.exe`) |
| Installer / packages | Yes | Yes | Yes | Yes (`install.sh`) | Yes (`install.ps1`) |
| Control API (Unix socket) | Yes | Yes | Yes | Yes (vsock → Mac sock) | Yes (WSL ↔ Windows) |
| `nyx machine` | N/A | N/A | N/A | Yes | Yes |
| TCP listen for API | No | No | No | No | No |

## Runtime and images

| Feature | Linux rootful | Linux rootless | Linux userns-remap | macOS machine | Windows machine |
|---------|:-------------:|:--------------:|:------------------:|:-------------:|:---------------:|
| Run OCI containers (`crun`) | Yes | Yes | Yes | Yes (in VM) | Yes (in WSL) |
| `nyx compose` | Yes | Yes | Yes | Yes | Yes |
| Pull / tag / push / save / load | Yes | Yes | Yes | Yes | Yes |
| `nyx build` (no Docker/BuildKit) | Yes | Yes | Yes | Yes | Yes |
| Restart policies / healthchecks | Yes | Yes | Yes | Yes | Yes |
| Bounded JSON logs | Yes | Yes | Yes | Yes | Yes |

## Networking

| Feature | Linux rootful | Linux rootless | Linux userns-remap | macOS machine | Windows machine |
|---------|:-------------:|:--------------:|:------------------:|:-------------:|:---------------:|
| Default `-net-driver` | `native` | `host` | `host` | `native` (in VM) | `native` (in WSL)¹ |
| Native bridge (`nyxbr0`) | Yes | No (rejected) | No (rejected) | Yes (in VM) | Yes (in WSL)¹ |
| Compose per-network `bridge`+`subnet` | Yes | No | No | Yes (in VM) | Yes (in WSL)¹ |
| Cross-bridge isolation (nft) | Yes | N/A | N/A | Yes (in VM) | Yes (in WSL)¹ |
| Host networking | Yes | Yes (default) | Yes (default) | Partial² | Partial² |
| CNI (`-net-driver=cni`) | Yes | No | No | Possible in VM | Possible in WSL |
| Embedded DNS / `.nyxd.local` | Yes (native/auto) | No (host resolv) | No (host resolv) | Yes (in VM) | Yes (in WSL)¹ |
| Publish ports to host loopback | Yes | Yes (userland fwd) | Yes | Partial³ | Yes |
| Publish UDP on localhost | Yes | Yes | Yes | Partial³ | Yes |
| Service names via `/etc/hosts` | Via DNS on native | Yes (host net) | Yes (host net) | Via DNS in VM | Via DNS in WSL¹ |

¹ Windows WSL distro runs rootful nyxd with native networking by default (same model as a Linux server inside WSL).  
² Host network inside the VM/WSL shares that Linux network namespace, not the Mac/Windows host namespace.  
³ macOS: TCP published ports are forwarded to Mac `localhost`; UDP may answer only on the VM address (see [macOS](/docs/getting-started/macos)).

## Security and isolation

| Feature | Linux rootful | Linux rootless | Linux userns-remap | macOS machine | Windows machine |
|---------|:-------------:|:--------------:|:------------------:|:-------------:|:---------------:|
| User namespace isolation | Optional (`-userns-remap`) | Yes (always) | Yes | VM boundary + Linux inside | WSL boundary + Linux inside |
| cgroup resource limits | Yes | No (dropped) | Yes | Yes (in VM) | Yes (in WSL) |
| Default seccomp profile | Yes | Yes | Yes | Yes | Yes |
| `seccomp_profile: unconfined` | Yes | Yes | Yes | Yes | Yes |
| Compose `apparmor_profile` | Partial⁴ | Partial⁴ | Partial⁴ | No⁵ | No⁵ |
| `privileged` / `cap_add` | Yes | Partial⁶ | Yes | Yes (in VM) | Yes (in WSL) |
| Mount host `docker.sock` | Yes (hybrid) | Partial⁷ | Yes | Partial⁸ | Partial⁸ |

⁴ Applied only when the **Linux** host has AppArmor mounted **and** the named profile is loaded; otherwise ignored with a warning (SELinux-only, Alpine, etc.).  
⁵ AppArmor is a Linux LSM; the Mac/Windows hosts have none. Profiles are not applied inside the default Alpine/WSL images unless you load AppArmor there yourself.  
⁶ Rootless cannot grant real host privileges (modules, host net config, device nodes) even with `--privileged`.  
⁷ Root-owned Docker socket is usually unreachable from a rootless user namespace.  
⁸ Only if that socket exists **inside** the VM/WSL (or is shared into it); mounting a Windows/macOS Docker Desktop socket path is a different layout.

## Paths and mounts

| Feature | Linux rootful | Linux rootless | Linux userns-remap | macOS machine | Windows machine |
|---------|:-------------:|:--------------:|:------------------:|:-------------:|:---------------:|
| Default data dir | `/var/lib/nyxd` | `$XDG_DATA_HOME/nyxd` | `/var/lib/nyxd` | VM disk under `~/Library/nyx/machines` | WSL disk (`ext4.vhdx`) |
| Bind-mount host project dirs | Yes | Yes | Yes | Yes under `/Users` | Yes (Windows paths translated) |
| Bind-mount arbitrary host paths | Yes | Yes | Yes | No (only shared virtio-fs) | Partial (via WSL path rules) |

## Related docs

- [Install](/docs/getting-started/install) — Linux packages and installers  
- [macOS](/docs/getting-started/macos) — vfkit VM details  
- [Windows](/docs/getting-started/windows) — WSL 2 distro details  
- [Rootless](/docs/operations/rootless) — user-namespace mode  
- [Networking](/docs/operations/networking) — drivers, DNS, AppArmor notes  
- [Docker sockets](/docs/operations/docker-socket) — hybrid Docker API / DinD  
- [Stability](/docs/reference/stability) — support policy  
