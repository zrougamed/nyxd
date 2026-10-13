#!/bin/sh
# Installer smoke test: builds an unsigned snapshot, stages it as a fake
# v9.9.9 release, and runs install.sh / the packages in containers for every
# init system and package format nyxd supports, checking that the service is
# installed, enabled, running (where an init runs) and removed again.
#
#   scripts/install-smoke.sh            # all scenarios
#   scripts/install-smoke.sh openrc     # one: systemd openrc sysv runit rpm arch rootless tamper
#
# Needs podman (systemd and OpenRC run as PID 1 in their containers) and Go
# (GoReleaser runs with `go run`). The containers' nyxd uses -net-driver host:
# an unprivileged container cannot create bridges.
set -eu
cd "$(dirname "$0")/.."
ENGINE=${ENGINE:-podman}
GORELEASER=${GORELEASER:-go run github.com/goreleaser/goreleaser/v2@v2.18.2}
ONLY=${1:-all}
REL=$(mktemp -d)
trap 'command rm -rf "$REL"; $ENGINE rm -f nyxd-smoke-systemd nyxd-smoke-openrc >/dev/null 2>&1 || true' EXIT
# A stale registry login must not stop anonymous image pulls.
echo '{}' >"$REL/.noauth.json"
export REGISTRY_AUTH_FILE="$REL/.noauth.json"

$GORELEASER release --snapshot --clean --skip=publish,sign >/dev/null 2>&1
cp dist/nyxd_*_amd64.deb "$REL/nyxd_9.9.9_amd64.deb"
cp dist/nyxd-*.x86_64.rpm "$REL/nyxd-9.9.9-1.x86_64.rpm"
cp dist/nyxd_*_x86_64.apk "$REL/nyxd_9.9.9_x86_64.apk"
cp dist/nyxd-*-x86_64.pkg.tar.zst "$REL/nyxd-9.9.9-1-x86_64.pkg.tar.zst"
cp dist/nyxd_*_linux_amd64.tar.gz "$REL/nyxd_9.9.9_linux_amd64.tar.gz"
cp scripts/install.sh "$REL/"
echo v9.9.9 >"$REL/VERSION"
(cd "$REL" && sha256sum nyxd* >checksums.txt)
chmod -R a+rX "$REL" # the rootless scenario reads it as another user

pass=0
fail=0
# check NAME OUTPUT PATTERN...: every pattern must appear in OUTPUT.
check() {
	name=$1 out=$2
	shift 2
	for p in "$@"; do
		if ! printf '%s\n' "$out" | grep -q -- "$p"; then
			echo "FAIL $name: missing \"$p\""
			printf '%s\n' "$out" | sed 's/^/    /'
			fail=$((fail + 1))
			return
		fi
	done
	echo "ok   $name"
	pass=$((pass + 1))
}
want() { [ "$ONLY" = all ] || [ "$ONLY" = "$1" ]; }
run() { $ENGINE run --rm -v "$REL:/rel:ro,Z" "$@" 2>&1; }
opts='mkdir -p /etc/default && echo "NYXD_OPTS=\"-net-driver host\"" >/etc/default/nyxd'
install='NYXD_RELEASE_URL=file:///rel sh /rel/install.sh'

