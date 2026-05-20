# Reference workload

Define **one** workload contract so all three stacks implement the same behavior.

## Recommended minimal pattern

1. **Image:** pinned digest or immutable tag (e.g. `nginx:1.29` with digest recorded after first pull).
2. **Container:** publish host port `8080` → container `80`, single replica.
3. **Warm-up:** `GET /` for 30s or N requests (discard timings).
4. **Measure:** fixed-duration load from a **separate** client (not inside the VM) to avoid skewing guest CPU.

## Example load (client)

```bash
# Example only — tune -c -d to match your SLA experiment
wrk -t4 -c100 -d60s --latency http://<VM_IP>:8080/
```

Record **wrk** version and full command line in the results file.

## nyxd column (illustrative)

```bash
sudo nyxd --log-level info   # or debug for one traced run
nyx pull nginx:1.29
nyx run -d -p 8080:80 nginx:1.29
# … run client from host …
nyx stop <id> && nyx rm <id>
```

## Docker / Podman

Keep port mapping and image ref aligned with the nyxd column. Document whether Podman is **rootful** or **rootless**; stay consistent across runs.

## Optional “same load” script

Add a small `run-load.sh` here if you automate the client phase; keep it POSIX `sh` and dependency-light where possible.
