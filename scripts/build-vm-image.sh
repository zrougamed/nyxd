#!/bin/sh
# Build the nyx VM image that `nyx machine` boots on macOS (vfkit, Apple
# Virtualization.framework): a small Alpine Linux with OpenRC, a static nyxd
# and crun, and the glue a VM needs:
#
#   - the root disk (/dev/vda) holds the system only; nyxd's data (images,
#     containers, volumes) lives on a second disk (/dev/vdb), formatted on
#     first boot, so an upgrade replaces the system and keeps the data
#   - /Users from the Mac is shared over virtio-fs at the same path, so bind
#     mounts and build contexts under /Users work unchanged
#   - the nyxd socket is bridged to vsock port 1024, and port 1025 shuts the
#     VM down (arm64 has no power-button driver); vfkit turns them into
#     Unix sockets on the Mac, which nyx uses
#   - eth0 gets an address from the Mac's NAT (DHCP); nyx forwards published
#     ports from localhost to it
#
#   scripts/build-vm-image.sh amd64|arm64 OUTDIR [VERSION]
#
# Produces OUTDIR/nyx-vm-<arch>.tar.gz and .sha256 with vmlinuz (an
# uncompressed Image on arm64, as Virtualization.framework requires),
# initramfs, rootfs.img (sparse ext4) and cmdline. Needs docker (or podman as
# DOCKER=podman) and, to build for the other architecture, binfmt emulation
# (docker run --privileged tonistiigi/binfmt --install all).
set -eu
umask 022

