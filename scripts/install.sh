#!/bin/sh
# nyxd installer for Linux and macOS.
#
#   curl -fsSL https://github.com/zrougamed/nyxd/releases/latest/download/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --rootless
#
# Linux, as root (sudo is used when needed): installs the native package when
# the system has a package manager nyxd ships for (apt/dpkg, dnf/yum/zypper,
# apk, pacman), else the binaries under --prefix. Either way the nyxd service
# is installed, enabled and started for the init system in use: systemd,
# OpenRC, runit or SysV (see packaging/linux/nyxd-service).
#
# Linux, --rootless: nyxd and nyx in ~/.local/bin, run by a systemd user
# service; no root needed (subordinate IDs must exist, see docs/rootless.md).
#
# macOS: installs the nyx client. nyxd itself needs Linux (a VM or a server).
#
# Options:
#   --version vX.Y.Z   release to install (default: the latest)
#   --method M         auto (default) | package | binary
#   --prefix DIR       binary installs (default /usr/local; --rootless: ~/.local)
#   --rootless         per-user install, no root daemon (Linux)
#   --no-start         install and enable the service, do not start it
#   --uninstall        remove nyxd (images and containers are kept)
#   --purge            with --uninstall: also delete images, containers and volumes
#   -y, --yes          do not ask before using sudo
#
# Environment: NYXD_RELEASE_URL overrides where release files are downloaded
# from (a mirror, or a local directory for tests: file:///path).
set -eu

REPO=zrougamed/nyxd
VERSION=""
METHOD=auto
PREFIX=""
ROOTLESS=0
NO_START=0
UNINSTALL=0
PURGE=0
YES=0

say() { printf '%s\n' "nyxd-install: $*" >&2; }
die() {
	say "error: $*"
	exit 1
}
have() { command -v "$1" >/dev/null 2>&1; }

while [ $# -gt 0 ]; do
	case $1 in
	--version) VERSION=${2:?--version needs a value}; shift ;;
	--version=*) VERSION=${1#*=} ;;
	--method) METHOD=${2:?--method needs a value}; shift ;;
	--method=*) METHOD=${1#*=} ;;
	--prefix) PREFIX=${2:?--prefix needs a value}; shift ;;
	--prefix=*) PREFIX=${1#*=} ;;
	--rootless) ROOTLESS=1 ;;
	--no-start) NO_START=1 ;;
	--uninstall) UNINSTALL=1 ;;
	--purge) PURGE=1 ;;
	-y | --yes) YES=1 ;;
	-h | --help)
		sed -n '2,33p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*) die "unknown option $1 (see --help)" ;;
	esac
	shift
done
case $METHOD in auto | package | binary) ;; *) die "--method must be auto, package or binary" ;; esac

OS=$(uname -s)
case $(uname -m) in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "unsupported CPU $(uname -m) (release builds: x86_64, arm64)" ;;
esac

# as_root CMD...: run as root, through sudo when needed.
as_root() {
	if [ "$(id -u)" = 0 ]; then
		"$@"
	elif have sudo; then
		if [ "$YES" = 0 ] && [ -z "${SUDO_ASKED:-}" ]; then
			say "root is needed to install the system service; using sudo"
			SUDO_ASKED=1
		fi
		sudo "$@"
	elif have doas; then
		doas "$@"
	else
		die "run as root, or install sudo (or use --rootless)"
	fi
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

base_url() {
	if [ -n "${NYXD_RELEASE_URL:-}" ]; then
		echo "${NYXD_RELEASE_URL%/}"
	else
		echo "https://github.com/$REPO/releases/download/$VERSION"
	fi
}

fetch() { # fetch URL DEST
	case $1 in
	file://*) cp "${1#file://}" "$2" ;;
	*)
		if have curl; then
			curl -fsSL --retry 3 -o "$2" "$1"
		elif have wget; then
			wget -q -O "$2" "$1"
		else
			die "curl or wget is needed"
		fi
		;;
	esac
}

