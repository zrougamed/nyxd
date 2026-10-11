#!/bin/sh
# End-to-end check of rootless nyxd: start the daemon as the current
# (non-root) user, pull an image, run containers, exec, stop, shut down.
#
#   make build build-nyx && scripts/rootless-smoke.sh
set -eu

[ "$(id -u)" -ne 0 ] || { echo "run as a non-root user" >&2; exit 1; }

BIN=${BIN:-$(cd "$(dirname "$0")/.." && pwd)/bin}
IMAGE=${IMAGE:-alpine:3.20}
WORK=$(mktemp -d)
export NYXD_SOCKET="$WORK/nyxd.sock"
DAEMON=""

cleanup() {
	[ -n "$DAEMON" ] && kill -TERM "$DAEMON" 2>/dev/null && wait "$DAEMON" 2>/dev/null || true
	# Image files belong to subordinate IDs: delete them from inside the namespace.
	"$BIN/nyxd" unshare rm -rf "$WORK" 2>/dev/null || rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	echo "--- nyxd log ---" >&2
	cat "$WORK/nyxd.log" >&2
	exit 1
}

"$BIN/nyxd" -base-dir "$WORK/data" -socket "$NYXD_SOCKET" >"$WORK/nyxd.log" 2>&1 &
DAEMON=$!

i=0
until "$BIN/nyx" ping >/dev/null 2>&1; do
	i=$((i + 1))
	[ $i -lt 50 ] || fail "daemon did not come up"
	sleep 0.2
done
grep -q '"rootless":true' "$WORK/nyxd.log" || fail "daemon not in rootless mode"

"$BIN/nyx" pull "$IMAGE" >/dev/null 2>&1 || fail "pull"

out=$("$BIN/nyx" run "$IMAGE" -- sh -c 'id -u; cat /etc/alpine-release; test ! -e /.nyxd-extracted && echo clean') ||
	fail "run"
echo "$out"
echo "$out" | grep -qx 0 || fail "container is not root in its namespace"
echo "$out" | grep -qx clean || fail "nyxd marker file leaked into rootfs"

"$BIN/nyx" run -d --name smoke "$IMAGE" -- sleep 300 >/dev/null || fail "run -d"
"$BIN/nyx" exec smoke -- sh -c 'echo exec-ok' | grep -qx exec-ok || fail "exec"
"$BIN/nyx" stop smoke >/dev/null || fail "stop"

kill -TERM "$DAEMON"
wait "$DAEMON" || fail "daemon exit status"
DAEMON=""
grep -q "nyxd stopped cleanly" "$WORK/nyxd.log" || fail "unclean shutdown"
echo "rootless smoke test: OK"
