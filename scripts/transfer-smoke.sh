#!/bin/sh
# End-to-end check of image transfer against a rootless nyxd: run registry:2
# with nyx itself, build, tag, push (twice: second is all "exists"), delete
# locally, pull back, run, then save / load round trip.
#
#   make build build-nyx && scripts/transfer-smoke.sh
set -eu

[ "$(id -u)" -ne 0 ] || { echo "run as a non-root user" >&2; exit 1; }

BIN=${BIN:-$(cd "$(dirname "$0")/.." && pwd)/bin}
PORT=${REGISTRY_PORT:-5000}
WORK=$(mktemp -d)
export NYXD_SOCKET="$WORK/nyxd.sock"
DAEMON=""

cleanup() {
	"$BIN/nyx" stop smoke-registry >/dev/null 2>&1 || true
	[ -n "$DAEMON" ] && kill -TERM "$DAEMON" 2>/dev/null && wait "$DAEMON" 2>/dev/null || true
	"$BIN/nyxd" unshare rm -rf "$WORK" 2>/dev/null || rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	tail -40 "$WORK/nyxd.log" >&2
	exit 1
}

"$BIN/nyxd" -base-dir "$WORK/data" -socket "$NYXD_SOCKET" >"$WORK/nyxd.log" 2>&1 &
DAEMON=$!
i=0
until "$BIN/nyx" ping >/dev/null 2>&1; do
	i=$((i + 1)); [ $i -lt 50 ] || fail "daemon did not come up"; sleep 0.2
done

"$BIN/nyx" run -d --name smoke-registry -e "REGISTRY_HTTP_ADDR=127.0.0.1:$PORT" registry:2 >/dev/null || fail "start registry"
i=0
until wget -q -O /dev/null "http://127.0.0.1:$PORT/v2/" 2>/dev/null || curl -sf "http://127.0.0.1:$PORT/v2/" >/dev/null 2>&1; do
	i=$((i + 1)); [ $i -lt 80 ] || fail "registry did not come up"; sleep 0.25
done

mkdir -p "$WORK/ctx"
printf 'FROM alpine:3.20\nRUN echo transferred > /msg\nCMD ["cat", "/msg"]\n' >"$WORK/ctx/Dockerfile"
"$BIN/nyx" build -q -t smoke:1 "$WORK/ctx" >/dev/null || fail "build"

REF="localhost:$PORT/smoke/app:1"
"$BIN/nyx" tag smoke:1 "$REF" || fail "tag"
"$BIN/nyx" push "$REF" >"$WORK/push1.log" || { cat "$WORK/push1.log"; fail "push"; }
grep -q "pushing" "$WORK/push1.log" || fail "first push uploaded nothing"
"$BIN/nyx" push "$REF" >"$WORK/push2.log" || fail "re-push"
grep -q "pushing" "$WORK/push2.log" && { cat "$WORK/push2.log"; fail "re-push uploaded blobs again"; }

"$BIN/nyx" image rm "$REF" smoke:1 >/dev/null || fail "image rm"
"$BIN/nyx" pull "$REF" >/dev/null 2>&1 || fail "pull back"
out=$("$BIN/nyx" run "$REF" --) || fail "run pulled image"
[ "$out" = "transferred" ] || fail "pulled image output: $out"

"$BIN/nyx" save -o "$WORK/app.tar" "$REF" || fail "save"
"$BIN/nyx" image rm "$REF" >/dev/null || fail "image rm before load"
"$BIN/nyx" load -i "$WORK/app.tar" | grep -q "Loaded image: $REF" || fail "load"
out=$("$BIN/nyx" run "$REF" --) || fail "run loaded image"
[ "$out" = "transferred" ] || fail "loaded image output: $out"

echo "transfer smoke test: OK"
