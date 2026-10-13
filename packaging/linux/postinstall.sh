#!/bin/sh
# shellcheck disable=SC2016 # $USER is printed for the reader to type
# nyxd package post-install and post-upgrade (deb, rpm, apk, Arch): create the
# "nyx" group that owns the daemon socket, then install, enable and (re)start
# the service for the init system in use (systemd, OpenRC, runit or SysV), see
# /usr/lib/nyxd/nyxd-service. NYXD_NO_START=1 skips the start; in a container
# or chroot without a running init nothing is started.
set -e
if ! getent group nyx >/dev/null 2>&1; then
	if command -v groupadd >/dev/null 2>&1; then
		groupadd --system nyx
	elif command -v addgroup >/dev/null 2>&1; then
		addgroup -S nyx
	fi
fi
/usr/lib/nyxd/nyxd-service setup || echo "nyxd: could not set up the service; see: /usr/lib/nyxd/nyxd-service" >&2
echo 'Use nyx without sudo: add yourself to the "nyx" group, then log in again:'
if command -v usermod >/dev/null 2>&1; then
	echo '  sudo usermod -aG nyx "$USER"'
else
	echo '  sudo addgroup "$USER" nyx'
fi
if [ -d /run/systemd/system ]; then
	echo 'Rootless instead (no root daemon): systemctl --user enable --now nyxd'
fi
exit 0