resolve_version() {
	[ -n "$VERSION" ] && return
	if [ -n "${NYXD_RELEASE_URL:-}" ]; then
		VERSION=$(cat "${NYXD_RELEASE_URL#file://}/VERSION" 2>/dev/null || true)
		[ -n "$VERSION" ] || die "--version is required with NYXD_RELEASE_URL"
		return
	fi
	if have curl; then
		url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
	else
		url=$(wget -S --spider "https://github.com/$REPO/releases/latest" 2>&1 | sed -n 's/^ *Location: //p' | tail -1)
	fi
	VERSION=${url##*/}
	case $VERSION in v*) ;; *) die "could not find the latest release (use --version)" ;; esac
}

sha256() {
	if have sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# download NAME: fetch a release file into $TMP and check it against checksums.txt.
download() {
	[ -f "$TMP/checksums.txt" ] || fetch "$(base_url)/checksums.txt" "$TMP/checksums.txt"
	say "downloading $1 ($VERSION)"
	fetch "$(base_url)/$1" "$TMP/$1" || die "download failed: $(base_url)/$1"
	want=$(awk -v f="$1" '$2 == f || $2 == "*"f {print $1}' "$TMP/checksums.txt")
	[ -n "$want" ] || die "$1 is not listed in checksums.txt"
	[ "$(sha256 "$TMP/$1")" = "$want" ] || die "checksum mismatch for $1"
	if have gpg && fetch "$(base_url)/checksums.txt.sig" "$TMP/checksums.txt.sig" 2>/dev/null &&
		fetch "$(base_url)/nyxd-release-key.asc" "$TMP/key.asc" 2>/dev/null; then
		export GNUPGHOME="$TMP/gnupg"
		mkdir -m 700 "$GNUPGHOME"
		gpg --batch --quiet --import "$TMP/key.asc" 2>/dev/null || die "cannot import the release key"
		gpg --batch --quiet --verify "$TMP/checksums.txt.sig" "$TMP/checksums.txt" 2>/dev/null ||
			die "checksums.txt signature does not verify"
		say "signature ok"
	fi
}

# pkg_manager prints the native package format this system installs.
pkg_manager() {
	if have dpkg && have apt-get; then echo deb
	elif have rpm && { have dnf || have yum || have zypper; }; then echo rpm
	elif have apk; then echo apk
	elif have pacman; then echo archlinux
	else echo none
	fi
}

package_file() { # package_file FORMAT
	v=${VERSION#v}
	case $1 in
	deb) echo "nyxd_${v}_${ARCH}.deb" ;;
	rpm) [ "$ARCH" = amd64 ] && echo "nyxd-${v}-1.x86_64.rpm" || echo "nyxd-${v}-1.aarch64.rpm" ;;
	apk) [ "$ARCH" = amd64 ] && echo "nyxd_${v}_x86_64.apk" || echo "nyxd_${v}_aarch64.apk" ;;
	archlinux) [ "$ARCH" = amd64 ] && echo "nyxd-${v}-1-x86_64.pkg.tar.zst" || echo "nyxd-${v}-1-aarch64.pkg.tar.zst" ;;
	esac
}

install_package() { # install_package FORMAT
	f=$(package_file "$1")
	download "$f"
	case $1 in
	deb)
		as_root env NYXD_NO_START="$NO_START" DEBIAN_FRONTEND=noninteractive apt-get install -y "$TMP/$f"
		;;
	rpm)
		if have dnf; then as_root env NYXD_NO_START="$NO_START" dnf install -y "$TMP/$f"
		elif have zypper; then as_root env NYXD_NO_START="$NO_START" zypper --non-interactive install --allow-unsigned-rpm "$TMP/$f"
		else as_root env NYXD_NO_START="$NO_START" yum install -y "$TMP/$f"
		fi
		;;
	# The checksum above was checked against the signed checksums.txt.
	apk) as_root env NYXD_NO_START="$NO_START" apk add --allow-untrusted "$TMP/$f" ;;
	archlinux) as_root env NYXD_NO_START="$NO_START" pacman -U --noconfirm "$TMP/$f" ;;
	esac
}

