# nyxd documentation

| Doc | Purpose |
|-----|---------|
| [INSTALL.md](INSTALL.md) | Linux install, systemd, cross-build |
| [USAGE.md](USAGE.md) | `nyx` CLI (including **`nyx compose`**), `curl` examples |
| [openapi.yaml](openapi.yaml) | Control HTTP API (Unix socket) — OpenAPI 3 |
| [networking.md](networking.md) | Native vs CNI, flags, `nyx run` / stop |
| [kernel-requirements.md](kernel-requirements.md) | Kernel modules, sysctl, disk |
| [native-network.md](native-network.md) | In-process bridge / IPAM internals |
| [qemu-alpine.md](qemu-alpine.md) | QEMU + Alpine 3.23 for safe testing |
| [ROADMAP.md](ROADMAP.md) | Delivery checklist, **eBPF**, defaults / hardening, anti-goals |
| [../benchmarks/README.md](../benchmarks/README.md) | Docker vs Podman vs nyxd QEMU methodology + result tables |

Community: **[../CONTRIBUTING.md](../CONTRIBUTING.md)** · **[../CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md)** · **[../LICENSE](../LICENSE)** (PolyForm Noncommercial 1.0.0)

Scripts:

- [`../scripts/qemu-alpine-nyxd.sh`](../scripts/qemu-alpine-nyxd.sh) — boot Alpine 3.23 virt ISO under QEMU  
