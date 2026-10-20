#!/bin/sh
# Package repository smoke test: builds a snapshot signed with throwaway keys,
# turns it into the pkgs.nyxd.io repositories (scripts/build-repos.sh), serves
# them over HTTP and, in a fresh container per distribution, runs the one-line
# install against them. Leave a working nyxd and nyx, and uninstall cleanly.
# LIMIT (default 120) is only a soft target: a successful but slow install
# (cold Rocky/RHEL mirrors often take minutes) is reported as ok-slow, not a
# failure. Missing install/uninstall checks still fail the job.
#
#   scripts/repo-smoke.sh               # every distribution
#   scripts/repo-smoke.sh fedora:41     # images whose name contains the argument
#
# Needs Go, gpg, openssl and a container engine (ENGINE): docker or podman,
# or nyx, which drives nyxd (NYXD_SOCKET) with an nyx built from this
# checkout. Unset, it is docker when a Docker daemon answers, else nyx when
# the nyxd socket exists. nyxd resolves bind mounts on its own host, so the
# temporary directory (TMPDIR) must have the same path there: gh-runners'
# RUNNER_NYXD=1 sets that up.
set -eu
cd "$(dirname "$0")/.."
GORELEASER=${GORELEASER:-go run github.com/goreleaser/goreleaser/v2@v2.18.2}
LIMIT=${LIMIT:-120}
ONLY=${1:-}
W=$(mktemp -d)
trap 'command rm -rf "$W"' EXIT

if [ -z "${ENGINE:-}" ]; then
	if docker info >/dev/null 2>&1; then
		ENGINE=docker
	elif [ -S "${NYXD_SOCKET:-/run/nyxd/nyxd.sock}" ]; then
		ENGINE=nyx
	else
		echo "repo-smoke: no container engine: start docker or nyxd, or set ENGINE" >&2
		exit 1
	fi
fi
# The repository is served inside each container (scripts/smokehttpd).
mkdir "$W/bin"
CGO_ENABLED=0 go build -o "$W/bin/smokehttpd" ./scripts/smokehttpd
RESOLV_CONF=""
if [ "$ENGINE" = nyx ]; then
	CGO_ENABLED=0 go build -o "$W/bin/nyx" ./cmd/nyx
	ENGINE=$W/bin/nyx
	# nyxd's containers resolve names through its bridge, which a firewalled
	# runner's policy may block; use this machine's resolvers unless they are
	# local ones the containers cannot reach.
	case $(awk '/^nameserver/ {print $2; exit}' /etc/resolv.conf) in
	"" | 127.*) ;;
	*) cp /etc/resolv.conf "$W/resolv.conf" && RESOLV_CONF=$W/resolv.conf ;;
	esac
fi
export RESOLV_CONF

# engine_run ARGS...: a throwaway container.
engine_run() {
	if [ -n "$RESOLV_CONF" ]; then
		$ENGINE run --rm -v "$RESOLV_CONF:/etc/resolv.conf:ro" "$@"
	else
		$ENGINE run --rm "$@"
	fi
}

mkdir "$W/probe" && echo bind-ok >"$W/probe/ok"
if [ "$(engine_run -v "$W/probe:/probe:ro" docker.io/library/alpine:3.20 cat /probe/ok 2>&1)" != bind-ok ]; then
	echo "repo-smoke: $ENGINE cannot bind $W into a container; with nyx, nyxd must be v1.1.6 or later" >&2
	echo "and see $W at the same path (gh-runners: RUNNER_NYXD=1)" >&2
	exit 1
fi

# Throwaway keys: GPG for deb, rpm, pacman and the indexes; RSA for apk.
export GNUPGHOME="$W/gnupg"
mkdir -m 700 "$GNUPGHOME"
gpg --batch --quiet --passphrase '' --quick-gen-key 'nyxd repo smoke <smoke@nyxd.invalid>' rsa3072 sign never 2>/dev/null
GPG_FINGERPRINT=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr/ {print $10; exit}')
export GPG_FINGERPRINT
gpg --batch --armor --export-secret-keys "$GPG_FINGERPRINT" >"$W/gpg.key"
openssl genrsa -out "$W/apk.rsa" 2048 2>/dev/null