ensure_crun() {
	have crun && return
	say "crun (the OCI runtime nyxd uses) is not installed; installing it"
	case $(pkg_manager) in
	deb) as_root apt-get install -y crun ;;
	rpm) if have dnf; then as_root dnf install -y crun; elif have zypper; then as_root zypper --non-interactive install crun; else as_root yum install -y crun; fi ;;
	apk) as_root apk add crun ;;
	archlinux) as_root pacman -S --noconfirm crun ;;
	*) say "warning: install crun yourself (https://github.com/containers/crun/releases)" ;;
	esac
}

# Linux binary install: nyxd, nyx, the service helper and its templates.
install_binaries() {
	p=${PREFIX:-/usr/local}
	a="nyxd_${VERSION#v}_linux_${ARCH}.tar.gz"
	download "$a"
	mkdir "$TMP/x" && tar -xzf "$TMP/$a" -C "$TMP/x"
	as_root install -d "$p/bin" "$p/lib/nyxd" "$p/share/nyxd/init/runit"
	as_root install -m 0755 "$TMP/x/nyxd" "$TMP/x/nyx" "$p/bin/"
	as_root install -m 0755 "$TMP/x/packaging/linux/nyxd-service" "$p/lib/nyxd/nyxd-service"
	for f in nyxd.service nyxd-user.service nyxd.openrc nyxd.sysv nyxd.default; do
		as_root install -m 0644 "$TMP/x/packaging/linux/$f" "$p/share/nyxd/init/$f"
	done
	as_root install -m 0755 "$TMP/x/packaging/linux/runit/run" "$p/share/nyxd/init/runit/run"
	ensure_crun
	if ! getent group nyx >/dev/null 2>&1; then
		if have groupadd; then as_root groupadd --system nyx; else as_root addgroup -S nyx; fi
	fi
	as_root install -d -m 0711 /var/lib/nyxd
	as_root env NYXD_NO_START="$NO_START" NYXD_BINDIR="$p/bin" NYXD_SHARE="$p/share/nyxd" "$p/lib/nyxd/nyxd-service" setup ||
		die "nyxd is installed, but its service could not be set up: $p/lib/nyxd/nyxd-service status"
}

install_rootless() {
	p=${PREFIX:-$HOME/.local}
	a="nyxd_${VERSION#v}_linux_${ARCH}.tar.gz"
	download "$a"
	mkdir "$TMP/x" && tar -xzf "$TMP/$a" -C "$TMP/x"
	install -d "$p/bin"
	install -m 0755 "$TMP/x/nyxd" "$TMP/x/nyx" "$p/bin/"
	have crun || say "warning: crun is not installed; ask an admin to install it (package \"crun\")"
	have newuidmap || say "warning: newuidmap is missing (package uidmap / shadow-utils): nyxd falls back to a single mapped ID"
	grep -q "^$(id -un):" /etc/subuid 2>/dev/null || say "warning: no /etc/subuid entry for $(id -un); see docs/rootless.md"
	if [ -d /run/systemd/system ] && systemctl --user show-environment >/dev/null 2>&1; then
		unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/nyxd.service"
		install -d "$(dirname "$unit")"
		sed "s#/usr/bin/nyxd#$p/bin/nyxd#g" "$TMP/x/packaging/linux/nyxd-user.service" >"$unit"
		systemctl --user daemon-reload
		if [ "$NO_START" = 1 ]; then
			systemctl --user enable nyxd.service
		else
			systemctl --user enable --now nyxd.service
		fi
		say "rootless nyxd: systemd user service enabled ($unit)"
		say "keep it running after logout: loginctl enable-linger $(id -un)"
	else
		say "no systemd user session: start nyxd yourself with: $p/bin/nyxd &"
	fi
	case ":$PATH:" in *":$p/bin:"*) ;; *) say "add $p/bin to your PATH" ;; esac
}

