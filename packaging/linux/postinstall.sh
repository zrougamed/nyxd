#!/bin/sh
# nyxd package post-install: the "nyx" group owns the daemon socket.
set -e
if ! getent group nyx >/dev/null 2>&1; then
	if command -v groupadd >/dev/null 2>&1; then
		groupadd --system nyx
	elif command -v addgroup >/dev/null 2>&1; then
		addgroup -S nyx
	fi
fi
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload || true
fi
cat <<'MSG'
nyxd installed. Start it with
  systemd:  sudo systemctl enable --now nyxd
  OpenRC:   sudo rc-update add nyxd default && sudo rc-service nyxd start
and add yourself to the "nyx" group to use nyx without sudo:
  sudo usermod -aG nyx "$USER"   (Alpine: sudo addgroup "$USER" nyx)
MSG
