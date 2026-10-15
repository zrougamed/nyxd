# nyxd documentation

| Doc | Purpose |
|-----|---------|
| [INSTALL.md](INSTALL.md) | Linux install, systemd, cross-build |
| [packaging.md](packaging.md) | Release packages (deb, rpm, apk, Arch, macOS, Windows), GPG signing, verifying downloads |
| [build.md](build.md) | `nyx build`: Dockerfile builds without Docker/BuildKit, cache, supported syntax |
| [images.md](images.md) | `nyx tag` / `push` / `save` / `load`: registries, archives, credentials |
| [USAGE.md](USAGE.md) | `nyx` CLI (including **`nyx compose`**), `curl` examples |
| [openapi.yaml](openapi.yaml) | Control HTTP API (Unix socket) — OpenAPI 3 |
| [windows.md](windows.md) | Windows: `nyx.exe` + the nyx WSL2 distro (`nyx machine`) |
| [macos.md](macos.md) | macOS: `nyx` + the nyx VM on vfkit (`nyx machine`) |
| [rootless.md](rootless.md) | Run nyxd as a normal user (user namespace, limits, `nyxd unshare`, `-userns-remap`) |
| [networking.md](networking.md) | Native vs CNI vs host, flags, `nyx run` / stop |
| [kernel-requirements.md](kernel-requirements.md) | Kernel modules, sysctl, disk |
| [native-network.md](native-network.md) | In-process bridge / IPAM internals |
| [image-layers.md](image-layers.md) | In-process layer extraction, whiteouts, security rules |
| [qemu-alpine.md](qemu-alpine.md) | QEMU + Alpine 3.23 for safe testing |
| [stability.md](stability.md) | What 1.x keeps compatible: API, CLI, flags, data, packages; deprecation and support |
| [stress-testing.md](stress-testing.md) | Load and failure test (`scripts/stress.sh`) and its CI workflow |
| [ROADMAP.md](ROADMAP.md) | What is shipped, what 1.0 needed, what comes next |
| [../benchmarks/README.md](../benchmarks/README.md) | Docker vs Podman vs nyxd QEMU methodology + result tables |

Community: **[../CONTRIBUTING.md](../CONTRIBUTING.md)** · **[../CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md)** · **[../LICENSE](../LICENSE)** (PolyForm Noncommercial 1.0.0)

Scripts:

- [`../scripts/qemu-alpine-nyxd.sh`](../scripts/qemu-alpine-nyxd.sh) — boot Alpine 3.23 virt ISO under QEMU  
