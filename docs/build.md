# Building images

`nyx build` builds OCI images from a Dockerfile. There is no Docker, no BuildKit and no extra binary: the Dockerfile
parser, the layer engine and the cache are part of `nyxd`, and `RUN` steps execute with the same `crun` that runs
your containers. It works **rootless** (see [rootless.md](rootless.md)) and rootful.

```bash
nyx build -t myapp:1.0 .
nyx build -t myapp:dev -f docker/Dockerfile.dev --build-arg VERSION=1.2 --target runtime .
nyx run myapp:1.0
```

| Flag | Meaning |
|------|---------|
| `-t, --tag REF` | Tag the result (repeatable). Without a tag the image is stored as `nyx-build:<id>`. |
| `-f, --file PATH` | Dockerfile (default `<context>/Dockerfile`; may live outside the context). |
| `--build-arg K=V` | Set a build argument (`--build-arg K` takes the value from your environment). |
| `--target STAGE` | Stop after the stage named `STAGE`. |
| `--no-cache` | Ignore the build cache. |
| `-q, --quiet` | Print only the image ID. |

The built image is stored in the local image store, so `nyx run`, `nyx compose` and `nyx image ls` use it right away.

## How it works

1. `nyx` sends the context directory as a tar stream, skipping what `.dockerignore` excludes. The Dockerfile and
   `.dockerignore` are always sent.
2. `nyxd` unpacks the context into a temporary directory (same safety rules as image layers: no path escapes, no
   device nodes) and parses the Dockerfile.
3. For each filesystem step it mounts overlayfs over the current layers:
   - **RUN** runs the command through `crun` in the container sandbox: seccomp profile, reduced capabilities,
     `noNewPrivileges`, the daemon's user namespace when rootless, and the **host network** (so package managers
     can download).
   - **COPY / ADD** write into the merged tree from Go.
4. The overlay upper directory becomes a new gzip layer (`internal/layer`), deletions become OCI whiteouts.
5. The image config and an OCI manifest are written to the store under every `-t` tag.

Output looks like this:

```
STEP 3/18: RUN apk add --no-cache gcc musl-dev
fetch https://dl-cdn.alpinelinux.org/alpine/v3.20/main/x86_64/APKINDEX.tar.gz
...
STEP 4/18: WORKDIR /src
STEP 5/18: COPY src/ ./
--> Using cache
...
COMMIT myapp:1.0
--> 631b20742467
Successfully built 631b20742467
Successfully tagged myapp:1.0
```

## Build cache

Every step has a key derived from the previous step's key and the step itself:

| Step | Key includes |
|------|--------------|
| `FROM` | the base image config digest (or the stage it builds on) |
| `RUN` | command, full environment (ENV + ARG values), `USER`, `WORKDIR` |
| `COPY` / `ADD` | file paths, modes, sizes and **contents** (and ownership for `--from`), destination, `--chown`, `--chmod` |
| `ARG`, `ENV`, `LABEL`, ... | the values set |

When a key was built before and its layer is still in the store, the step prints `--> Using cache` and is skipped.
Changing a file under `COPY src/ ./` rebuilds that step and everything after it; steps before it stay cached.
Cache records live in `{baseDir}/build/cache/`; layers are ordinary blobs in the image store.

## Supported Dockerfile syntax

| Instruction | Support |
|-------------|---------|
| `FROM image [AS name]` | ✅ registry images (pulled on demand), earlier stages by name or index, `scratch`. `--platform` only for the host architecture. |
| `RUN` | ✅ shell and exec form, `--network=default\|host`. ❌ `--mount` (cache, secret, bind), heredocs |
| `COPY` | ✅ files, directories (contents), wildcards, `--from=stage\|image`, `--chown=user:group` (names resolved in the image), `--chmod=0755`, `--link` (accepted). ❌ `--parents`, `--exclude` |
| `ADD` | ✅ everything `COPY` does (except `--from`), local tar archives (gzip, bzip2, plain) extracted, `http(s)://` URLs with optional `--checksum=sha256:...`. ❌ git URLs, xz archives |
| `ARG` | ✅ before `FROM` (usable in `FROM` lines) and per stage, defaults, `--build-arg` |
| `ENV`, `LABEL` | ✅ `k=v` pairs and the legacy `ENV k v` form |
| `WORKDIR`, `USER`, `EXPOSE`, `VOLUME`, `STOPSIGNAL`, `SHELL` | ✅ |
| `CMD`, `ENTRYPOINT` | ✅ exec and shell form; a new `ENTRYPOINT` resets an inherited `CMD` |
| `HEALTHCHECK` | ✅ `CMD` / `NONE`, `--interval`, `--timeout`, `--start-period`, `--retries` (stored in the image config) |
| `ONBUILD` | stored in the image; triggers from a base image are **not** executed (a warning is printed) |
| `MAINTAINER` | ✅ converted to a `maintainer` label |
| Parser directives | ✅ `# escape=`; `# syntax=` is ignored |
| Variables | ✅ `$VAR`, `${VAR}`, `${VAR:-default}`, `${VAR-default}`, `${VAR:+alt}`, `${VAR+alt}`, quotes and escapes |

Unsupported features fail the build with a clear message (for example `RUN: --mount is not supported yet`), never
silently.

## Security

- **Untrusted Dockerfiles run in the container sandbox.** `RUN` gets the same seccomp profile, capability set and
  `noNewPrivileges` as `nyx run`. Rootless, the whole build runs in the daemon's user namespace with no host
  capabilities.
- **Host network during RUN.** Build steps can reach the network and local services, like `docker build
  --network=host`. Do not build Dockerfiles you would not run.
- **No escapes through the context or the image.** Source paths resolve inside the context (symlinks pointing outside
  it are not followed out). Destinations resolve inside the image root: an image symlink such as `/var/run -> /run`
  is followed inside the image, never to the host.
- **Only what you send is visible.** The daemon sees the tar stream from `nyx`, not your filesystem; `.dockerignore`
  is applied before anything leaves your machine.

## Image USER at run time

`nyx run` now honours the image `USER` (name or `uid[:gid]`, resolved against the image's `/etc/passwd` and
`/etc/group`), like Docker. An explicit user (API / compose `user:`) still wins. If the user does not exist in the
image, the container fails to start with `unable to find user ...`.

## Limits and roadmap

- `RUN --mount=type=cache|secret|bind` and heredocs are planned next.
- Builds are linear per stage (no parallel stage execution yet).
- Image IDs change on every rebuild (history timestamps); reproducible IDs (`SOURCE_DATE_EPOCH`) are planned.
- To move built images, use `nyx tag` / `nyx push` / `nyx save` / `nyx load` (see the images page).

## Testing

```bash
make test-build    # builds, starts a rootless nyxd, runs a multi-stage build, checks output and cache
```

CI runs the same script.