install_macos() {
	p=${PREFIX:-/usr/local}
	f="nyx-darwin-${ARCH}"
	download "$f"
	if [ -w "$p/bin" ] || { [ ! -e "$p/bin" ] && [ -w "$p" ]; }; then
		install -d "$p/bin" && install -m 0755 "$TMP/$f" "$p/bin/nyx"
	else
		as_root install -d "$p/bin" && as_root install -m 0755 "$TMP/$f" "$p/bin/nyx"
	fi
	say "installed $p/bin/nyx"
	say "nyxd runs on Linux: point nyx at one with NYXD_SOCKET or -socket (a VM, a server over ssh -L)."
}

uninstall() {
	if [ "$OS" = Darwin ]; then
		p=${PREFIX:-/usr/local}
		if [ -w "$p/bin" ]; then rm -f "$p/bin/nyx"; else as_root rm -f "$p/bin/nyx"; fi
		say "removed $p/bin/nyx"
		return
	fi
	if [ "$ROOTLESS" = 1 ]; then
		p=${PREFIX:-$HOME/.local}
		systemctl --user disable --now nyxd.service >/dev/null 2>&1 || true
		rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/nyxd.service" "$p/bin/nyxd" "$p/bin/nyx"
		systemctl --user daemon-reload >/dev/null 2>&1 || true
		[ "$PURGE" = 1 ] && rm -rf "${XDG_DATA_HOME:-$HOME/.local/share}/nyxd"
		say "removed the rootless install"
		return
	fi
	removed=0
	case $(pkg_manager) in
	deb) if dpkg -s nyxd >/dev/null 2>&1; then as_root env DEBIAN_FRONTEND=noninteractive apt-get remove -y nyxd && removed=1; fi ;;
	rpm) if rpm -q nyxd >/dev/null 2>&1; then
		if have dnf; then as_root dnf remove -y nyxd; elif have zypper; then as_root zypper --non-interactive remove nyxd; else as_root yum remove -y nyxd; fi
		removed=1
	fi ;;
	apk) if apk info -e nyxd >/dev/null 2>&1; then as_root apk del nyxd && removed=1; fi ;;
	archlinux) if pacman -Q nyxd >/dev/null 2>&1; then as_root pacman -R --noconfirm nyxd && removed=1; fi ;;
	esac
	p=${PREFIX:-/usr/local}
	if [ -x "$p/lib/nyxd/nyxd-service" ]; then
		as_root env NYXD_BINDIR="$p/bin" NYXD_SHARE="$p/share/nyxd" "$p/lib/nyxd/nyxd-service" teardown || true
		as_root rm -rf "$p/bin/nyxd" "$p/bin/nyx" "$p/lib/nyxd" "$p/share/nyxd"
		removed=1
	fi
	[ "$removed" = 1 ] || say "nyxd does not look installed"
	if [ "$PURGE" = 1 ]; then
		as_root rm -rf /var/lib/nyxd /etc/default/nyxd
		say "deleted /var/lib/nyxd (images, containers, volumes)"
	else
		say "kept /var/lib/nyxd (images, containers, volumes); --purge deletes it"
	fi
}

main() {
	if [ "$UNINSTALL" = 1 ]; then
		uninstall
		return
	fi
	resolve_version
	case $OS in
	Darwin) install_macos ;;
	Linux)
		if [ "$ROOTLESS" = 1 ]; then
			install_rootless
		else
			fmt=$(pkg_manager)
			if [ "$METHOD" = package ] && [ "$fmt" = none ]; then die "no supported package manager found (use --method binary)"; fi
			if [ "$METHOD" != binary ] && [ "$fmt" != none ]; then
				install_package "$fmt"
			else
				install_binaries
			fi
		fi
		;;
	*) die "unsupported OS $OS: on Windows use install.ps1" ;;
	esac
	say "nyxd $VERSION installed"
}

main
