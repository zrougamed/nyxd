#!/bin/sh
# A stand-in for wsl.exe on Linux, for testing `nyx machine` and the nyx WSL
# distro without Windows (see scripts/wsl-smoke.sh). Point nyx at it with
# NYX_WSL_EXE. It implements the calls nyx makes:
#
#   --import NAME DIR TARBALL --version 2
#   -d NAME [-u root] [--exec CMD ARGS...]    no --exec: run root's login shell
#   --terminate NAME | --unregister NAME
#
# Like WSL2, every distro is a "VM" (user + mount + pid namespace) that boots on
# the first command and is shared by all later ones. The imported tarball is the
# root of that mount namespace (scripts/fakewsl-init pivots into it, as WSL's
# init does), so anything the distro lacks (a shell, libc) is missing here too. Unprivileged: VM root is the calling user and VM uids
# 100000-165535 are the user's subordinate range, so nyxd -userns-remap=default
# works unchanged. WSL's /proc, /sys, /dev, /run (tmpfs), /mnt/c automount and
# generated /etc/resolv.conf and /etc/hosts are reproduced.
#
# Environment: FAKE_WSL_HOME (state, required), FAKE_WSL_DRIVE_C (directory
# mounted at /mnt/c, default FAKE_WSL_HOME/c).
set -eu

HOME_DIR=${FAKE_WSL_HOME:?FAKE_WSL_HOME must be set}
SRC=$(cd "$(dirname "$0")/.." && pwd)
DRIVE_C=${FAKE_WSL_DRIVE_C:-$HOME_DIR/c}
REMAP_START=100000 # nyxd -userns-remap=default
REMAP_COUNT=65536

die() { echo "wsl: $*" >&2; exit 1; }

# subid FILE: start of the caller's range in /etc/subuid or /etc/subgid.
subid() {
	awk -F: -v u="$(id -un)" -v n="$(id -u)" -v c="$REMAP_COUNT" \
		'($1 == u || $1 == n) && $3 >= c { print $2; exit }' "$1"
}

# in_vm DISTRO_DIR CMD...: run CMD in a new user + mount + pid namespace with
# the VM ID mapping, in the background, output to DISTRO_DIR/vm.log; sets UPID
# to the pid of the unshare process. The VM outlives this wsl call (like the
# WSL utility VM), so it must not hold the caller's pipes open.
in_vm() {
	d=$1
	shift
	su=$(subid /etc/subuid)
	sg=$(subid /etc/subgid)
	[ -n "$su" ] && [ -n "$sg" ] || die "need $REMAP_COUNT subordinate IDs in /etc/subuid and /etc/subgid for $(id -un)"
	fifo="$d/.map.$$"
	rm -f "$fifo"
	mkfifo "$fifo"
	# The shell waits for the ID maps, then execs CMD as namespace root (exec
	# grants it full capabilities in the namespace).
	unshare --user --mount --pid --fork --kill-child --propagation private \
		sh -c 'read _ <"$0"; rm -f "$0"; exec "$@"' "$fifo" "$@" </dev/null >>"$d/vm.log" 2>&1 &
	upid=$!
	self=$(readlink /proc/self/ns/user)
	i=0
	while [ "$(readlink "/proc/$upid/ns/user" 2>/dev/null || echo "$self")" = "$self" ]; do
		i=$((i + 1))
		[ $i -lt 500 ] || die "user namespace was not created"
		sleep 0.01
	done
	newuidmap "$upid" 0 "$(id -u)" 1 "$REMAP_START" "$su" "$REMAP_COUNT"
	newgidmap "$upid" 0 "$(id -g)" 1 "$REMAP_START" "$sg" "$REMAP_COUNT"
	echo >"$fifo"
	UPID=$upid
}

distro_dir() {
	[ -n "${1:-}" ] || die "missing distro name"
	echo "$HOME_DIR/distros/$1"
}

holder_pid() {
	cat "$1/vm.pid" 2>/dev/null || true
}

running() {
	p=$(holder_pid "$1")
	[ -n "$p" ] && kill -0 "$p" 2>/dev/null
}

