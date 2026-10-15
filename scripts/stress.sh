#!/bin/sh
# Load and failure test for nyxd: many containers at once, start/stop churn,
# a log flood, a container killed by the kernel OOM killer, and a daemon
# crash with containers running. Fails on errors, leaked file descriptors,
# daemon memory growth, unbounded logs, or containers that are not re-adopted.
#
#   make build build-nyx && sudo scripts/stress.sh    # rootful: every scenario
#   make build build-nyx && scripts/stress.sh         # rootless: no OOM scenario (no cgroup limits)
#
# Tunables (environment):
#   STRESS_CONTAINERS  containers running at once        (default 50)
#   STRESS_CHURN       short-lived containers to run     (default 200)
#   STRESS_PARALLEL    clients working at once           (default 8)
#   STRESS_LOG_SECONDS duration of the log flood         (default 20)
#   STRESS_RSS_GROWTH  allowed daemon RSS growth, MiB    (default 40)
#   STRESS_FD_GROWTH   allowed daemon fd growth          (default 16)
#   STRESS_BIG_IMAGE   also pull this (large) image and report time and peak RSS
#   STRESS_REPORT      write the results as Markdown to this file
#   IMAGE              base image (default alpine:3.20)
set -eu

BIN=${BIN:-$(cd "$(dirname "$0")/.." && pwd)/bin}
IMAGE=${IMAGE:-alpine:3.20}
N=${STRESS_CONTAINERS:-50}
CHURN=${STRESS_CHURN:-200}
PAR=${STRESS_PARALLEL:-8}
LOG_SECONDS=${STRESS_LOG_SECONDS:-20}
RSS_GROWTH=${STRESS_RSS_GROWTH:-40}
FD_GROWTH=${STRESS_FD_GROWTH:-16}
LOG_MAX=1m
LOG_FILES=2
# A long-running container that stops promptly on SIGTERM (a plain sleep as
# PID 1 ignores it and waits out the stop grace period).
IDLE='trap "exit 0" TERM; while :; do sleep 1; done'

WORK=$(mktemp -d)
export NYXD_SOCKET="$WORK/nyxd.sock"
# Every client call is bounded: a hang fails the test with the daemon log.
CMD_TIMEOUT=${STRESS_CMD_TIMEOUT:-120}
NYX="$WORK/nyx"
DAEMON=""
ROOTFUL=0
[ "$(id -u)" -ne 0 ] || ROOTFUL=1
RESULTS="$WORK/results"
: >"$RESULTS"
printf '#!/bin/sh\nexec timeout %s %s "$@"\n' "$CMD_TIMEOUT" "$BIN/nyx" >"$NYX"
chmod +x "$NYX"

cleanup() {
	set +e
	if [ -n "$DAEMON" ]; then
		"$NYX" ps -q 2>/dev/null | xargs -r -n 20 "$NYX" rm >/dev/null 2>&1
		kill -TERM "$DAEMON" 2>/dev/null && wait "$DAEMON" 2>/dev/null
	fi
	if [ $ROOTFUL = 1 ]; then
		rm -rf "$WORK"
	else
		# Image files belong to subordinate IDs: delete them from inside the namespace.
		"$BIN/nyxd" unshare rm -rf "$WORK" 2>/dev/null || rm -rf "$WORK" 2>/dev/null
	fi
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	echo "--- nyxd log (last 40 lines) ---" >&2
	tail -40 "$WORK/nyxd.log" >&2
	report
	exit 1
}

# result NAME VALUE: one row of the report.
result() {
	printf '%s|%s\n' "$1" "$2" >>"$RESULTS"
	printf '  %-40s %s\n' "$1" "$2"
}

report() {
	[ -n "${STRESS_REPORT:-}" ] || return 0
	{
		echo "## nyxd stress test"
		echo
		echo "$(if [ $ROOTFUL = 1 ]; then echo rootful; else echo rootless; fi), $(nproc) CPUs, $(awk '/MemTotal/ {printf "%.1f GiB", $2/1048576}' /proc/meminfo), kernel $(uname -r), $("$NYX" version 2>/dev/null | head -1)"
		echo
		echo "| Measure | Value |"
		echo "|---|---|"
		sed 's/^/| /; s/|/ | /2; s/$/ |/' "$RESULTS"
	} >"$STRESS_REPORT"
}

now_ms() { date +%s%3N; }

# The process that serves the API (rootless nyxd re-executes itself in a
# user namespace: the newest process with this base dir is the daemon).
daemon_pid() { pgrep -n -f -- "-base-dir $WORK/data" ; }
rss_mib() { awk '/^VmRSS/ {printf "%d", $2/1024}' "/proc/$(daemon_pid)/status"; }
hwm_mib() { awk '/^VmHWM/ {printf "%d", $2/1024}' "/proc/$(daemon_pid)/status"; }
fds() { ls "/proc/$(daemon_pid)/fd" | wc -l; }
running() { "$NYX" ps -q | grep -c . || true; }

