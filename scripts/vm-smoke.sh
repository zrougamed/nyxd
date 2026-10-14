#!/bin/sh
# Boot test for the nyx VM image (scripts/build-vm-image.sh) under QEMU on
# Linux (KVM for amd64; arm64 runs emulated, so it is slower), with the devices vfkit gives it on macOS: virtio disks (system +
# data), virtio-net, a virtio console and nyxd's socket over vsock.
#
#   scripts/vm-smoke.sh dist-extra/nyx-vm-amd64.tar.gz
#   scripts/vm-smoke.sh dist-extra/nyx-vm-arm64.tar.gz
#
# Checks: nyxd answers over vsock, a container runs and its published port
# answers on the VM's address, the data disk is formatted, survives a
# shutdown and a second boot, and its filesystem grows with the disk. The
# first shutdown is the vsock request nyx machine stop sends; the second is
# the power button on amd64 (arm64 has no driver for it, so vsock again). Needs qemu-system-x86_64 and KVM
# (or qemu-system-aarch64), /dev/vhost-vsock, socat with vsock support, and a built bin/nyx.
set -eu
IMG=${1:?nyx-vm-<arch>.tar.gz}
case "$IMG" in
*arm64*) QEMU="qemu-system-aarch64 -M virt -cpu max" SLOW=6 ;;
*) QEMU="qemu-system-x86_64 -enable-kvm -cpu host" SLOW=1 ;;
esac
NYX=${NYX:-$(cd "$(dirname "$0")/.." && pwd)/bin/nyx}
CID=${CID:-$((1000 + $$ % 50000))}
V=$(mktemp -d)
QPID=""
SPID=""
cleanup() {
	[ -n "$SPID" ] && kill "$SPID" 2>/dev/null
	[ -n "$QPID" ] && kill "$QPID" 2>/dev/null
	rm -rf "$V"
}
trap cleanup EXIT INT TERM

tar -xzf "$IMG" -C "$V"
truncate -s 8G "$V/data.img"
# The VM's port 18080 is reachable on the host as 127.0.0.1:28080 (QEMU user net).
HOSTFWD=${HOSTFWD:-28080}

boot() {
	: >"$V/console.log"
	# shellcheck disable=SC2086 # QEMU is a command with its arguments
	$QEMU -m 1024 -smp 2 -display none -no-reboot \
		-kernel "$V/vmlinuz" -initrd "$V/initramfs" -append "$(cat "$V/cmdline")" \
		-drive "file=$V/rootfs.img,if=virtio,format=raw" -drive "file=$V/data.img,if=virtio,format=raw" \
		-netdev "user,id=n,hostfwd=tcp:127.0.0.1:$HOSTFWD-:18080" -device virtio-net-pci,netdev=n \
		-device "vhost-vsock-pci,guest-cid=$CID" -device virtio-serial-pci \
		-chardev "file,id=c,path=$V/console.log" -device virtconsole,chardev=c -device virtio-rng-pci \
		-monitor "unix:$V/monitor.sock,server,nowait" >"$V/qemu.out" 2>&1 &
	QPID=$!
	command rm -f "$V/nyxd.sock"
	socat "UNIX-LISTEN:$V/nyxd.sock,fork" "VSOCK-CONNECT:$CID:1024" 2>/dev/null &
	SPID=$!
	i=0
	until "$NYX" -socket "$V/nyxd.sock" ping >/dev/null 2>&1; do
		i=$((i + 1))
		if [ "$i" -gt $((90 * SLOW)) ] || ! kill -0 "$QPID" 2>/dev/null; then
			echo "FAIL: nyxd did not answer over vsock" >&2
			tail -30 "$V/console.log" >&2
			cat "$V/qemu.out" >&2
			exit 1
		fi
		sleep 1
	done
	echo "ok   boot: nyxd answers over vsock after ${i}s"
}

# shutdown_vm vsock|button
shutdown_vm() {
	if [ "$1" = vsock ]; then
		socat -u /dev/null "VSOCK-CONNECT:$CID:1025"
	else
		echo system_powerdown | socat - "UNIX-CONNECT:$V/monitor.sock" >/dev/null
	fi
	i=0
	while kill -0 "$QPID" 2>/dev/null; do
		i=$((i + 1))
		if [ "$i" -gt $((60 * SLOW)) ]; then
			echo "FAIL: no clean shutdown on the power button" >&2
			exit 1
		fi
		sleep 1
	done
	kill "$SPID" 2>/dev/null || true
	QPID="" SPID=""
	echo "ok   $1 shutdown: clean in ${i}s"
}

nyx() { "$NYX" -socket "$V/nyxd.sock" "$@"; }

boot
if grep -q "ERROR: nyx-datadisk" "$V/console.log"; then
	echo "FAIL: data disk not mounted" >&2
	grep -A2 "data disk" "$V/console.log" >&2
	exit 1
fi
echo "ok   data disk formatted and mounted"

nyx run -d --name web -p 18080:18080 busybox:1.36 -- sh -c 'mkdir -p /w && echo hello-from-vm >/w/index.html && exec httpd -f -p 18080 -h /w' >/dev/null
i=0
until [ "$(curl -s --max-time 2 "http://127.0.0.1:$HOSTFWD/")" = "hello-from-vm" ]; do
	i=$((i + 1))
	[ "$i" -gt $((30 * SLOW)) ] && { echo "FAIL: published port does not answer" >&2; nyx logs web >&2 || true; exit 1; }
	sleep 1
done
echo "ok   container with a published port answers on the VM address"

shutdown_vm vsock
truncate -s 12G "$V/data.img" # nyx machine init --disk-size 12
boot
nyx image ls | grep -q busybox || { echo "FAIL: image gone after reboot (data disk not kept)" >&2; exit 1; }
echo "ok   data disk kept across reboot"
kib=$(sed -n 's/.*nyx data disk: \([0-9]*\) KiB.*/\1/p' "$V/console.log" | tail -1)
[ "${kib:-0}" -gt $((10 * 1024 * 1024)) ] || { echo "FAIL: data filesystem not grown to the 12G disk (${kib:-?} KiB)" >&2; exit 1; }
echo "ok   data filesystem grown with the disk ($((kib / 1024 / 1024)) GiB)"
case "$IMG" in
*arm64*) shutdown_vm vsock ;;
*) shutdown_vm button ;;
esac
echo "all VM checks passed"