echo "== snapshot packages"
# Signed like a release: nfpm signs the packages, GoReleaser the pacman .sig files.
NFPM_KEY_FILE="$W/gpg.key" NFPM_PASSPHRASE='' NFPM_APK_KEY_FILE="$W/apk.rsa" GPG_PASSPHRASE='' \
	$GORELEASER release --snapshot --clean --skip=publish >"$W/goreleaser.log" 2>&1 ||
	{ tail -20 "$W/goreleaser.log"; exit 1; }
mkdir "$W/pkgs"
cp dist/*.deb dist/*.rpm dist/*.apk dist/*.pkg.tar.zst dist/*.pkg.tar.zst.sig "$W/pkgs/"

echo "== repositories"
ENGINE=$ENGINE APK_KEY_FILE="$W/apk.rsa" scripts/build-repos.sh "$W/pkgs" "$W/repo" >"$W/repos.log" 2>&1 ||
	{ tail -20 "$W/repos.log"; exit 1; }
cp scripts/install.sh "$W/repo/install.sh"
URL=http://127.0.0.1:8080

# What a user runs, against the local repository; then the checks. The
# repository server started before the image's own preparation.
SCRIPT='
i=0
until curl -fs "$URL/nyxd.asc" >/dev/null; do
	i=$((i + 1))
	[ $i -lt 50 ] || { echo "repository server did not start"; exit 1; }
	sleep 0.1
done
start=$(date +%s)
curl -fsSL "$URL/install.sh" | NYXD_REPO_URL="$URL" sh -s -- --no-start >/tmp/install.log 2>&1 || { cat /tmp/install.log; echo INSTALL-FAILED; exit 1; }
echo "seconds=$(( $(date +%s) - start ))"
grep -q "installed from $URL" /tmp/install.log && echo from-repo
nyxd -version | head -1
nyx --help >/dev/null 2>&1 && echo nyx-ok
command -v crun >/dev/null && echo crun-ok
NYXD_REPO_URL="$URL" sh -c "curl -fsSL $URL/install.sh | sh -s -- --uninstall" >/dev/null 2>&1
[ -e /usr/bin/nyxd ] || echo removed
ls /etc/apt/sources.list.d/nyxd.list /etc/yum.repos.d/nyxd.repo /etc/zypp/repos.d/nyxd.repo /usr/share/keyrings/nyxd.gpg 2>/dev/null | grep . || echo repo-config-removed
'
# image|command that makes curl available (bare images lack it)
images='
docker.io/library/ubuntu:24.04|apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null
docker.io/library/ubuntu:22.04|apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null
docker.io/library/debian:12|apt-get update -qq && apt-get install -y -qq curl ca-certificates >/dev/null
registry.fedoraproject.org/fedora:41|true
docker.io/rockylinux/rockylinux:9|true
docker.io/opensuse/leap:15.6|zypper -q --non-interactive install curl gzip >/dev/null
docker.io/library/alpine:3.20|apk add -q curl
docker.io/library/archlinux:latest|true
'
echo "$images" | while IFS='|' read -r image prep; do
	[ -n "$image" ] || continue
	case $image in *"$ONLY"*) ;; *) continue ;; esac
	out=$(engine_run -v "$W/repo:/repo:ro" -v "$W/bin/smokehttpd:/usr/local/bin/smokehttpd:ro" -e URL="$URL" \
		"$image" sh -c "smokehttpd /repo 127.0.0.1:8080 & $prep; $SCRIPT" 2>&1) || true
	# Take the last seconds= line (docker pull noise can precede the script).
	secs=$(printf '%s\n' "$out" | sed -n 's/^seconds=//p' | tail -1)
	missing=""
	for want in from-repo nyx-ok crun-ok removed repo-config-removed; do
		printf '%s\n' "$out" | grep -qx "$want" || missing="$missing $want"
	done
	if [ -n "$missing" ] || [ -z "$secs" ]; then
		echo "FAIL $image (${secs:-?}s, missing:${missing:- none})"
		printf '%s\n' "$out" | tail -25 | sed 's/^/    /'
		echo x >>"$W/failed"
	elif [ "$secs" -gt "$LIMIT" ]; then
		echo "ok   $image (${secs}s, slow >${LIMIT}s)"
	else
		echo "ok   $image (${secs}s)"
	fi
done
[ ! -f "$W/failed" ] || { echo "$(wc -l <"$W/failed") distribution(s) failed"; exit 1; }
echo "all distributions installed from the repository (soft target ${LIMIT}s)"
