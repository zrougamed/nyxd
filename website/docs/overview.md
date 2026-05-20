---
id: overview
title: Overview
slug: /
---

# nyxd

`nyxd` is a minimal OCI container orchestrator for Linux, built around `crun` and a small control plane.

It is designed for operators who want a lightweight runtime stack without Docker, Podman, or containerd.

## What you get

- Native in-process networking by default, with optional CNI.
- Compose-style workflow via `nyx compose`.
- Supervisor with restart policies and state reconciliation.
- OCI bundle generation, image pulling, and runtime lifecycle control.
- Health checks (exec/http/tcp), logs, and Unix socket control API.

## Start Here

- Installation: [Install nyxd](./getting-started/install.md)
- CLI and daemon usage: [Usage](./getting-started/usage.md)
- Networking model: [Networking](/docs/operations/networking)
- API contract: [API Reference](./reference/api-reference.md)

## Architecture at a glance

Core subsystems in this repository:

- `internal/image`: OCI image fetch and digest verification.
- `internal/overlay`: overlayfs rootfs lifecycle.
- `internal/network`: native and CNI networking backends.
- `internal/bundle`: OCI `config.json` generation.
- `internal/supervisor`: lifecycle and restart orchestration.
- `internal/compose`: compose parsing and service orchestration.

## License

nyxd is licensed under PolyForm Noncommercial 1.0.0. See the repository `LICENSE` for terms.
