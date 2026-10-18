# Using Docker sockets with nyxd

nyxd speaks its own control API on a Unix socket (default `/run/nyxd/nyxd.sock`).
That socket is **not** the Docker Engine API. Tools that hard-code
`/var/run/docker.sock` (legacy CI jobs, some IDEs, Docker-in-Docker images)
will not talk to nyxd through that path.

## Legacy apps that need a Docker API

If the **host** still runs Docker (or Podman with a Docker-compatible socket)
and the container only needs to drive that engine:

```yaml
services:
  legacy:
    image: my-ci:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
```

nyxd bind-mounts the host socket into the container like any other volume.
The process inside talks to the **host Docker daemon**, not to nyxd.
`:ro` on the bind does **not** reduce Docker API power (the engine still
accepts privileged create/exec); treat socket access as full host-engine
privilege either way.

Rootless nyxd cannot usefully mount a root-owned Docker socket unless that
socket is reachable from the user namespace (unusual). Prefer rootful nyxd for
this hybrid path, or run the legacy tool on the host.

## Docker-in-Docker (dockerd inside a container)

True DinD needs a Docker daemon **inside** the container (privileged or with
enough capabilities, nested storage, and usually `seccomp_profile: unconfined`).
nyxd can start such a workload when the image provides `dockerd`, for example:

```yaml
services:
  dind:
    image: docker:dind
    privileged: true
    seccomp_profile: unconfined
    environment:
      DOCKER_TLS_CERTDIR: ""
```

That is still Docker-in-Docker, not “nyxd pretending to be Docker”. There is
no Docker API shim in nyxd today; use `nyx` / `GET/POST /v1/...` for nyxd-native
automation, or mount a real Docker socket as above for Docker API clients.

## Prefer nyxd-native where possible

| Need | Prefer |
|------|--------|
| Pull/run/compose under nyxd | `nyx` CLI or `/run/nyxd/nyxd.sock` |
| Third-party tool that only speaks Docker API | Host Docker socket mount (hybrid) or run the tool against Docker |
| BuildKit / rootlesskit userns on Ubuntu | Compose `apparmor_profile` + `seccomp_profile: unconfined` (see [Networking](/docs/operations/networking)) |
