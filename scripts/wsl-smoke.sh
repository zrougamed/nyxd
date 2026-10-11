#!/bin/sh
# End-to-end check of `nyx machine` and the nyx WSL distro, on Linux: nyx
# drives scripts/fake-wsl.sh (a wsl.exe stand-in that boots the distro tarball
# in its own user + mount + pid namespace, chrooted, without a shell), exactly
# as nyx.exe drives wsl.exe on Windows.
#
#   make build-nyx wsl-rootfs && scripts/wsl-smoke.sh
#
# Needs: unshare, nsenter, newuidmap/newgidmap and 65536 subordinate IDs for
# the current user (same as scripts/rootless-smoke.sh), python3 (HTTP server).
set -eu

[ "$(id -u)" -ne 0 ] || { echo "run as a non-root user" >&2; exit 1; }

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${BIN:-$ROOT/bin}
ARCH=$(go env GOARCH 2>/dev/null || echo amd64)
TARBALL=${TARBALL:-$BIN/nyx-wsl-$ARCH.tar.gz}
IMAGE=${IMAGE:-alpine:3.20}
[ -f "$TARBALL" ] || { echo "missing $TARBALL (make wsl-rootfs)" >&2; exit 1; }

WORK=$(mktemp -d)
export FAKE_WSL_HOME="$WORK/wsl"
export FAKE_WSL_DRIVE_C="$WORK/c"
export NYX_WSL_EXE="$ROOT/scripts/fake-wsl.sh"
export XDG_CACHE_HOME="$WORK/cache" # nyx machine data (%LOCALAPPDATA% on Windows)
export NYXD_SOCKET=wsl://nyx        # the default endpoint of nyx.exe
NYX="$BIN/nyx"
WSL="$NYX_WSL_EXE"
HTTPD=""

cleanup() {
	[ -n "$HTTPD" ] && kill "$HTTPD" 2>/dev/null || true
	for d in "$FAKE_WSL_HOME"/distros/*/; do
		[ -d "$d" ] && "$WSL" --unregister "$(basename "$d")" 2>/dev/null || true
	done
	rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	echo "--- nyx machine logs ---" >&2
	"$NYX" machine logs >&2 2>/dev/null || true
	echo "--- fake WSL VM log ---" >&2
	cat "$FAKE_WSL_HOME"/distros/*/vm.log >&2 2>/dev/null || true
	exit 1
}

step() { echo "== $*"; }

vm_running() { # NAME
	p=$(cat "$FAKE_WSL_HOME/distros/$1/vm.pid" 2>/dev/null) && kill -0 "$p" 2>/dev/null
}

# --- init from a release-style URL (tarball + .sha256 next to it) -----------
step "machine init (download + sha256)"
mkdir -p "$WORK/www"
cp "$TARBALL" "$WORK/www/nyx-wsl.tar.gz"
(cd "$WORK/www" && sha256sum nyx-wsl.tar.gz >nyx-wsl.tar.gz.sha256)
python3 -u -m http.server --bind 127.0.0.1 --directory "$WORK/www" 0 >"$WORK/httpd.log" 2>&1 &
HTTPD=$!
i=0
until PORT=$(sed -n 's/.* port \([0-9]*\).*/\1/p' "$WORK/httpd.log" | head -n1) && [ -n "$PORT" ]; do
	i=$((i + 1))
	[ $i -lt 100 ] || fail "HTTP server did not start"
	sleep 0.1
done
URL="http://127.0.0.1:$PORT/nyx-wsl.tar.gz"

cp "$WORK/www/nyx-wsl.tar.gz.sha256" "$WORK/good.sha256"
echo "0000000000000000000000000000000000000000000000000000000000000000  nyx-wsl.tar.gz" >"$WORK/www/nyx-wsl.tar.gz.sha256"
out=$("$NYX" machine init --image "$URL" 2>&1) && fail "init accepted a tarball with the wrong checksum"
echo "$out" | grep -q "does not match" || fail "unexpected checksum error: $out"
[ ! -d "$FAKE_WSL_HOME/distros/nyx" ] || fail "distro imported despite checksum mismatch"
cp "$WORK/good.sha256" "$WORK/www/nyx-wsl.tar.gz.sha256"

"$NYX" machine init --image "$URL" || fail "machine init"
MDIR="$XDG_CACHE_HOME/nyx/machines/nyx"
[ -f "$MDIR/disk/ext4.vhdx" ] || fail "no disk in $MDIR"
[ ! -e "$MDIR/rootfs.tar" ] || fail "temporary rootfs.tar left behind"
"$NYX" machine init --image "$URL" >/dev/null 2>&1 && fail "second init of the same name succeeded"

# --- the distro has no shell --------------------------------------------------
step "no shell in the distro"
out=$("$WSL" -d nyx 2>&1) && fail "plain 'wsl -d nyx' succeeded"
echo "$out" | grep -q "has no shell" || fail "login shell did not print the hint: $out"
"$WSL" -d nyx -u root --exec /bin/sh -c true 2>/dev/null && fail "/bin/sh exists in the distro"
"$WSL" -d nyx -u root --exec /usr/local/bin/nyxd -version | grep -q '^nyxd ' || fail "nyxd -version via --exec"
"$WSL" -d nyx -u root --exec /usr/local/bin/crun --version | grep -q '^crun version' || fail "crun via --exec"

