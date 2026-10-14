#!/bin/sh
# Build the nyx WSL2 distro tarball: a static nyxd, a static crun and the
# Mozilla CA bundle; no shell, no package manager.
#
#   scripts/build-wsl-rootfs.sh amd64|arm64 OUTDIR [VERSION]
#
# Produces OUTDIR/nyx-wsl-<arch>.tar.gz and .sha256. crun is pinned by version
# and sha256; the pinned files were checked against the crun release GPG
# signatures (Giuseppe Scrivano, key AC40 4C1C 0BF7 35C6 3FF4 D562 263D 6DF2 E163 E1EA).
set -eu
umask 022

ARCH=${1:?arch (amd64|arm64)}
OUT=${2:?output directory}
VERSION=${3:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}

# shellcheck source=scripts/pins.sh
. "$(dirname "$0")/pins.sh"
case "$ARCH" in
amd64) CRUN_SHA256=$CRUN_SHA256_amd64 ;;
arm64) CRUN_SHA256=$CRUN_SHA256_arm64 ;;
*) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
R="$WORK/rootfs"
mkdir -p "$OUT"

# Directory layout (WSL mounts /proc, /sys, /dev, /mnt/* and writes resolv.conf/hosts).
for d in usr/local/bin usr/sbin etc/ssl/certs root tmp run var/lib/nyxd proc sys dev mnt; do
	mkdir -p "$R/$d"
done
chmod 1777 "$R/tmp"
chmod 0700 "$R/root"

# nyxd, static (NYXD_BIN: use a prebuilt one, e.g. a -cover build for scripts/coverage.sh).
if [ -n "${NYXD_BIN:-}" ]; then
	install -m 0755 "$NYXD_BIN" "$R/usr/local/bin/nyxd"
else
	(cd "$ROOT_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath \
		-ldflags "-w -s -X main.version=$VERSION" -o "$R/usr/local/bin/nyxd" ./cmd/nyxd)
fi

# crun, static, pinned.
CRUN_URL="https://github.com/containers/crun/releases/download/$CRUN_VERSION/crun-$CRUN_VERSION-linux-$ARCH-disable-systemd"
if [ -n "${CRUN_CACHE:-}" ] && [ -f "$CRUN_CACHE/crun-$ARCH" ]; then
	cp "$CRUN_CACHE/crun-$ARCH" "$WORK/crun"
else
	curl -sSfL -o "$WORK/crun" "$CRUN_URL"
fi
echo "$CRUN_SHA256  $WORK/crun" | sha256sum -c - >/dev/null || { echo "crun sha256 mismatch" >&2; exit 1; }
install -m 0755 "$WORK/crun" "$R/usr/local/bin/crun"

# CA bundle, pinned. Go reads /etc/ssl/certs/ca-certificates.crt.
if [ -n "${CRUN_CACHE:-}" ] && [ -f "$CRUN_CACHE/cacert-$CACERT_DATE.pem" ]; then
	cp "$CRUN_CACHE/cacert-$CACERT_DATE.pem" "$WORK/cacert.pem"
else
	curl -sSfL -o "$WORK/cacert.pem" "https://curl.se/ca/cacert-$CACERT_DATE.pem"
fi
echo "$CACERT_SHA256  $WORK/cacert.pem" | sha256sum -c - >/dev/null || { echo "CA bundle sha256 mismatch" >&2; exit 1; }
install -m 0644 "$WORK/cacert.pem" "$R/etc/ssl/certs/ca-certificates.crt"

# Accounts: root only. No shell exists, by design (use `wsl -d nyx --exec ...`).
# The login shell is nyxd under the name nologin: it prints a hint and exits,
# so a plain `wsl -d nyx` cannot start the daemon without -userns-remap.
ln -s ../local/bin/nyxd "$R/usr/sbin/nologin"
printf 'root:x:0:0:root:/root:/usr/sbin/nologin\n' >"$R/etc/passwd"
printf 'root:x:0:\n' >"$R/etc/group"
install -m 0644 "$ROOT_DIR/packaging/wsl/wsl.conf" "$R/etc/wsl.conf"
cat >"$R/etc/os-release" <<OSR
NAME="nyx"
ID=nyx
PRETTY_NAME="nyx $VERSION (nyxd + crun, no shell)"
VERSION_ID=$VERSION
HOME_URL="https://github.com/zrougamed/nyxd"
OSR

# Reproducible tarball: sorted, numeric root ownership, fixed mtime.
EPOCH=${SOURCE_DATE_EPOCH:-$(cd "$ROOT_DIR" && git log -1 --format=%ct 2>/dev/null || echo 0)}
TAR="$OUT/nyx-wsl-$ARCH.tar.gz"
tar --sort=name --numeric-owner --owner=0 --group=0 --mtime="@$EPOCH" \
	-C "$R" -cf - . | gzip -n -9 >"$TAR"
(cd "$OUT" && sha256sum "nyx-wsl-$ARCH.tar.gz" >"nyx-wsl-$ARCH.tar.gz.sha256")
echo "built $TAR ($(du -h "$TAR" | cut -f1))"
