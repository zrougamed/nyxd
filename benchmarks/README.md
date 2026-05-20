# Benchmarks: nyxd vs Docker vs Podman (Alpine 3.23 / QEMU)

Compare **the same workload** on three **as similar as possible** Alpine Linux 3.23 guests under QEMU. One VM runs **Docker**, one **Podman**, one **nyxd** + **nyx**. Goal: apples-to-apples numbers for startup, resource use, and steady-state behavior—not a claim of “fastest in all cases.”

## Environment matrix (fill before each run)

| Setting | Docker VM | Podman VM | nyxd VM |
|--------|-----------|-----------|---------|
| QEMU CPU model / `-smp` | | | |
| RAM (`-m`) | | | |
| Disk backend (qcow2 raw, cache mode) | | | |
| Alpine version / kernel (`uname -a`) | | | |
| Docker version (`docker version`) | | | |
| Podman version (`podman version`) | | | |
| nyxd commit / `nyx version` | | | |
| Date / timezone | | | |

Keep **CPU count, RAM, and disk** identical across VMs. Prefer **virtio** NIC and disk. Snapshot or reinstall from the **same base image** between product runs.

## Workload (must be identical)

1. Use the script or contract in [`workload/README.md`](workload/README.md) (e.g. pull fixed image tag → run container with fixed flags → warm-up → measured phase).
2. Record **exact** commands for each stack (Dockerfile / `podman run` / `nyx pull` + `nyx run`) in your run notes so runs are reproducible.
3. Run the same **client-side load generator** from a fourth machine or the host against the **published container port** (same concurrency, duration, and seed if applicable).

## What to measure (example rows)

Copy [`TEMPLATE.md`](TEMPLATE.md) into `results/YYYY-MM-DD-run-N.md` and fill after each trial.

Suggested metrics:

| Metric | How to capture (examples) |
|--------|---------------------------|
| Cold pull → first ready | time until health check passes |
| Daemon RSS after steady state | `ps` / `systemd-cgtop` |
| Container start latency | `time` wrapper or daemon logs |
| Steady CPU % | `pidstat`, `top` batch mode |
| Load test throughput / p99 | `wrk`, `hey`, `vegeta` (same binary + args) |
| Stop / remove latency | scripted teardown |

## Tracing (optional)

- Enable **nyxd** `--log-level debug` for one run; archive logs under `results/…/`.
- **Docker**: `journalctl -u docker` or daemon debug (document flags).
- **Podman**: rootless vs rootful affects numbers—**pick one** and keep it consistent across all runs in a series.

## Ethics / license

This repo is **PolyForm Noncommercial** for the nyxd code; benchmark numbers and your scripts in `results/` are yours. Do not redistribute proprietary vendor binaries from inside this folder.

## See also

- [docs/qemu-alpine.md](../docs/qemu-alpine.md) — booting Alpine 3.23 under QEMU for nyxd experiments.
- [docs/USAGE.md](../docs/USAGE.md) — `nyx` commands used in the nyxd column.
