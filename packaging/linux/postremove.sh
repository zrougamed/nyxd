#!/bin/sh
# nyxd package post-remove: on purge (deb), also drop /etc/default/nyxd.
# Images, containers and volumes under /var/lib/nyxd are never removed here.
if [ "${1:-}" = purge ]; then
	rm -f /etc/default/nyxd
fi
exit 0