if want systemd; then
	$ENGINE run -d --name nyxd-smoke-systemd --systemd=always -v "$REL:/rel:ro,Z" docker.io/jrei/systemd-debian:12 >/dev/null
	sleep 3
	out=$($ENGINE exec nyxd-smoke-systemd sh -c "apt-get update -qq >/dev/null 2>&1; $opts; $install 2>&1 | grep nyxd
		sleep 2; echo enabled=\$(systemctl is-enabled nyxd) active=\$(systemctl is-active nyxd); nyx ping
		$install --uninstall >/dev/null 2>&1; echo after=\$(systemctl is-active nyxd); ls /var/lib/nyxd >/dev/null && echo data-kept" 2>&1) || true
	check "deb + systemd" "$out" "systemd service installed, enabled and running" "enabled=enabled active=active" '"ok":true' "after=inactive" "data-kept"
fi

if want openrc; then
	$ENGINE run -d --name nyxd-smoke-openrc -v "$REL:/rel:ro,Z" docker.io/library/alpine:3.20 \
		sh -c 'apk add -q openrc >/dev/null && sed -i "s/^#rc_sys=\"\"/rc_sys=\"docker\"/" /etc/rc.conf && exec /sbin/init' >/dev/null
	sleep 6
	out=$($ENGINE exec nyxd-smoke-openrc sh -c "$opts; $install 2>&1 | grep nyxd:; sleep 1; rc-service nyxd status; nyx ping
		apk add -q --allow-untrusted --force-overwrite /rel/nyxd_9.9.9_x86_64.apk >/dev/null 2>&1; rc-service nyxd status
		apk del -q nyxd >/dev/null 2>&1; rc-service nyxd status 2>&1; pgrep nyxd || echo stopped-after-remove" 2>&1) || true
	check "apk + OpenRC (install, upgrade, remove)" "$out" "openrc service installed, enabled and running" "status: started" '"ok":true' "does not exist" "stopped-after-remove"
fi

if want sysv; then
	out=$(run docker.io/library/debian:12 sh -c "apt-get update -qq >/dev/null 2>&1; $opts; $install --method binary 2>&1 | grep nyxd
		grep ^DAEMON= /etc/init.d/nyxd; sleep 1; /usr/local/bin/nyx ping
		$install --uninstall >/dev/null 2>&1; ls /etc/init.d/nyxd /usr/local/bin/nyxd 2>&1") || true
	check "binary + SysV" "$out" "sysv service installed, enabled and running" "DAEMON=/usr/local/bin/nyxd" '"ok":true' "init.d/nyxd': No such file"
fi

if want runit; then
	out=$(run ghcr.io/void-linux/void-glibc-full:latest sh -c "(runsvdir /etc/runit/runsvdir/default >/dev/null 2>&1 &)
		xbps-install -Sy crun >/dev/null 2>&1; $opts; $install 2>&1 | grep nyxd
		for i in 1 2 3 4 5 6; do /usr/local/bin/nyx ping 2>/dev/null && break; sleep 1; done
		$install --uninstall >/dev/null 2>&1; sleep 1; pgrep nyxd || echo stopped-after-remove") || true
	check "binary + runit (Void)" "$out" "runit service installed, enabled and running" '"ok":true' "stopped-after-remove"
fi

if want rpm; then
	out=$(run registry.fedoraproject.org/fedora:41 sh -c "$install 2>&1 | grep -E 'nyxd-service|nyxd v9'; rpm -q nyxd crun
		$install --uninstall >/dev/null 2>&1; rpm -q nyxd") || true
	check "rpm (no init running)" "$out" "no supported init system found" "nyxd v9.9.9 installed" "crun-" "package nyxd is not installed"
fi

if want arch; then
	out=$(run docker.io/library/archlinux:latest sh -c "pacman -Sy --noconfirm >/dev/null 2>&1; $install 2>&1 | grep -E 'nyxd-service|nyxd v9'
		pacman -Q nyxd crun; $install --uninstall >/dev/null 2>&1; pacman -Q nyxd") || true
	check "Arch package (no init running)" "$out" "no supported init system found" "nyxd v9.9.9 installed" "crun " "was not found"
fi

if want rootless; then
	out=$(run docker.io/library/debian:12 sh -c "useradd -m alice; su alice -c '$install --rootless 2>&1; ls ~/.local/bin; $install --rootless --uninstall 2>&1; ls ~/.local/bin'") || true
	check "rootless" "$out" "nyxd v9.9.9 installed" "nyxd" "removed the rootless install"
fi

if want tamper; then
	printf x >>"$REL/nyxd_9.9.9_linux_amd64.tar.gz"
	out=$(run docker.io/library/debian:12 sh -c "$install --method binary; echo exit=\$?; ls /usr/local/bin/nyxd 2>&1") || true
	check "tampered download rejected" "$out" "checksum mismatch" "exit=1" "No such file"
fi

echo "$pass passed, $fail failed"
[ "$fail" = 0 ]
