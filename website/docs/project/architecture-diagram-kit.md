# Architecture Diagram Kit

Use this page as a canonical source for building architecture diagrams for the repository.

It provides normalized IDs, node definitions, edge lists, and sequence flows that can be mapped directly to Mermaid, C4, Excalidraw, or draw.io.

## Scope

The model covers:

- System context around `nyxd`
- Internal daemon component architecture
- Runtime sequence for `nyx run`
- Reconciliation sequence for `nyx compose up`
- Persistent data layout

## Naming and ID conventions

- Stable node IDs use uppercase snake case.
- Edge IDs use `E##`.
- Sequence step IDs use `R##` (run flow) and `C##` (compose flow).
- Keep labels short and use file paths in notes.

## 1) System context nodes

| ID | Label | Type | Notes |
|---|---|---|---|
| USER | Operator | Actor | Human user running CLI commands |
| NYX | nyx CLI | Application | `cmd/nyx/main.go` |
| NYXD | nyxd Daemon | Application | `cmd/nyxd/main.go` |
| CRUN | crun | External Runtime | OCI runtime binary |
| REGISTRY | OCI Registry | External Service | Docker Hub or compatible registry |
| KERNEL | Linux Kernel Features | Platform | netns, cgroups, overlayfs, nftables |
| CNI_PLUGINS | CNI Plugins | Optional External | Used only when `-net-driver=cni` |
| DATA_STORE | nyxd Data Directory | Storage | Default `/var/lib/nyxd` |

## 2) System context edges

| ID | From | To | Protocol | Meaning |
|---|---|---|---|---|
| E01 | USER | NYX | CLI invocation | User runs commands |
| E02 | NYX | NYXD | HTTP over Unix socket | Control API calls |
| E03 | NYXD | CRUN | Process exec | Runtime lifecycle operations |
| E04 | NYXD | REGISTRY | HTTPS | Manifest and blob pulls |
| E05 | NYXD | KERNEL | Syscalls and net tools | Namespaces, mount, network setup |
| E06 | NYXD | CNI_PLUGINS | CNI exec | Optional CNI ADD/DEL flows |
| E07 | NYXD | DATA_STORE | Filesystem IO | Images, bundles, logs, state |

## 3) Internal daemon container map

| ID | Label | Responsibility | Primary Source |
|---|---|---|---|
| D01 | Control API | Route and validate API requests | `internal/control/server.go` |
| D02 | Supervisor | Container lifecycle and policy loop | `internal/supervisor/supervisor.go` |
| D03 | Compose Builder | Compose parse/validate/spec generation | `internal/compose/specs.go` |
| D04 | Image Store and Puller | Resolve, pull, verify OCI images | `internal/image/puller.go` |
| D05 | Overlay Manager | Layer extraction and overlay mount | `internal/overlay/overlay.go` |
| D06 | Bundle Generator | OCI `config.json` creation | `internal/bundle/bundle.go` |
| D07 | Runtime Adapter | `crun` command wrapper | `internal/runtime/crun.go` |
| D08 | Network Backend | Native or CNI container networking | `internal/network/backend.go` |
| D09 | DNS Backend | Embedded/noop resolver policy | `internal/netdns/backend.go` |
| D10 | Health Subsystem | Readiness and health probes | `internal/health/health.go` |
| D11 | Log Collector | Stream and persist container logs | `internal/logs/logger.go` |
| D12 | Persist and Reconcile | State persistence and re-adoption | `internal/supervisor/persist.go` |

## 4) Internal component edges

| ID | From | To | Meaning |
|---|---|---|---|
| E20 | D01 | D03 | Compose endpoints build service specs |
| E21 | D01 | D02 | Run/stop/remove/compose lifecycle requests |
| E22 | D01 | D04 | Pull/list/remove/prune image operations |
| E23 | D03 | D04 | Ensure images exist for compose services |
| E24 | D03 | D02 | Submit ordered desired service specs |
| E25 | D02 | D05 | Prepare and teardown container rootfs |
| E26 | D02 | D08 | Setup and teardown container networking |
| E27 | D02 | D06 | Build OCI bundle configuration |
| E28 | D02 | D07 | Execute runtime lifecycle operations |
| E29 | D02 | D10 | Run readiness and health monitoring |
| E30 | D02 | D11 | Attach and stream process logs |
| E31 | D02 | D12 | Persist supervised state and reconcile |
| E32 | D02 | D09 | Register and deregister service DNS records |

## 5) Runtime sequence matrix (`nyx run`)

| Step | From | To | Action |
|---|---|---|---|
| R01 | USER | NYX | Run `nyx run <image>` |
| R02 | NYX | D01 | `POST /v1/containers/run` |
| R03 | D01 | D04 | Resolve or pull image if absent |
| R04 | D01 | D02 | Build `ContainerSpec` and request start |
| R05 | D02 | D05 | Prepare overlay rootfs |
| R06 | D02 | D08 | Configure netns and port mappings |
| R07 | D02 | D06 | Generate bundle `config.json` |
| R08 | D02 | D07 | Launch workload through `crun` |
| R09 | D02 | D11 | Start log streaming |
| R10 | D02 | D10 | Start readiness and health tracking |
| R11 | D01 | NYX | Return run result or stream |
| R12 | D02 | D12 | Persist runtime state |

## 6) Reconcile sequence matrix (`nyx compose up`)

| Step | From | To | Action |
|---|---|---|---|
| C01 | USER | NYX | Run `nyx compose up` |
| C02 | NYX | D01 | `POST /v1/compose/up` |
| C03 | D01 | D03 | Parse/validate/interpolate compose file |
| C04 | D03 | D04 | Resolve or pull referenced service images |
| C05 | D03 | D02 | Submit dependency-ordered desired specs |
| C06 | D02 | D02 | Reconcile by service: keep, recreate, or start |
| C07 | D02 | D07 | Restart changed services via runtime operations |
| C08 | D01 | NYX | Return started/reconciled container IDs |

## 7) Persistent data model

| Path | Purpose | Producer |
|---|---|---|
| `images/blobs/sha256/*` | OCI blobs | Image puller |
| `images/images/<repo>/<tag>/` | Manifest/config metadata | Image puller |
| `overlay/<id>/` | Layer extraction and merged rootfs | Overlay manager |
| `bundles/<id>/config.json` | OCI runtime bundle spec | Bundle generator |
| `bundles/<id>/nyxd-meta.json` | Runtime metadata for re-adoption | Supervisor |
| `supervisor/containers/<id>.json` | Persisted supervision record | Persist and reconcile module |
| `run/crun/` | Runtime state root | Runtime adapter and crun |
| `logs/<id>.log` | Container log stream (JSONL) | Log collector |

## 8) Diagram generation checklist

1. Use the node IDs as canonical names in all diagrams.
2. Draw context diagram from section 1 and section 2 only.
3. Draw internal component diagram from section 3 and section 4.
4. Draw sequence diagrams from section 5 and section 6.
5. Draw data layout diagram from section 7.
6. Keep labels consistent so docs and diagrams do not drift.

## 9) Suggested documentation placement

- Overview entry point: [Overview](/docs/)
- Architecture narrative: [Architecture](/docs/project/architecture)
- This machine-readable mapping: Architecture Diagram Kit
