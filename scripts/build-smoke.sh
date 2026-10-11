#!/bin/sh
# End-to-end check of `nyx build` against a rootless nyxd: multi-stage build,
# COPY --from/--chown, ARG/ENV/USER/WORKDIR, .dockerignore, build cache, and
# running the result as the image USER.
#
#   make build build-nyx && scripts/build-smoke.sh
set -eu

[ "$(id -u)" -ne 0 ] || { echo "run as a non-root user" >&2; exit 1; }

BIN=${BIN:-$(cd "$(dirname "$0")/.." && pwd)/bin}
WORK=$(mktemp -d)
export NYXD_SOCKET="$WORK/nyxd.sock"
DAEMON=""

cleanup() {
	[ -n "$DAEMON" ] && kill -TERM "$DAEMON" 2>/dev/null && wait "$DAEMON" 2>/dev/null || true
	"$BIN/nyxd" unshare rm -rf "$WORK" 2>/dev/null || rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	echo "--- nyxd log ---" >&2
	tail -50 "$WORK/nyxd.log" >&2
	exit 1
}

mkdir -p "$WORK/ctx/conf" "$WORK/ctx/ignored"
cat >"$WORK/ctx/Dockerfile" <<'DOCKERFILE'
ARG BASE=alpine:3.20
FROM ${BASE} AS gen
ARG WHO=world
RUN mkdir -p /out && echo "hello $WHO" > /out/greeting

FROM ${BASE}
RUN adduser -D -u 1234 app && rm /etc/motd
COPY --from=gen --chown=app:app /out/ /srv/
COPY conf/ /etc/demo/
COPY . /ctx/
ENV MODE=smoke
WORKDIR /srv
USER app
CMD ["sh", "-c", "echo $(id -u) $MODE $(cat greeting) $(ls /etc/demo) $(test -e /etc/motd && echo motd || echo nomotd) $(stat -c %u greeting)"]
DOCKERFILE
echo a >"$WORK/ctx/conf/a.conf"
echo secret >"$WORK/ctx/ignored/secret"
echo "ignored" >"$WORK/ctx/.dockerignore"

"$BIN/nyxd" -base-dir "$WORK/data" -socket "$NYXD_SOCKET" >"$WORK/nyxd.log" 2>&1 &
DAEMON=$!
i=0
until "$BIN/nyx" ping >/dev/null 2>&1; do
	i=$((i + 1))
	[ $i -lt 50 ] || fail "daemon did not come up"
	sleep 0.2
done

"$BIN/nyx" build -t smoke:1 --build-arg WHO=nyx "$WORK/ctx" >"$WORK/build1.log" 2>&1 ||
	{ cat "$WORK/build1.log"; fail "build"; }

out=$("$BIN/nyx" run smoke:1) || fail "run"
echo "$out"
[ "$out" = "1234 smoke hello nyx a.conf nomotd 1234" ] || fail "unexpected output: $out"

"$BIN/nyx" build -t smoke:1 --build-arg WHO=nyx "$WORK/ctx" >"$WORK/build2.log" 2>&1 || fail "rebuild"
hits=$(grep -c "Using cache" "$WORK/build2.log" || true)
[ "$hits" -eq 5 ] || { cat "$WORK/build2.log"; fail "expected 5 cache hits, got $hits"; }

echo b >"$WORK/ctx/conf/b.conf"
"$BIN/nyx" build -t smoke:2 --build-arg WHO=nyx "$WORK/ctx" >"$WORK/build3.log" 2>&1 || fail "build after change"
hits=$(grep -c "Using cache" "$WORK/build3.log" || true)
[ "$hits" -eq 3 ] || { cat "$WORK/build3.log"; fail "expected 3 cache hits after changing conf/, got $hits"; }

ctx=$("$BIN/nyx" run smoke:2 -- ls -A /ctx) || fail "run ls /ctx"
echo "$ctx" | grep -qx Dockerfile || fail "context missing Dockerfile: $ctx"
echo "$ctx" | grep -qx ignored && fail ".dockerignore not applied: $ctx"

echo "build smoke test: OK"
