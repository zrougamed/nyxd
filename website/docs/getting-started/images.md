# Sharing images: tag, push, save, load

Images you build or pull live in the local store. These commands move them between machines and registries.
Like everything else in nyxd they are implemented in-process (OCI distribution API and archive formats in Go): no
Docker, skopeo or registry SDK.

```bash
nyx build -t myapp:1.0 .
nyx tag myapp:1.0 registry.example.com/team/myapp:1.0
nyx push registry.example.com/team/myapp:1.0

nyx save -o myapp.tar myapp:1.0         # to a file (or stdout)
nyx load -i myapp.tar                   # from a file (or stdin)
nyx save myapp:1.0 | ssh edge-box nyx load
```

The same commands exist under `nyx image` (`nyx image push ...`).

## `nyx tag <source> <target>`

Gives a local image another reference. Nothing is copied: both names point at the same manifest and blobs.

## `nyx push <ref>`

Uploads a local image to the registry named in the reference.

```
$ nyx push localhost:5000/team/demo:1
[1/3] pushing 9c007d3030be (870 B)
[2/3] pushing 25f1d6b1951a (3.5 MB)
[3/3] pushing 6eec064da034 (293 B)
pushed localhost:5000/team/demo:1@sha256:d22265207bf9...
```

- Blobs the registry already has are skipped (`exists`), so re-pushing or pushing an image built on a common base
  only uploads what changed.
- Authentication follows the registry's `WWW-Authenticate` challenge (Bearer token or Basic), the standard
  distribution-spec flow. Tested: push and pull with `registry:2`; anonymous pulls from Docker Hub, GHCR and Quay.
- `localhost`, `127.0.0.1` and `[::1]` registries are reached over plain HTTP, like Docker. Every other registry uses
  HTTPS.

### Credentials

Checked in this order:

1. `--username USER --password PASS`
2. `NYX_REGISTRY_USERNAME` and `NYX_REGISTRY_PASSWORD`
3. The `auths` entries of `~/.docker/config.json` (or `$DOCKER_CONFIG/config.json`), so an existing `docker login`
   works. Credential helpers (`credsStore`, `credHelpers`) are **not** executed: nyx never runs extra binaries.

Credentials go from `nyx` to `nyxd` over the local control socket and are used only for that push.

`nyx pull` uses the same authentication. Before, it always asked Docker Hub's token service, so anonymous pulls from
other registries failed with "registry denied access"; pulls from GHCR, Quay and a local `registry:2` now work.

## `nyx save [-o FILE] <ref>...`

Writes one tar archive with:

- an **OCI image layout** (`oci-layout`, `index.json`, `blobs/sha256/...`), and
- a **docker-archive** `manifest.json`,

the same combined format `docker save` produces since Docker 25. The archive loads with `nyx load`, `docker load`
and `podman load`. Several references can go in one archive; shared layers are stored once. Without `-o` the
archive goes to stdout (refused when stdout is a terminal). With `-o` the file appears only once complete.

## `nyx load [-i FILE]`

Imports archives made by `nyx save`, `docker save` (any version, including the older `<id>/layer.tar` layout) and
`podman save`. Gzip-compressed archives are accepted. Image names come from the archive
(`io.containerd.image.name` / `RepoTags`); unnamed images are stored as `nyx-load:<id>`.

An archive is untrusted input:

- it is unpacked with the same rules as image layers (no path escapes, no device nodes),
- files are read through `os.Root`, so a symlink in the archive cannot make nyxd read a host file,
- **every blob is checked against its sha256 digest**; a modified archive is rejected with `digest mismatch`.

## API

| Endpoint | Body | Response |
|----------|------|----------|
| `POST /v1/images/tag` | `{"source": "...", "target": "..."}` | `{"ok": true, ...}` |
| `POST /v1/images/push` | `{"ref": "...", "username": "...", "password": "..."}` | NDJSON progress, ends with `{"phase":"done","digest":...}` or `{"phase":"error",...}` |
| `GET /v1/images/save?ref=a&ref=b` | – | `application/x-tar` archive |
| `POST /v1/images/load` | archive (tar, optionally gzip) | `{"ok": true, "refs": [...]}` |

See [openapi.yaml](/docs/reference/api).

## Testing

```bash
make test-transfer   # rootless nyxd runs registry:2, then build, tag, push x2, pull, run, save, load
```

CI runs the same script. Unit tests cover push/pull against an in-process registry with token auth, rejected
credentials, save/load round trips, docker-archive imports, tampered blobs and symlink escapes.