# --- start, status ------------------------------------------------------------
step "machine start"
"$NYX" machine status | grep -q "not running" || fail "status before start"
"$NYX" machine start || fail "machine start"
"$NYX" machine status | grep -q ": running" || fail "status after start"
"$NYX" machine start | grep -q "already running" || fail "second start"
"$NYX" ping >/dev/null || fail "ping over wsl://nyx"
"$NYX" machine logs | grep -q '"rootless":true' || fail "daemon is not in a user namespace"

# --- containers run remapped ----------------------------------------------------
step "run (remapped user namespace)"
"$NYX" pull "$IMAGE" >/dev/null 2>&1 || fail "pull"
out=$("$NYX" run "$IMAGE" sh -c 'id -u; cat /proc/self/uid_map') || fail "run"
echo "$out"
echo "$out" | grep -qx 0 || fail "container is not root in its namespace"
echo "$out" | awk '$1 == 0 && $2 == 100000 && $3 == 65536 { ok = 1 } END { exit !ok }' ||
	fail "container root is not VM uid 100000"
"$NYX" run -d --name smoke "$IMAGE" sleep 300 >/dev/null || fail "run -d"
"$NYX" exec smoke sh -c 'echo exec-ok' | grep -qx exec-ok || fail "exec"
"$NYX" stop smoke >/dev/null || fail "stop"

# --- build: context streamed through the stdio proxy --------------------------
step "build"
mkdir -p "$WORK/ctx"
head -c 5000000 /dev/urandom >"$WORK/ctx/blob"
printf 'FROM %s\nCOPY blob /blob\nRUN sha256sum /blob | cut -d" " -f1 > /blob.sha256\n' "$IMAGE" >"$WORK/ctx/Dockerfile"
"$NYX" build -t wsl-smoke:1 "$WORK/ctx" >"$WORK/build.log" 2>&1 || { cat "$WORK/build.log"; fail "build"; }
want=$(sha256sum "$WORK/ctx/blob" | cut -d' ' -f1)
got=$("$NYX" run wsl-smoke:1 cat /blob.sha256) || fail "run built image"
[ "$got" = "$want" ] || fail "build context corrupted: $got != $want"

# --- compose: Windows drive path and relative bind ----------------------------
step "compose (C:\\ bind, relative bind)"
mkdir -p "$WORK/c/data" "$WORK/c/proj/rel"
echo from-drive-c >"$WORK/c/data/hello.txt"
echo from-relative >"$WORK/c/proj/rel/rel.txt"
cat >"$WORK/c/proj/compose.yaml" <<EOF
services:
  reader:
    image: $IMAGE
    command: ["sh", "-c", "cat /data/hello.txt /rel/rel.txt; sleep 300"]
    volumes:
      - 'C:\\data:/data:ro'
      - ./rel:/rel
EOF
# A Windows nyx.exe sends C:\...\compose.yaml as /mnt/c/...; this Linux nyx
# passes the VM path as is.
"$NYX" compose up -f /mnt/c/proj/compose.yaml >/dev/null 2>&1 || fail "compose up"
i=0
until "$NYX" logs proj-reader 2>/dev/null | grep -q from-relative; do
	i=$((i + 1))
	[ $i -lt 50 ] || { "$NYX" logs proj-reader >&2 || true; fail "compose binds not visible"; }
	sleep 0.2
done
"$NYX" logs proj-reader | grep -q from-drive-c || fail "C:\\ bind not visible"
"$NYX" compose down -f /mnt/c/proj/compose.yaml >/dev/null 2>&1 || fail "compose down"

# --- stop / restart / terminate -------------------------------------------------
step "machine stop / start / stop --terminate"
"$NYX" machine stop || fail "machine stop"
"$NYX" machine status | grep -q "not running" || fail "status after stop"
vm_running nyx || fail "stop without --terminate shut the distro down"
"$NYX" machine logs | grep -q "nyxd stopped cleanly" || fail "unclean daemon shutdown"
"$NYX" machine start >/dev/null || fail "restart"
"$NYX" image ls | grep -q wsl-smoke || fail "images lost across restart"
"$NYX" machine stop --terminate || fail "machine stop --terminate"
vm_running nyx && fail "distro still running after --terminate"
"$NYX" machine start >/dev/null || fail "start after terminate"
"$NYX" image ls | grep -q wsl-smoke || fail "images lost across terminate"

# --- second machine from a local tarball ------------------------------------------
step "second machine (--name, local --image)"
"$NYX" machine init --name second --image "$TARBALL" >/dev/null || fail "init second"
"$NYX" machine start --name second >/dev/null || fail "start second"
NYXD_SOCKET=wsl://second "$NYX" image ls | grep -q wsl-smoke && fail "machines share images"
"$NYX" machine rm --name second >/dev/null || fail "rm second"

# --- remove -----------------------------------------------------------------------
step "machine rm"
"$NYX" machine rm || fail "machine rm"
[ ! -e "$FAKE_WSL_HOME/distros/nyx" ] || fail "distro still registered"
[ ! -e "$MDIR" ] || fail "machine directory left behind"
"$NYX" machine status | grep -q "not running" || fail "status after rm"

echo "wsl smoke test: OK"