start_daemon() {
	"$BIN/nyxd" -base-dir "$WORK/data" -socket "$NYXD_SOCKET" \
		-log-max-size "$LOG_MAX" -log-max-files "$LOG_FILES" >>"$WORK/nyxd.log" 2>&1 &
	DAEMON=$!
	i=0
	until "$NYX" ping >/dev/null 2>&1; do
		i=$((i + 1))
		[ $i -lt 100 ] || fail "daemon did not come up"
		sleep 0.1
	done
}

# wait_until SECONDS DESCRIPTION COMMAND...: poll until COMMAND succeeds.
wait_until() {
	limit=$1 what=$2
	shift 2
	end=$(($(date +%s) + limit))
	until "$@"; do
		[ "$(date +%s)" -lt "$end" ] || fail "$what (after ${limit}s)"
		sleep 0.2
	done
}

echo "== start ($(if [ $ROOTFUL = 1 ]; then echo rootful; else echo rootless; fi))"
start_daemon
"$NYX" pull "$IMAGE" >/dev/null 2>&1 || fail "pull $IMAGE"
"$NYX" run "$IMAGE" -- true >/dev/null 2>&1 || fail "first run"
result "daemon RSS at idle" "$(rss_mib) MiB"

echo "== $N containers at once"
t0=$(now_ms)
seq 1 "$N" | xargs -P "$PAR" -I{} "$NYX" run -d --name "many-{}" "$IMAGE" -- sh -c "$IDLE" >/dev/null ||
	fail "starting $N containers"
t1=$(now_ms)
[ "$(running)" -eq "$N" ] || fail "$(running) of $N containers running"
result "start $N containers ($PAR clients)" "$(((t1 - t0) / 1000)).$((((t1 - t0) % 1000) / 100)) s"
result "daemon RSS with $N running" "$(rss_mib) MiB"
for i in 1 $((N / 2)) "$N"; do
	"$NYX" exec "many-$i" -- sh -c 'echo ok' | grep -qx ok || fail "exec in many-$i"
