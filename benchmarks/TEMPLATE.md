# Benchmark run — _title / date_

## Summary

_One paragraph: what was compared and the main takeaway._

## Results table

| Metric | Docker (VM A) | Podman (VM B) | nyxd (VM C) | Notes |
|--------|---------------|---------------|-------------|-------|
| Image pull (cold) | | | | same ref, clear cache method |
| First request after start | | | | define “ready” |
| Steady RSS (daemon + workload) | | | | how measured |
| p50 / p99 latency (ms) | / / | / / | / / | load tool + RPS |
| Throughput (req/s) | | | | |
| Stop + remove total (s) | | | | |
| Disk used (image + layers) | | | | `df` / `du` method |

## Commands log

### Docker

```text
(paste exact sequence)
```

### Podman

```text
(paste exact sequence)
```

### nyxd / nyx

```text
(paste exact sequence)
```

## Raw artifacts

_Link or list paths under `results/…` for logs, wrk output, `ps` snapshots._
