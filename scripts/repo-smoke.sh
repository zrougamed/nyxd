#!/bin/sh
# Package repository smoke test: builds a snapshot signed with throwaway keys,
# turns it into the pkgs.nyxd.io repositories (scripts/build-repos.sh), serves
# them over HTTP and, in a fresh container per distribution, runs the one-line
# install against them. Each install must finish within LIMIT seconds (60),
# leave a working nyxd and nyx, and uninstall cleanly.
#
#   scripts/repo-smoke.sh               # every distribution
#   scripts/repo-smoke.sh fedora:41     # images whose name contains the argument
#
# Needs docker or podman (ENGINE), Go, gpg, openssl and python3.
set -eu
cd "$(dirname "$0")/.."
ENGINE=${ENGINE:-docker}
GORELEASER=${GORELEASER:-go run github.com/goreleaser/goreleaser/v2@v2.18.2}
LIMIT=${LIMIT:-60}
ONLY=${1:-}
W=$(mktemp -d)
HTTPD=""
trap '[ -n "$HTTPD" ] && kill "$HTTPD" 2>/dev/null; command rm -rf "$W"' EXIT

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
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
python3 -m http.server --bind 127.0.0.1 --directory "$W/repo" "$port" >"$W/httpd.log" 2>&1 &
HTTPD=$!
URL="http://127.0.0.1:$port"
i=0
until curl -fs "$URL/nyxd.asc" >/dev/null; do
	i=$((i + 1))
	[ $i -lt 50 ] || { echo "repository server did not start" >&2; exit 1; }
	sleep 0.1
done

# What a user runs, against the local repository; then the checks.
SCRIPT='
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
	out=$($ENGINE run --rm --network host -e URL="$URL" "$image" sh -c "$prep; $SCRIPT" 2>&1) || true
	secs=$(printf '%s\n' "$out" | sed -n 's/^seconds=//p')
	missing=""
	for want in from-repo nyx-ok crun-ok removed repo-config-removed; do
		printf '%s\n' "$out" | grep -qx "$want" || missing="$missing $want"
	done
	if [ -z "$missing" ] && [ -n "$secs" ] && [ "$secs" -le "$LIMIT" ]; then
		echo "ok   $image (${secs}s)"
	else
		echo "FAIL $image (${secs:-?}s, missing:${missing:- none})"
		printf '%s\n' "$out" | tail -25 | sed 's/^/    /'
		echo x >>"$W/failed"
	fi
done
[ ! -f "$W/failed" ] || { echo "$(wc -l <"$W/failed") distribution(s) failed"; exit 1; }
echo "all distributions installed from the repository within ${LIMIT}s"
