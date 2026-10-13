#!/bin/sh
# nyxd package pre-remove: stop, disable and remove the service on removal.
# An upgrade keeps it (post-install restarts it with the new binary).
# Containers, images and /var/lib/nyxd are left in place.
#   deb: $1 = remove | upgrade | failed-upgrade | deconfigure
#   rpm: $1 = number of versions left (0 = erase, 1 = upgrade)
#   apk, Arch: run on removal only
case "${1:-remove}" in
upgrade | failed-upgrade | 1) exit 0 ;;
esac
/usr/lib/nyxd/nyxd-service teardown >/dev/null 2>&1 || true
exit 0