ARCH=${1:?arch (amd64|arm64)}
OUT=${2:?output directory}
VERSION=${3:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
DOCKER=${DOCKER:-docker}

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
mkdir -p "$OUT" "$WORK/in" "$WORK/out"
OUT=$(cd "$OUT" && pwd)

# nyxd, static (NYXD_BIN: use a prebuilt one).
if [ -n "${NYXD_BIN:-}" ]; then
	install -m 0755 "$NYXD_BIN" "$WORK/in/nyxd"
else
	(cd "$ROOT_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath \
		-ldflags "-w -s -X main.version=$VERSION" -o "$WORK/in/nyxd" ./cmd/nyxd)
fi

# crun, static, pinned (same file as the WSL distro).
if [ -n "${CRUN_CACHE:-}" ] && [ -f "$CRUN_CACHE/crun-$ARCH" ]; then
	cp "$CRUN_CACHE/crun-$ARCH" "$WORK/in/crun"
else
	curl -sSfL -o "$WORK/in/crun" \
		"https://github.com/containers/crun/releases/download/$CRUN_VERSION/crun-$CRUN_VERSION-linux-$ARCH-disable-systemd"
fi
echo "$CRUN_SHA256  $WORK/in/crun" | sha256sum -c - >/dev/null || { echo "crun sha256 mismatch" >&2; exit 1; }

cp "$ROOT_DIR/packaging/linux/nyxd.openrc" "$WORK/in/nyxd.openrc"
cp -r "$ROOT_DIR/packaging/vm" "$WORK/in/vm"
printf '%s\n' "$VERSION" >"$WORK/in/version"

# Everything below runs in an Alpine container of the target architecture,
# so apk and mkinitfs (run by the kernel package) are native.
# shellcheck disable=SC2016 # the script runs in the container, with its own variables
$DOCKER run --rm --platform "linux/$ARCH" -v "$WORK:/work" "docker.io/library/alpine:$ALPINE_VERSION" sh -euc '
	R=/work/rootfs
	trap "rm -rf $R" EXIT # root-owned: the host user could not delete it
	apk add -q e2fsprogs >/dev/null
	mkdir -p "$R/etc/apk" "$R/etc/mkinitfs"
	cp -r /etc/apk/keys "$R/etc/apk/"
	cp /etc/apk/repositories "$R/etc/apk/"
	# The initramfs only needs to find the virtio root disk.
	echo "features=\"base virtio ext4\"" >"$R/etc/mkinitfs/mkinitfs.conf"
	apk add -q --root "$R" --initdb --no-cache --arch "$(apk --print-arch)" \
		alpine-base linux-virt e2fsprogs e2fsprogs-extra socat nftables ca-certificates-bundle busybox-openrc >/dev/null

	install -m 0755 /work/in/nyxd "$R/usr/bin/nyxd"
	install -m 0755 /work/in/crun "$R/usr/bin/crun"
	install -m 0755 /work/in/nyxd.openrc "$R/etc/init.d/nyxd"
	install -m 0755 /work/in/vm/nyx-datadisk "$R/etc/init.d/nyx-datadisk"
	install -m 0755 /work/in/vm/nyx-vsock "$R/etc/init.d/nyx-vsock"
	install -m 0755 /work/in/vm/nyx-power "$R/etc/init.d/nyx-power"
	install -m 0644 /work/in/vm/fstab "$R/etc/fstab"
	install -m 0644 /work/in/vm/interfaces "$R/etc/network/interfaces"
	install -m 0644 /work/in/vm/modules "$R/etc/modules"
	install -m 0644 /work/in/vm/inittab "$R/etc/inittab"
	install -D -m 0644 /work/in/vm/nyxd.default "$R/etc/default/nyxd"
	install -m 0644 /work/in/vm/nyxd.confd "$R/etc/conf.d/nyxd"
	install -D -m 0755 /work/in/vm/acpi-power "$R/etc/acpi/PWRF/00000080"
	echo nyx >"$R/etc/hostname"
	printf "127.0.0.1\tlocalhost nyx\n::1\tlocalhost ip6-localhost\n" >"$R/etc/hosts"
	mkdir -p "$R/Users" "$R/var/lib/nyxd"
	echo "nyx:x:1000:" >>"$R/etc/group"
	sed "s/^VERSION_ID=.*/VERSION_ID=$(cat /work/in/version)/" "$R/etc/os-release" >"$R/etc/os-release.new"
	mv "$R/etc/os-release.new" "$R/etc/os-release"
	echo "nyx VM $(cat /work/in/version)" >"$R/etc/motd"

	# nyxd needs a pure cgroup v2 hierarchy; OpenRC mounts a hybrid one by default.
	sed -i "s/^#\{0,1\}rc_cgroup_mode=.*/rc_cgroup_mode=\"unified\"/" "$R/etc/rc.conf"
	grep -q "^rc_cgroup_mode=\"unified\"" "$R/etc/rc.conf"

	add() { chroot "$R" rc-update add "$1" "$2" >/dev/null; }
	for s in devfs dmesg mdev hwdrivers cgroups; do add "$s" sysinit; done
	for s in modules sysctl hostname bootmisc localmount networking seedrng; do add "$s" boot; done
	for s in acpid nyx-datadisk nyxd nyx-vsock nyx-power; do add "$s" default; done
	for s in killprocs mount-ro savecache; do add "$s" shutdown; done

	# Kernel and initramfs go next to the disk, for the Linux boot loader.
	cp "$R"/boot/initramfs-virt /work/out/initramfs
	k="$R/boot/vmlinuz-virt"
	if [ "$(head -c 2 "$k")" = "MZ" ] && [ "$(dd if="$k" bs=1 skip=4 count=4 2>/dev/null)" = "zimg" ]; then
		# EFI zboot (arm64): the header says where the gzip payload is.
		off=$(od -An -t u4 -j 8 -N 4 "$k" | tr -d " ")
		size=$(od -An -t u4 -j 12 -N 4 "$k" | tr -d " ")
		tail -c +"$((off + 1))" "$k" | head -c "$size" | gunzip >/work/out/vmlinuz
	else
		cp "$k" /work/out/vmlinuz
	fi
	rm -rf "$R/boot"/vmlinuz-* "$R/boot"/initramfs-*

	mke2fs -q -t ext4 -L nyxroot -d "$R" /work/out/rootfs.img 2G
	# No commas: vfkit splits its --bootloader options on them.
	echo "console=hvc0 root=/dev/vda rootfstype=ext4 rw quiet" >/work/out/cmdline
	chown -R "$(stat -c %u /work/in):$(stat -c %g /work/in)" /work/out
'

TAR="$OUT/nyx-vm-$ARCH.tar.gz"
# Prefer a GNU sparse member for rootfs.img (small archive). Older nyx clients
# only accepted TypeReg; current nyx also accepts TypeGNUSparse ('S').
tar --sparse -C "$WORK/out" -cf - vmlinuz initramfs rootfs.img cmdline | gzip -n -9 >"$TAR"
(cd "$OUT" && sha256sum "nyx-vm-$ARCH.tar.gz" >"nyx-vm-$ARCH.tar.gz.sha256")
echo "built $TAR ($(du -h "$TAR" | cut -f1))"