done
t0=$(now_ms)
seq 1 "$N" | xargs -P "$PAR" -I{} "$NYX" rm "many-{}" >/dev/null || fail "removing $N containers"
t1=$(now_ms)
[ "$(running)" -eq 0 ] || fail "$(running) containers left after removal"
result "remove $N containers" "$(((t1 - t0) / 1000)).$((((t1 - t0) % 1000) / 100)) s"
left=$(find "$WORK/data/bundles" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
[ "$left" -eq 0 ] || fail "$left bundles left after removal"
left=$(find "$WORK/data/logs" -name 'many-*' 2>/dev/null | wc -l)
[ "$left" -eq 0 ] || fail "$left log files left after removal"

echo "== churn: $CHURN short-lived containers"
# Warm up first, so that one-time allocations do not count as growth.
seq 1 20 | xargs -P "$PAR" -I{} "$NYX" run "$IMAGE" -- true >/dev/null 2>&1 || fail "warm-up"
rss0=$(rss_mib)
fd0=$(fds)
t0=$(now_ms)
seq 1 "$CHURN" | xargs -P "$PAR" -I{} "$NYX" run "$IMAGE" -- sh -c 'echo churn-{}' >/dev/null 2>&1 ||
	fail "churn"
t1=$(now_ms)
sleep 1
rss1=$(rss_mib)
fd1=$(fds)
result "run $CHURN containers ($PAR clients)" "$(((t1 - t0) / 1000)).$((((t1 - t0) % 1000) / 100)) s ($((CHURN * 1000 / (t1 - t0 + 1))) per s)"
result "daemon RSS before / after churn" "$rss0 / $rss1 MiB"
result "daemon fds before / after churn" "$fd0 / $fd1"
[ "$(running)" -eq 0 ] || fail "$(running) containers left after churn"
ran=$(cat "$WORK/data/logs"/*.log | grep -o '"log":"churn-[0-9]*"' | sort -u | wc -l)
[ "$ran" -eq "$CHURN" ] || fail "$ran of $CHURN churn containers printed their line"
left=$(find "$WORK/data/bundles" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)
[ "$left" -eq 0 ] || fail "$left bundles left after churn"
[ $((rss1 - rss0)) -le "$RSS_GROWTH" ] || fail "daemon RSS grew by $((rss1 - rss0)) MiB (limit $RSS_GROWTH)"
[ $((fd1 - fd0)) -le "$FD_GROWTH" ] || fail "daemon fds grew by $((fd1 - fd0)) (limit $FD_GROWTH)"

echo "== log flood: ${LOG_SECONDS}s"
"$NYX" run -d --name flood "$IMAGE" -- sh -c 'yes "flood flood flood flood flood flood flood flood flood flood"' >/dev/null ||
	fail "start flood"
sleep "$LOG_SECONDS"
t0=$(now_ms)
"$NYX" logs --tail 5 flood | grep -q flood || fail "logs --tail during the flood"
t1=$(now_ms)
result "nyx logs --tail 5 during the flood" "$((t1 - t0)) ms"
[ $((t1 - t0)) -lt 2000 ] || fail "logs --tail took $((t1 - t0)) ms"
bytes=$(du -cb "$WORK/data/logs"/flood* | tail -1 | cut -f1)
limit=$((LOG_FILES * 1024 * 1024 + 65536))
result "flood log on disk (limit $LOG_FILES x $LOG_MAX)" "$((bytes / 1024)) KiB"
[ "$bytes" -le "$limit" ] || fail "flood logs use $bytes bytes (limit $limit)"
result "daemon RSS during the flood" "$(rss_mib) MiB"
"$NYX" rm flood >/dev/null || fail "rm flood"

if [ $ROOTFUL = 1 ]; then
	echo "== out of memory"
	mkdir -p "$WORK/oom"
	cat >"$WORK/oom/compose.yaml" <<EOF
services:
  hog:
    image: $IMAGE
    command: ["sh", "-c", "sleep 2; head -c 1073741824 /dev/zero | tail"]
    restart: "no"
    deploy:
      resources:
        limits:
          memory: 32m
  bystander:
    image: $IMAGE
    command: ["sh", "-c", 'trap "exit 0" TERM; while :; do sleep 1; done']
EOF
	"$NYX" compose up -f "$WORK/oom/compose.yaml" --project oom >"$WORK/compose.out" 2>&1 ||
		fail "compose up: $(tail -5 "$WORK/compose.out")"
	oom_exit() { grep '"container exited"' "$WORK/nyxd.log" | grep '"id":"oom-hog"' | grep -q '"exitCode":137'; }
	wait_until 60 "the memory hog was not OOM-killed" oom_exit
	result "container over its 32 MiB limit" "OOM-killed, exit code 137"
	"$NYX" exec oom-bystander -- sh -c 'echo ok' | grep -qx ok || fail "bystander stopped with the hog"
	"$NYX" ping >/dev/null || fail "daemon gone after the OOM kill"
	"$NYX" compose down -f "$WORK/oom/compose.yaml" --project oom >/dev/null 2>&1 || fail "compose down"
else
	result "out of memory" "skipped (rootless: no cgroup limits)"
fi

echo "== daemon crash with 10 containers running"
seq 1 10 | xargs -P "$PAR" -I{} "$NYX" run -d --name "crash-{}" "$IMAGE" -- sh -c "$IDLE" >/dev/null || fail "start crash-*"
kill -KILL "$(daemon_pid)"
[ "$(daemon_pid)" = "" ] || kill -KILL "$(daemon_pid)" 2>/dev/null || true
wait "$DAEMON" 2>/dev/null || true
DAEMON=""
t0=$(now_ms)
start_daemon
re_adopted() { [ "$(running)" -eq 10 ]; }
wait_until 30 "$(running) of 10 containers re-adopted" re_adopted
t1=$(now_ms)
result "restart after SIGKILL, 10 re-adopted" "$((t1 - t0)) ms"
out=$("$NYX" exec crash-5 -- sh -c 'echo ok' 2>&1)
[ "$out" = ok ] || fail "exec after re-adopt: $out"
seq 1 10 | xargs -P "$PAR" -I{} "$NYX" rm "crash-{}" >/dev/null || fail "rm crash-*"

if [ -n "${STRESS_BIG_IMAGE:-}" ]; then
	echo "== large image: $STRESS_BIG_IMAGE"
	t0=$(now_ms)
	"$BIN/nyx" pull "$STRESS_BIG_IMAGE" >/dev/null 2>&1 || fail "pull $STRESS_BIG_IMAGE"
	t1=$(now_ms)
	result "pull $STRESS_BIG_IMAGE" "$(((t1 - t0) / 1000)) s"
	"$NYX" run "$STRESS_BIG_IMAGE" -- true >/dev/null 2>&1 || fail "run $STRESS_BIG_IMAGE"
fi

result "daemon peak RSS (since restart)" "$(hwm_mib) MiB"
result "daemon RSS at the end" "$(rss_mib) MiB"
report
echo "stress test: OK"
