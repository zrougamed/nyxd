# Architecture

This page explains how `nyxd` is structured so new contributors can quickly map commands, APIs, and runtime behavior to source code.

## System context

At runtime, the system has these actors:

- User runs `nyx` commands.
- `nyx` talks to `nyxd` over a Unix socket HTTP API.
- `nyxd` orchestrates images, rootfs, networking, and lifecycle.
- `crun` executes OCI containers.
- OCI registries provide image manifests and layer blobs.
- Linux kernel features provide namespaces, cgroups, overlayfs, nftables, and veth.

The design goal is a minimal stack: no Docker, no Podman, no containerd.

## High-level architecture

### 1) Daemon entrypoint

- `cmd/nyxd/main.go`
- Parses flags, initializes subsystems, starts the control API, and runs shutdown logic.

### 2) CLI client

- `cmd/nyx/main.go`
- A thin client that dispatches commands to daemon endpoints.

### 3) Control API

- `internal/control/server.go`
- Exposes `/v1/*` routes for run, pull, exec, logs, compose, image and container operations.

### 4) Supervisor (core orchestrator)

- `internal/supervisor/supervisor.go`
- Owns container start/stop/remove, restart policy behavior, health integration, and lifecycle supervision.

### 5) Compose translation

- `internal/compose/parse.go`
- `internal/compose/specs.go`
- Parses compose YAML, validates dependencies, resolves images, and builds ordered `ContainerSpec` values.

### 6) Image subsystem

- `internal/image/puller.go`
- Pulls and verifies OCI manifests/blobs and persists image metadata.

### 7) Rootfs subsystem

- `internal/overlay/overlay.go`
- Extracts layers and mounts overlayfs merged rootfs per container.

### 8) OCI bundle generation

- `internal/bundle/bundle.go`
- Produces OCI `config.json` including namespaces, mounts, capabilities, seccomp, resources, and env/args.

### 9) Runtime adapter

- `internal/runtime/crun.go`
- Wraps `crun` CLI for run/create/start/exec/state/kill/delete/list.

### 10) Network subsystem

- Interface: `internal/network/backend.go`
- Native default: `internal/network/native/manager.go`
- Optional CNI path: `internal/network/cni.go`

### 11) DNS policy

- `internal/netdns/backend.go`
- Selects resolver backend based on daemon flags and driver mode.

### 12) Health and logs

- Health checks: `internal/health/health.go`
- Log collector: `internal/logs/logger.go`

### 13) Persistence and recovery

- `internal/supervisor/persist.go`
- Persists supervisor metadata and supports re-adoption of running workloads after unclean daemon restarts.

## Request-to-runtime flow

### `nyx run`

1. User runs `nyx run`.
2. `nyx` posts to `POST /v1/containers/run`.
3. Control handler validates request and resolves container ID.
4. If image is absent, daemon pulls image from registry.
5. Supervisor prepares overlay rootfs and network namespace.
6. Bundle module writes OCI `config.json`.
7. Runtime module invokes `crun`.
8. Supervisor starts log streaming and health monitoring.
9. API returns container info or stream status to client.

### `nyx compose up`

1. `nyx` sends compose file reference to `POST /v1/compose/up`.
2. Compose module parses YAML, interpolates env, validates graph.
3. Compose builder resolves/pulls required images and creates ordered specs.
4. Supervisor reconciles desired state:
   - unchanged services stay running
   - changed services are stopped and recreated
   - missing services are started
5. API returns service container IDs.

## Filesystem data model

Under the daemon base directory (default `/var/lib/nyxd`):

- `images/`: pulled manifests/configs and blob cache
- `overlay/`: extracted layers and merged rootfs trees
- `bundles/<id>/`: OCI bundle files and run metadata
- `supervisor/containers/`: persisted per-container supervisor records
- `run/crun/`: runtime state root
- `logs/<id>.log`: JSONL container logs

This keeps state simple and inspectable without introducing a separate database.

## Design properties

- Small and explicit component boundaries.
- Linux-first implementation.
- Runtime backend abstraction for networking modes.
- Control plane remains local-first (Unix socket).
- Supervisor-centric lifecycle and recovery semantics.