boot() {
	d=$1
	running "$d" && return 0
	R="$d/rootfs"
	mkdir -p "$DRIVE_C"
	init="$HOME_DIR/fakewsl-init"
	[ -x "$init" ] || (cd "$SRC" && CGO_ENABLED=0 go build -o "$init" ./scripts/fakewsl-init) ||
		die "cannot build fakewsl-init"
	rm -f "$d/vm.pid" "$d/ready"
	in_vm "$d" sh -c '
		set -eu
		R=$1 C=$2 READY=$3 INIT=$4
		mount --bind "$R" "$R"
		mount -t proc proc "$R/proc"
		mount --rbind /sys "$R/sys"
		mount --rbind /dev "$R/dev"
		mount -t tmpfs -o mode=0755,nosuid,nodev tmpfs "$R/run"
		mkdir -p "$R/mnt/c"
		mount --bind "$C" "$R/mnt/c"
		cat /etc/resolv.conf >"$R/etc/resolv.conf"
		printf "127.0.0.1\tlocalhost\n127.0.1.1\t%s\n::1\tlocalhost\n" "$(uname -n)" >"$R/etc/hosts"
		exec "$INIT" "$R" "$READY"
	' boot "$R" "$DRIVE_C" "$d/ready" "$init"
	upid=$UPID
	i=0
	until [ -s "$d/ready" ]; do
		i=$((i + 1))
		kill -0 "$upid" 2>/dev/null || die "distro failed to boot: $(cat "$d/vm.log")"
		[ $i -lt 500 ] || { kill -KILL "$upid"; die "distro did not boot"; }
		sleep 0.01
	done
	# The VM's pid 1 is the child of the unshare process.
	pid1=$(cat "/proc/$upid/task/$upid/children" 2>/dev/null || true)
	pid1=${pid1%% *}
	[ -n "$pid1" ] || { kill -KILL "$upid"; die "distro failed to boot: no init"; }
	echo "$pid1" >"$d/vm.pid"
}

terminate() {
	d=$1
	p=$(holder_pid "$d")
	if [ -n "$p" ] && kill -0 "$p" 2>/dev/null; then
		kill -KILL "$p" # pid 1 of the VM: everything in it goes too
		while kill -0 "$p" 2>/dev/null; do sleep 0.01; done
	fi
	rm -f "$d/vm.pid" "$d/ready"
}

cmd_import() {
	[ $# -eq 5 ] && [ "$4" = "--version" ] || die "usage: --import NAME DIR TARBALL --version 2"
	[ "$5" = 2 ] || die "only WSL 2 is emulated"
	d=$(distro_dir "$1")
	[ ! -e "$d" ] || die "A distribution with the supplied name already exists."
	mkdir -p "$d/rootfs" "$2"
	tar -xf "$3" -C "$d/rootfs" || { rm -rf "$d"; die "import failed"; }
	: >"$2/ext4.vhdx"
	echo "$2" >"$d/installdir"
}

cmd_unregister() {
	d=$(distro_dir "$1")
	[ -d "$d" ] || die "There is no distribution with the supplied name."
	terminate "$d"
	# Files there belong to the VM's subordinate IDs: remove them from inside.
	in_vm "$d" rm -rf --one-file-system "$d/rootfs"
	wait "$UPID" || die "unregister: cannot remove $d/rootfs"
	rm -f "$(cat "$d/installdir")/ext4.vhdx"
	rm -rf "$d"
}

cmd_run() {
	name="" user=root
	while [ $# -gt 0 ]; do
		case $1 in
		-d | --distribution) name=$2; shift 2 ;;
		-u | --user) user=$2; shift 2 ;;
		-e | --exec) shift; break ;;
		*) die "unsupported argument: $1" ;;
		esac
	done
	[ "$user" = root ] || die "only -u root is emulated"
	d=$(distro_dir "$name")
	[ -d "$d" ] || die "There is no distribution with the supplied name."
	if [ $# -eq 0 ]; then
		# Like WSL: no --exec starts the user's login shell.
		shell=$(awk -F: '$1 == "root" { print $7 }' "$d/rootfs/etc/passwd")
		[ -n "$shell" ] || die "root has no login shell"
		set -- "$shell"
	fi
	boot "$d"
	exec env -i PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
		HOME=/root USER=root LOGNAME=root LANG=C.UTF-8 TERM="${TERM:-dumb}" WSL_DISTRO_NAME="$name" \
		nsenter -t "$(holder_pid "$d")" --user --mount --pid --root --wd -- "$@"
}

mkdir -p "$HOME_DIR/distros"
case ${1:-} in
--import) shift; cmd_import "$@" ;;
--terminate | -t) d=$(distro_dir "${2:-}"); [ -d "$d" ] || die "There is no distribution with the supplied name."; terminate "$d" ;;
--unregister) cmd_unregister "${2:-}" ;;
-d | --distribution) cmd_run "$@" ;;
*) die "unsupported: $*" ;;
esac
