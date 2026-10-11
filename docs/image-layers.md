# Image layers

nyxd unpacks and creates OCI image layers **in-process** (`internal/layer`). It does not call the host `tar`
binary or any external tool, so layer handling adds no extra binaries (or CVEs) to the host.

## Where layers live

| Path | Content |
|------|---------|
| `{baseDir}/images/blobs/sha256/<hex>` | Compressed layer blobs as pulled from the registry |
| `{baseDir}/overlay/_layers/<hex>/` | Extracted layer, shared by every container using it (overlayfs `lowerdir`) |
| `{baseDir}/overlay/<container-id>/{diff,work,merged}` | Per-container writable layer and merged rootfs |

A layer directory only appears under `_layers/` once its extraction has fully completed: layers are extracted
into a temporary directory and atomically renamed into place. A crash mid-extraction never leaves a half-written
layer that later containers would reuse.

## How a layer is applied

Each layer is extracted into its **own** directory and stacked by overlayfs. OCI whiteouts become overlayfs markers,
so deletions in an upper layer correctly hide files from lower layers:

| In the layer tar | On disk (overlay lowerdir) |
|------------------|---------------------------|
| `dir/.wh.name` | `dir/name` as a whiteout (character device `0/0`) |
| `dir/.wh..wh..opq` | `trusted.overlay.opaque=y` on `dir` (opaque directory) |
| `.wh..wh..plnk` and other `.wh..wh.` metadata | ignored |

`internal/layer` also has a **flatten** mode that applies whiteouts as real deletions onto an existing tree. It is
used where overlayfs is not available or not wanted (image builds, non-overlay storage).

## Security rules

- **No escapes.** Every write goes through Go's `os.Root`. Entry names are cleaned against `/` (`../../x` lands at
  `<layer>/x`), symlinks are followed only while they stay inside the layer, and hardlinks must point inside it.
  A layer that tries to write through a symlink to `/etc` fails to extract.
- **No device nodes.** Block and character devices in image layers are skipped. FIFOs are allowed.
- **No forged overlay metadata.** Extended attributes in `trusted.*` and `user.overlay.*` and `security.selinux`
  are never taken from an image, so an image cannot fake whiteouts or opaque directories. Other xattrs (for
  example `security.capability` file capabilities) are preserved.
- **Ownership and modes are preserved** when nyxd runs as root, including setuid/setgid bits, the same as Docker.
  Containers run with `noNewPrivileges`, so setuid binaries cannot gain privileges inside a container.
- **Compression:** gzip and uncompressed layers. zstd layers are rejected with a clear error.

## Creating layers

`layer.Diff` turns an overlayfs upper directory back into an OCI layer tar. Whiteouts become `.wh.` entries,
opaque directories get a `.wh..wh..opq` marker, and hardlinks are kept. Output is deterministic (sorted entries,
no atime/ctime, optional mtime clamp like `SOURCE_DATE_EPOCH`). `layer.WriteBlob` gzips the result and returns
the blob digest, diffID and size. This is the base for `nyx build` and image commits.

## Upgrading from older versions

Before this change, layers were extracted with the host `tar` into `{baseDir}/overlay/_cache/`. That path did not
apply whiteouts across layers (files deleted in an upper layer were still visible) and dropped file ownership.
On startup nyxd deletes `_cache/` unless a running container still has it mounted, and new containers use `_layers/`.

## Testing

```bash
make test           # unit tests (traversal, symlink escape, whiteouts, opaque dirs, hardlinks, Diff round trip)
make test-overlay   # mounts real overlayfs inside an unprivileged user + mount namespace
```

`make test-overlay` needs unprivileged user namespaces (`unshare -Urm`). On Ubuntu 24.04+ you may need
`sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`.
