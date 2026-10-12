#!/bin/sh
# nyxd package pre-remove: stop the system service. Containers, images and
# /var/lib/nyxd are left in place.
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl disable --now nyxd.service >/dev/null 2>&1 || true
elif command -v rc-service >/dev/null 2>&1; then
	rc-service nyxd stop >/dev/null 2>&1 || true
	rc-update del nyxd default >/dev/null 2>&1 || true
fi
exit 0
