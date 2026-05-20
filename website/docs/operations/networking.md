# nyxd networking

This document describes how the daemon picks a network implementation, how that maps to code, and how to run with **native** (default) or **CNI** plugins.

## Who chooses the driver?

**Only `nyxd` (the daemon)** selects the network backend via CLI flags. The **`nyx` client** does not set `-net-driver`; it talks to the control socket. After you change networking flags, **rebuild and restart `nyxd`** so the new binary and options are in effect.

## DNS (`-dns`)

nyxd can run a small **embedded DNS** server on the bridge gateway (UDP port 53, default `NYXD_GATEWAY_IP` / `10.88.0.1`) that answers **A** records for container hostnames (`<hostname>` and `<hostname>.nyxd.local`). Containers receive a bind-mounted `/etc/resolv.conf` pointing at that gateway when registration applies.

| `-dns` value | Behaviour |
|--------------|-------------|
| **`auto`** (default) | **Native** (`-net-driver=native`): embedded DNS for every supervised container. **CNI**: embedded DNS only for **Compose** stacks where each service is attached solely to networks declared with `internal: true` (see compose `networks:`). |
| **`embedded`** | Always use embedded DNS (including all services on CNI). |
| **`cni`** | Do not run embedded DNS; use CNI-side DNS (for example the **`dnsname`** plugin in your conflist). |
| **`off`** | No embedded DNS and no `/etc/resolv.conf` override from nyxd. |

Set **`NYXD_GATEWAY_IP`** (and **`NYXD_CONTAINER_SUBNET`**) to match the bridge your CNI plugin uses if it differs from the native defaults.

## Default: native (no `/opt/cni/bin`)

By default, `nyxd` runs with:

```bash
sudo ./nyxd --log-level info
# equivalent explicit flag:
# sudo ./nyxd --net-driver=native --log-level info
```

On startup you should see a JSON log line similar to:

```json
{"msg":"network backend","driver":"native",...}
```

Then run a container:

```bash
sudo ./nyx pull nginx:alpine
sudo ./nyx run nginx:alpine
```

**Foreground vs detach:** by default, `nyx run` prints the JSON response, then **waits**; **Ctrl+C** (or SIGTERM) calls **`POST /v1/containers/{id}/stop`** so the workload is torn down (similar to `docker run` without `-d`). Use **`nyx run -d`** / **`--detach`** to exit immediately after start (old behavior). You can also run **`nyx stop <id>`** from another terminal.

Native networking is implemented under `internal/network/native/` (bridge, veth, IPAM, nftables-based port maps). It does **not** require the standard CNI plugin bundle under `/opt/cni/bin`.

## Optional: CNI plugins (`-net-driver=cni`)

To use the **exec-based** path that shells out to standard CNI plugins (`bridge`, `host-local`, etc.), start the daemon with:

```bash
sudo ./nyxd -net-driver=cni \
  -cni-bin-dir=/opt/cni/bin \
  -cni-conf-dir=/etc/cni/net.d \
  -network=nyx \
  --log-level info
```

Install plugins first, for example:

```bash
make install-cni
# or install containernetworking/plugins to /opt/cni/bin by your own process
```

You should then see:

```json
{"msg":"network backend","driver":"cni",...}
```

## Code layout: `network.Backend` and the supervisor

### `network.Backend`

The interface is defined in `internal/network/backend.go`:

- `EnsureNetwork()` — host-wide setup (bridge / CNI conflist, etc.)
- `Setup(ctx, containerID, netNSPath, ports, opts *SetupOptions)` — join the container netns, return allocated IP (or equivalent). `opts.Internal` (compose `networks.*.internal: true`) is enforced on the **native** driver by nftables forward drops for IPv4 traffic leaving the bridge CIDR; **CNI** ignores `opts` for now (configure plugins separately).
- `Teardown(ctx, containerID, netNSPath)` — tear down that container’s networking

Implementations:

| Implementation | Package / type | Notes |
|----------------|----------------|--------|
| Native | `internal/network/native` — `*native.Manager` | In-process; no CNI binaries on disk |
| CNI exec | `internal/network` — `*network.Manager` | Runs plugins from `-cni-bin-dir` |

Both satisfy `network.Backend` (see compile-time assertions in `native/manager.go` and `network/cni.go`).

### Supervisor

`internal/supervisor` holds a `network.Backend`, not a concrete `*network.Manager`. `supervisor.New(...)` also receives the selected DNS backend and `-dns` / `-net-driver` policy for compose stack registration; see **DNS (`-dns`)** above.

### `network.PortMapping`

`supervisor.ContainerSpec` uses `[]network.PortMapping`. The native stack imports and uses **`network.PortMapping`** as well, so port maps from the API/supervisor align with native setup without duplicate structs.

## systemd

The repo’s `nyxd.service` example runs with **`--net-driver=native`** and does not mount `/etc/cni` in `ReadWritePaths` (native mode does not need it). If you switch the unit to `-net-driver=cni`, add `/etc/cni` (and ensure `/opt/cni/bin` is available on the host) as appropriate for your layout.

## Related docs

- [Native network internals](native-network.md) — bridge, IPAM, nftables, kernel modules
- [Kernel requirements](/docs/operations/kernel-requirements) — modules and sysctl
- [Roadmap](/docs/project/roadmap) — delivery checklist including `-net-driver`
