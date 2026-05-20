# Using nyxd and nyx

## nyx CLI (recommended)

Install **`nyx`** next to **`nyxd`** (`make build-nyx`). It talks to the daemon over the Unix socket (default `/run/nyxd/nyxd.sock`, override with `-socket` or `NYXD_SOCKET`). To use **`nyx` as a normal user** (no `sudo`), run **`nyxd --socket-group=…`** and add your user to that POSIX group — see **[Install](/docs/getting-started/install)** (`Use nyx without sudo`, `Install to /usr/bin`).

```bash
nyx ping
nyx version

# Pull (progress UI on stderr; add --json for a single JSON blob on stdout)
sudo nyx pull nginx:alpine

# Private registry (optional; same fields as POST /v1/images/pull JSON)
sudo nyx pull --username "$USER" --password "$TOKEN" registry.example.com/myapp:1.0

# Run — foreground prints container id, then streams logs; Ctrl+C sends SIGKILL (POST /v1/containers/{id}/kill)
sudo nyx run nginx:alpine

# Detach: prints container id only (use --json for the full JSON object on stdout)
sudo nyx run -d nginx:alpine
sudo nyx run -d --json nginx:alpine

# If the image is not in the local store, nyxd pulls it automatically before starting.

# Named container
sudo nyx run --name web nginx:alpine

# Docker-style flags: publish, env, hostname, restart
sudo nyx run -p 8080:80 -e MYVAR=1 --hostname web --restart no nginx:alpine

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

## Compose stacks (`nyx compose`)

**Compose file:** if you omit `-f` / `--file`, **`nyx`** picks the first existing file in the **current directory**, in this order: `nyx-compose.yaml` / `.yml`, then `docker-compose.yaml` / `.yml`, then `compose.yaml` / `.yml`, then `podman-compose.yaml` / `.yml`. The file must be readable on the **daemon host** (paths are sent to `nyxd`).

**Project name:** container IDs are `{project}-{service}` (sanitized). If you omit **`--project`**, the default is the **directory** that contains the compose file (same idea as Docker Compose), not the filename — so two different folders that both use `docker-compose.yml` do not collide. Use the same **`--project`** for `up`, `stop`, and `down` when you override it.

**Depends on:** both `depends_on: [svc]` and the Compose **map** form `depends_on: { svc: { condition: service_healthy } }` are accepted; only service **names** are used for start order (`condition` is ignored — readiness comes from **healthcheck** + sequential start).

**Stop grace:** per-service `stop_grace_period` maps to SIGTERM wait before `nyx stop` / compose stop forces teardown. If omitted, the daemon uses a **10s** default (similar to `docker stop`), not a multi‑minute wait.

**Named volumes:** declare names under the top-level `volumes:` key. Data lives under **`{nyxd --base-dir}/volumes/<project>/<volume>/`**. Removing them is optional: **`nyx compose down -v`** deletes those host dirs only after containers are removed and nothing still references the path.

```bash
cd /path/to/stack   # contains e.g. docker-compose.yml or nyx-compose.yaml

sudo nyx compose up
sudo nyx compose up -f ./prod-compose.yaml --project myapp

sudo nyx compose stop              # SIGTERM / stop path, reverse dependency order
sudo nyx compose down            # stop + remove containers
sudo nyx compose down -v         # also remove declared named volume dirs when safe
```

See **[OpenAPI spec](https://raw.githubusercontent.com/zrougamed/nyxd/main/docs/openapi.yaml)** (`ComposeProjectRequest`, `/v1/compose/up|stop|down`) and **[Roadmap](/docs/project/roadmap)** (compose gaps: no `nyx volume ls`, implicit anonymous named volumes).

## Raw HTTP with curl

See **[OpenAPI spec](https://raw.githubusercontent.com/zrougamed/nyxd/main/docs/openapi.yaml)** for schemas. Socket example:

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

- **Spec:** [OpenAPI YAML](https://raw.githubusercontent.com/zrougamed/nyxd/main/docs/openapi.yaml)  
- **Viewer:** paste the file into [Swagger Editor](https://editor.swagger.io/) or use `redocly preview-docs`.

## Related docs

- [Install](/docs/getting-started/install) — Linux install  
- [Networking](/docs/operations/networking) — networking flags and behavior  
- [Kernel requirements](/docs/operations/kernel-requirements) — modules and sysctl  
