# Using nyxd and nyx

## nyx CLI (recommended)

Install **`nyx`** next to **`nyxd`** (`make build-nyx`). It talks to the daemon over the Unix socket (default `/run/nyxd/nyxd.sock`, override with `-socket` or `NYXD_SOCKET`). To use **`nyx` as a normal user** (no `sudo`), run **`nyxd --socket-group=…`** and add your user to that POSIX group — see **[INSTALL.md](INSTALL.md)** (`Use nyx without sudo`, `Install to /usr/bin`).

```bash
nyx ping
nyx version

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

# Logs (optional follow; tail count)
sudo nyx logs web
sudo nyx logs -f --tail 100 web
sudo nyx container logs web -f

# List / remove (docker-like)
sudo nyx ps
sudo nyx ps -q
sudo nyx stop web other-id
sudo nyx rm web

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

## Container logs

nyxd stores each container's stdout and stderr as JSON lines under
`/var/lib/nyxd/logs/<id>.log` and rotates the file by size, so a chatty
service cannot fill the disk:

| nyxd flag | Default | Meaning |
|-----------|---------|---------|
| `-log-max-size` | `10m` | Rotate when the log reaches this size (`k`, `m`, `g` suffixes); `0` never rotates |
| `-log-max-files` | `3` | Files kept per container, the current one included (`<id>.log`, `<id>.log.1`, `<id>.log.2`) |

With the defaults a container keeps at most 30 MiB of logs. Set the flags in
`NYXD_OPTS` (see the service setup in the install guide), e.g.
`NYXD_OPTS="-log-max-size 50m -log-max-files 5"`.

`nyx logs --tail N` reads from the end of the files (rotated ones included) without
loading them whole, and `nyx logs -f` keeps following across rotations. Lines longer
than 64 KiB are split into several entries. A container's logs stay readable after it exits; `nyx rm <id>` deletes them
(and stops and removes it first if it is still running).

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

**Depends on:** both `depends_on: [svc]` and the Compose **map** form `depends_on: { svc: { condition: service_healthy } }` are accepted; only service **names** are used for start order (`condition` is ignored — readiness comes from **healthcheck** + sequential start).

**Stop grace:** per-service `stop_grace_period` maps to SIGTERM wait before `nyx stop` / compose stop forces teardown. If omitted, the daemon uses a **10s** default (similar to `docker stop`), not a multi‑minute wait.

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
sudo nyx compose down            # stop + remove containers
sudo nyx compose down -v         # also remove declared named volume dirs when safe
```

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
