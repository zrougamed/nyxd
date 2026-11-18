# Using nyxd and nyx

## nyx CLI (recommended)

Install **`nyx`** next to **`nyxd`** (`make build-nyx`). It talks to the daemon over the Unix socket (default `/run/nyxd/nyxd.sock`, override with `-socket` or `NYXD_SOCKET`; a remote daemon with `--host ssh://...`, see below). To use **`nyx` as a normal user** (no `sudo`), run **`nyxd --socket-group=…`** and add your user to that POSIX group — see **[INSTALL.md](INSTALL.md)** (`Use nyx without sudo`, `Install to /usr/bin`).

```bash
nyx ping
nyx version
nyx info          # mode, storage, kernel, cgroups, crun, counts (--json for bug reports)

# Pull (progress UI on stderr; add --json for a single JSON blob on stdout)
sudo nyx pull nginx:alpine

# Private registry (optional; same fields as POST /v1/images/pull JSON)
sudo nyx pull --username "$USER" --password "$TOKEN" registry.example.com/myapp:1.0

# Run — foreground prints container id, then streams logs; Ctrl+C sends SIGKILL (POST /v1/containers/{id}/kill)
sudo nyx run nginx:alpine

# Command after the image, like docker run ("--" before it is optional)
sudo nyx run alpine:3.20 sh -c 'echo hello; exit 3'   # nyx exits; the daemon logs exitCode 3

# Interactive shell (docker-like --rm -it) and one-shot cleanup
sudo nyx run --rm -it alpine:3.20
sudo nyx run --rm hello-world

# Detach: prints container id only (use --json for the full JSON object on stdout)
sudo nyx run -d nginx:alpine
sudo nyx run -d --json nginx:alpine

# If the image is not in the local store, nyxd pulls it automatically before starting.

# Named container
sudo nyx run --name web nginx:alpine

# Docker-style flags: publish, env, hostname, restart
sudo nyx run -p 8080:80 -e MYVAR=1 --hostname web --restart no nginx:alpine
# --restart defaults to no, as in Docker; use unless-stopped or always for a long-running service
sudo nyx run -d --name web --restart unless-stopped nginx:alpine
# Limits and a read-only root with scratch space (limits need cgroups; rootless without delegation ignores them)
sudo nyx run -d -m 256m --cpus 0.5 --pids-limit 200 nginx:alpine
sudo nyx run --rm --read-only --tmpfs /tmp:size=64m alpine:3.20 touch /tmp/ok
# Bind mounts (absolute paths on the nyxd host) and the host's network
sudo nyx run --rm -v /srv/data:/data:ro alpine:3.20 ls /data
sudo nyx run --rm --network host alpine:3.20 ip addr
# Names: letters, digits, '-' and '_' (not as the first character: reserved for nyxd)

# Logs (optional follow; tail count)
sudo nyx logs web
sudo nyx logs -f --tail 100 web
sudo nyx container logs web -f

# List / remove (docker-like)
sudo nyx ps
sudo nyx ps -q
sudo nyx ps -a                     # also exited containers, with their exit code
sudo nyx stop web other-id
sudo nyx start web                 # start a stopped container again, same settings
sudo nyx restart -t 5 web          # stop (SIGKILL after 5s) and start
sudo nyx rm web

# Live resource usage (docker/podman-like); --no-stream prints one sample
sudo nyx stats
sudo nyx stats --no-stream web
sudo nyx compose stats --no-stream
sudo nyx compose stats web

# Low-level details as a JSON array
sudo nyx inspect web
sudo nyx container inspect web

# Images (local refs pulled into the store)
sudo nyx image ls
sudo nyx image rm nginx:alpine
sudo nyx image prune              # remove image metadata not referenced by any running container
sudo nyx image prune --dry-run      # list what would be removed

# Daemon restart: clean shutdown stops workloads and removes supervisor JSON. After an unclean stop while crun still runs a workload, nyxd re-adopts from `supervisor/containers/*.json`, or from `bundles/<id>/nyxd-meta.json` if that JSON is missing (image must still resolve locally).

# Stop from another shell
sudo nyx stop web

# Exec (flags before id like docker; `--` optional)
sudo nyx exec <id> sh -c 'hostname'
sudo nyx exec <id> -- sh -c 'hostname'
```

## Remote daemon over SSH

`nyx` can drive a `nyxd` on another machine (a Pi in the closet, a NAS) over SSH, without
any TCP listener on the daemon:

```bash
nyx --host ssh://pi@raspberrypi.local ps
NYX_HOST=ssh://pi@nas:2222 nyx compose up -d
nyx -H ssh://pi@nas/run/user/1000/nyxd/nyxd.sock ps   # a rootless daemon's socket
```

- For each connection `nyx` runs the system `ssh` (`ssh -T host -- nyxd stdio-proxy`), so your keys,
  ssh-agent, `known_hosts` and `~/.ssh/config` apply. `nyx` stores no credentials. A
  `ControlMaster` / `ControlPersist` entry in `~/.ssh/config` saves the SSH handshake on every command.
- Without a socket path, the remote side uses the remote user's rootless daemon
  (`$XDG_RUNTIME_DIR/nyxd/nyxd.sock`) when it is running, else `/run/nyxd/nyxd.sock`. The remote user
  needs access to that socket (the `nyx` group, see [INSTALL.md](INSTALL.md)) and `nyxd` in `PATH`.
- `-H` / `--host` beats `NYX_HOST`, which beats `NYXD_SOCKET`. `--host` also takes a socket path or
  `unix:///path`.
- Paths refer to the remote host: `nyx run -v` sources, and for `nyx compose` the compose file is
  sent from your machine but its `.env` and relative bind mounts resolve on the remote host, in the
  directory of the same path. `nyx build` and `compose up --build` send the build context from your
  machine.

## Copying files (`nyx cp`)

Copy files or directories between the host and a running container, without a bind mount and
without a shell or `tar` in the image:

```bash
nyx cp web:/etc/nginx/nginx.conf ./nginx.conf     # out of the container
nyx cp ./fix.sh web:/usr/local/bin/fix.sh         # in (new name)
nyx cp ./site web:/usr/share/nginx/               # a directory, into an existing one
nyx cp web:/var/log/app - | tar -t                # a tar stream on stdout
tar -c conf | nyx cp - web:/etc/app/              # a tar stream from stdin
```

- When DEST is an existing directory the copy lands inside it; otherwise it is created (or replaced)
  under that name.
- Files copied into a container belong to root; `-a` / `--archive` keeps their uid and gid. Files copied
  out belong to you.
- Symlinks inside the container resolve as the container sees them (an absolute link points into the
  container's root, never the host's), and every write stays inside the container or the destination
  directory. Paths under a bind-mounted volume use the volume's files.
- The container must be running: an exited container's filesystem is gone. `/proc`, `/sys`, `/dev` and
  tmpfs mounts cannot be copied.
- API: `GET` / `PUT /v1/containers/{id}/archive?path=...` (tar).

## Events (`nyx events`)

Subscribe to what happens instead of polling `nyx ps`: a container dying, restarting, getting
OOM-killed or turning unhealthy, images being pulled, built or removed.

```bash
nyx events
nyx events --filter container=web --filter event=health_status
nyx events --json --since 10m | jq -c 'select(.action == "die")'
```

```
2026-11-17T22:55:10Z image pull alpine:3.20
2026-11-17T22:55:12Z container start web (image=alpine:3.20)
2026-11-17T22:55:23Z container die web (image=alpine:3.20, exit_code=137, signal=9, stopped=true)
```

- Container events: `create`, `start`, `die` (`exit_code`, `signal`, `stopped=true` when nyx stopped
  it, `error` when it failed to start), `oom`, `restart` (`attempt`), `health_status` (`status`), `remove`.
  Compose services carry `compose_project` and `compose_service`. Image events: `pull`, `build`, `remove`.
- `--filter` keys: `type` (container, image), `event`, `container`, `image`. Repeat a key for
  alternatives; different keys must all match.
- nyxd keeps the last 1000 events in memory (nothing on disk); `--since` replays them. A client that
  falls behind is disconnected with an `events_dropped` line instead of slowing nyxd down.
- `oom` needs per-container cgroups: rootless daemons do not report it.
- API: `GET /v1/events` (newline-delimited JSON; `since`, repeatable `filter`).

## Container logs

nyxd stores each container's stdout and stderr as JSON lines under
`/var/lib/nyxd/logs/<id>.log` and rotates the file by size, so a chatty
service cannot fill the disk:

| nyxd flag | Default | Meaning |
|-----------|---------|---------|
| `-log-max-size` | `10m` | Rotate when the log reaches this size (`k`, `m`, `g` suffixes); `0` never rotates |
| `-log-max-files` | `3` | Files kept per container, the current one included (`<id>.log`, `<id>.log.1`, `<id>.log.2`) |
| `-log-dir` | `<base-dir>/logs` | Where the log files live; a tmpfs keeps log writes off an SD card ([INSTALL.md](INSTALL.md#sd-cards-keep-logs-in-memory)) |

With the defaults a container keeps at most 30 MiB of logs. Set the flags in
`NYXD_OPTS` (see the service setup in the install guide), e.g.
`NYXD_OPTS="-log-max-size 50m -log-max-files 5"`.

`nyx logs --tail N` reads from the end of the files (rotated ones included) without
loading them whole, and `nyx logs -f` keeps following across rotations. Lines longer
than 64 KiB are split into several entries. A container's logs stay readable after it exits; `nyx rm <id>` deletes them
(and stops and removes it first if it is still running).

## Metrics (Prometheus)

nyxd serves metrics in the Prometheus text format at `GET /v1/metrics` on the control socket.
For scrapers that cannot read a Unix socket, add a TCP listener that serves `/metrics` and
nothing else (read-only, off by default):

```bash
NYXD_OPTS="-metrics-listen 127.0.0.1:9323"
curl -s http://127.0.0.1:9323/metrics | grep nyxd_container_memory
```

- Per container (labels `name`, `image`, and `compose_project` / `compose_service` for stacks):
  `nyxd_container_cpu_seconds_total`, `_memory_usage_bytes`, `_memory_limit_bytes`,
  `_network_receive_bytes_total`, `_network_transmit_bytes_total`, `_block_read_bytes_total`,
  `_block_write_bytes_total`, `_pids`, `_restarts_total`, `_oom_kills_total`, `_health_status{status}`.
- Daemon: `nyxd_build_info`, `nyxd_uptime_seconds`, `nyxd_containers{state}`, `nyxd_images`, and Go
  runtime basics (`go_goroutines`, `go_memstats_*`, `go_gc_cycles_total`).
- Rootless daemons have no per-container cgroups: CPU, memory and PIDs then cover each container's
  init process, and block I/O and OOM kills read 0.
- Example scrape config and a small Grafana dashboard:
  [example/metrics](https://github.com/zrougamed/nyxd/tree/main/example/metrics).
- Keep the listener on loopback or a trusted network: it shows container names and images.

## Disk use (`nyx system df`, `nyx system prune`)

```bash
$ nyx system df
TYPE          COUNT   ACTIVE   SIZE      RECLAIMABLE
Images        12      5        1.8GiB    940MiB
Build cache   31      -        620MiB    620MiB
Containers    7       5        48MiB     12MiB
Logs          7       5        210MiB    30MiB
Volumes       4       3        3.1GiB    0B

nyx system df -v                 # per image, container, log and volume
nyx system prune --dry-run       # what would go
nyx system prune [--images] [--volumes]
```

- Sizes are space on disk; layers shared between images count once. Images include the extracted
  layers containers run on. A container's size is its writable layer.
- `prune` removes only what nothing references: exited containers and their logs, logs and overlay
  directories left by containers that no longer exist, the build cache, and layers no image uses.
  `--images` also removes images no container uses; `--volumes` also named volumes no container mounts.
  Running containers and everything they use are never touched; an exited container's image and
  volumes count as in use until the container is removed.
- Build directories and unreferenced layers younger than an hour are kept: a build or pull may be using them.
- API: `GET /v1/system/df`, `POST /v1/system/prune` (`images`, `volumes`, `dry_run`).

## Registry mirrors

Pull through a mirror or pull-through cache, for Docker Hub rate limits or networks that only reach
an internal registry:

```bash
NYXD_OPTS="-registry-mirror docker.io=https://mirror.lan:5000 -registry-mirror ghcr.io=https://ghcr-cache.lan"
NYXD_OPTS="... -registry-mirror-only"   # air-gapped: never contact the registry itself
```

- Pulls of a registry's images try its mirrors first, in order, then the registry itself (unless
  `-registry-mirror-only`). A mirror URL may carry a path prefix for proxy projects
  (`https://harbor.lan/dockerhub`).
- Image names do not change: `alpine:3.20` stays `docker.io/library/alpine:3.20`.
- Every layer is checked against its digest and a pinned reference (`image@sha256:...`) against the
  manifest's, so a mirror cannot swap content; a mirror serving something else is skipped.
- A mirror that needs a login uses your `nyx login` (or docker/podman login) for the mirror's host,
  sent by `nyx` with the pull; the registry's own credentials never go to the mirror.
- `nyx info` lists the mirrors.

## Building images (`nyx build`)

```bash
nyx build -t myapp:1.0 .                                  # Dockerfile in the current directory
nyx build -t myapp:dev -f Dockerfile.dev --build-arg VERSION=1.2 --target runtime .
nyx build --no-cache -q -t myapp:1.0 .                    # print only the image id
nyx run myapp:1.0
```

No Docker or BuildKit involved; works rootless. Supported syntax, cache rules and security notes: [build.md](build.md).

## Sharing images (`nyx tag`, `push`, `save`, `load`)

```bash
nyx tag myapp:1.0 registry.example.com/team/myapp:1.0
nyx login registry.example.com                           # or reuse an existing docker / podman login
nyx push registry.example.com/team/myapp:1.0
nyx save -o myapp.tar myapp:1.0                          # OCI layout + docker manifest.json
nyx load -i myapp.tar                                    # also loads docker save / podman save archives
```

Details: [images.md](images.md).

## Compose stacks (`nyx compose`)

**Compose file:** if you omit `-f` / `--file`, **`nyx`** picks the first existing file in the **current directory**, in this order: `nyx-compose.yaml` / `.yml`, then `docker-compose.yaml` / `.yml`, then `compose.yaml` / `.yml`, then `podman-compose.yaml` / `.yml`. The file must be readable on the **daemon host** (paths are sent to `nyxd`).

**Project name:** container IDs are `{project}-{service}` (sanitized). If you omit **`--project`**, the default is the top-level **`name:`** of the compose file, else the **directory** that contains it (same idea as Docker Compose), not the filename — so two different folders that both use `docker-compose.yml` do not collide. Use the same **`--project`** for `up`, `stop`, and `down` when you override it.

**Updating a stack:** `nyx compose pull [SERVICE...]` pulls every service image in parallel (services with `build:` only with `--include-build`) and says per service whether it was up to date. `nyx compose up` on a running stack then recreates only the services whose image or configuration changed, in dependency order, and leaves the rest running:

```
$ nyx compose pull && nyx compose up
db (postgres:17): up to date
api (ghcr.io/me/api:latest): pulled, 2 new layer(s)
db: up to date
api: recreated (image changed)
```

`--force-recreate` recreates every running service, `--no-recreate` keeps running services as they are (changed ones are reported as `kept`).

**Depends on:** both `depends_on: [svc]` and the Compose **map** form `depends_on: { svc: { condition: ... } }` set the start order (dependencies first). `nyx compose up` then honours the condition before starting each service:

- `service_started` (also the list form): the dependency has been started.
- `service_healthy`: the dependency's `healthcheck` passes. The dependency must define a `healthcheck` in the compose file, otherwise `compose up` refuses the file instead of waiting forever.
- `service_completed_successfully`: the dependency has exited 0 (migrations, init jobs). A non-zero exit stops the rest of the stack from starting.

All waits of one `compose up` share a deadline, `--wait-timeout` (default `5m`). `restart: true` under `depends_on` is accepted with a warning and not acted on yet.

**Stop grace:** per-service `stop_grace_period` maps to SIGTERM wait before `nyx stop` / compose stop forces teardown. If omitted, the daemon uses a **10s** default (similar to `docker stop`), not a multi‑minute wait.

**Host network:** per-service `network_mode: host` (or a compose network with `driver: host`) shares the host network namespace, like `nyx run --network host`. Services reach each other on `127.0.0.1` (and by service name via `/etc/hosts`). Published `ports:` are not allowed with host networking — bind inside the process instead (e.g. `0.0.0.0:8080`).

**Resources:** `deploy.resources.limits` (`memory`, `cpus`, `memory_swap`, `pids_limit`, `cpuset_cpus`, …) and `reservations.memory` become cgroup
limits. A memory limit also caps memory plus swap at twice the limit unless `memory_swap` is set (equal to
`memory` means no extra swap). Rootless without cgroup delegation, limits are not applied (the daemon
logs a warning).

**Security / DNS / ulimits:** optional `apparmor_profile` (host-loaded profile; ignored when AppArmor is absent),
`seccomp_profile`, `ulimits` (e.g. `nofile`), and `dns:` (IPv4 nameservers). A volume at `/etc/resolv.conf`
is the only resolv mount for that service. Compose `networks` with both `bridge` and `subnet` use a dedicated
native bridge; services that omit `networks` stay on `nyxbr0`. What applies on Linux vs macOS/Windows machines:
[platform-matrix.md](platform-matrix.md).

**Docker socket / DinD:** nyxd's socket is not the Docker API. To feed legacy tools that need
`/var/run/docker.sock`, bind-mount the host Docker socket, or run a DinD image privileged — see
[docker-socket.md](docker-socket.md).

**Rootless:** published ports, service names and the limits of the shared host network: see [rootless.md](rootless.md#networking).

**Build:** services with `build:` (a context path, or `context`, `dockerfile`, `args`, `target`) are built by
`nyx compose up` before the stack starts, the same way as `nyx build` (`.dockerignore` honoured, output in your
terminal). Only images that are missing are built; `nyx compose up --build` rebuilds them all. Without `image:`, the
image is named `{project}-{service}:latest`. `nyxd` never pulls the image of a service that has `build:`; called
directly through the API, `compose up` fails with "not built yet" until it exists. Remote (git/URL) contexts are not
supported.

**Environment files:** `env_file:` (a path, a list, or `- path: x.env` / `required: false` entries, relative to the
compose file) is loaded before `environment:`, which wins. `.env` next to the compose file is only for `${VAR}`
substitution in the YAML, as in Docker Compose: write `$$VAR` for a variable the container itself should expand.

**Registry logins:** images that `compose up` pulls use your docker, podman or nyx login (see
[images.md](images.md#credentials-and-nyx-login)); a service's `registry_username` / `registry_password` still wins.

**Named volumes:** declare names under the top-level `volumes:` key. Data lives under **`{nyxd --base-dir}/volumes/<project>/<volume>/`**. Removing them is optional: **`nyx compose down -v`** deletes those host dirs only after containers are removed and nothing still references the path.

```bash
cd /path/to/stack   # contains e.g. docker-compose.yml or nyx-compose.yaml

sudo nyx compose up
sudo nyx compose up -f ./prod-compose.yaml --project myapp
sudo nyx compose up --build        # rebuild services with build: first

sudo nyx compose stop              # SIGTERM / stop path, reverse dependency order
sudo nyx compose start             # start the stopped services again (dependency order)
sudo nyx compose restart api       # restart one service (or all without names)
sudo nyx compose logs              # every service, prefixed with its name (last 200 lines each)
sudo nyx compose logs -f --tail 50 api worker   # -f follows here; the file is --file
sudo nyx compose ps                # service / status table (like docker compose ps)
sudo nyx compose down            # stop + remove containers
sudo nyx compose down -v         # also remove declared named volume dirs when safe
```

Security notes for operators: **[security.md](security.md)**. Compose `network_mode: host`,
`privileged: true`, and host control-socket binds print warnings on `compose up`.

See **[openapi.yaml](openapi.yaml)** (`ComposeProjectRequest`, `/v1/compose/up|stop|down`) and **[ROADMAP.md](ROADMAP.md)** (compose gaps: no `nyx volume ls`, implicit anonymous named volumes).

## Raw HTTP with curl

See **[openapi.yaml](openapi.yaml)** for schemas. Socket example:

```bash
SOCK=/run/nyxd/nyxd.sock

curl -sS --unix-socket "$SOCK" http://localhost/v1/ping
curl -sS --unix-socket "$SOCK" http://localhost/v1/version
curl -sS --unix-socket "$SOCK" http://localhost/v1/containers
curl -sS --unix-socket "$SOCK" 'http://localhost/v1/containers?detail=1'

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d '{"ref":"nginx:alpine","stream":false}' \
  http://localhost/v1/images/pull

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d '{"ref":"registry.example.com/myapp:1.0","stream":false,"username":"me","password":"secret"}' \
  http://localhost/v1/images/pull

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d '{"image":"nginx:alpine","publish":["8080:80"]}' \
  http://localhost/v1/containers/run

curl -sS --unix-socket "$SOCK" -X POST http://localhost/v1/containers/<id>/stop

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d '{"signal":"KILL"}' \
  http://localhost/v1/containers/<id>/kill

curl -sS --unix-socket "$SOCK" 'http://localhost/v1/containers/<id>/logs?tail=200&plain=1'

curl -sS --unix-socket "$SOCK" http://localhost/v1/images

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d '{"ref":"nginx:alpine"}' \
  http://localhost/v1/images/remove

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d '{}' \
  http://localhost/v1/images/prune

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d '{"dry_run":true}' \
  http://localhost/v1/images/prune

curl -sS --unix-socket "$SOCK" -X POST http://localhost/v1/containers/<id>/remove

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d '{"argv":["sh","-c","uname -a"]}' \
  http://localhost/v1/containers/<id>/exec

# Compose (file path must exist on the nyxd host)
curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d "{\"file\":\"/var/lib/stacks/demo/docker-compose.yml\",\"project\":\"demo\"}" \
  http://localhost/v1/compose/up

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d "{\"file\":\"/var/lib/stacks/demo/docker-compose.yml\",\"project\":\"demo\"}" \
  http://localhost/v1/compose/stop

curl -sS --unix-socket "$SOCK" -H 'Content-Type: application/json' \
  -d "{\"file\":\"/var/lib/stacks/demo/docker-compose.yml\",\"project\":\"demo\",\"remove_volumes\":true}" \
  http://localhost/v1/compose/down
```

## OpenAPI

- **Spec:** [openapi.yaml](openapi.yaml)  
- **Viewer:** paste the file into [Swagger Editor](https://editor.swagger.io/) or use `redocly preview-docs`.

## Related docs

- [INSTALL.md](INSTALL.md) — Linux install  
- [networking.md](networking.md) — networking flags and behavior  
- [kernel-requirements.md](kernel-requirements.md) — modules and sysctl  
