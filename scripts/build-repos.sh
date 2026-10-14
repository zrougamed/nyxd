#!/bin/sh
# Build the signed package repositories served at pkgs.nyxd.io from release
# packages (the .deb, .rpm, .apk and .pkg.tar.zst files of one or more
# releases, with the pacman .sig files next to them):
#
#   scripts/build-repos.sh PKGDIR OUTDIR
#
#   OUTDIR/apt/      dists/stable/{InRelease,Release,Release.gpg,main/binary-*/}, pool/
#   OUTDIR/rpm/      <arch>/ with repodata/ (repomd.xml signed), nyxd.repo
#   OUTDIR/alpine/   <arch>/APKINDEX.tar.gz (RSA-signed), nyxd.rsa.pub
#   OUTDIR/arch/     <arch>/nyxd.db (+ .sig) with the packages and their .sig
#   OUTDIR/nyxd.asc, OUTDIR/nyxd.gpg   the release key (armored, binary)
#
# Indexes are built in containers (Debian, Fedora, Alpine, Arch); signatures
# are made here with gpg: GPG_FINGERPRINT selects the key (GPG_PASSPHRASE if
# it has one). APK_KEY_FILE is the RSA key that signs the Alpine index; without
# it the Alpine repository is left out. ENGINE=podman to use podman.
set -eu
PKGDIR=$(cd "${1:?package directory}" && pwd)
OUT=${2:?output directory}
: "${GPG_FINGERPRINT:?GPG_FINGERPRINT selects the signing key}"
ENGINE=${ENGINE:-docker}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
uid="$(id -u):$(id -g)"

# ctr IMAGE SCRIPT: run SCRIPT in IMAGE with the packages at /pkgs and the
# output at /out, files owned by the caller afterwards.
ctr() {
	$ENGINE run --rm -v "$PKGDIR:/pkgs:ro" -v "$OUT:/out" "$1" sh -euc "$2; chown -R $uid /out"
}

gpgsign() { # gpgsign ARGS...: gpg with the release key
	if [ -n "${GPG_PASSPHRASE:-}" ]; then
		printf '%s' "$GPG_PASSPHRASE" | gpg --batch --yes --pinentry-mode=loopback --passphrase-fd=0 \
			--local-user="$GPG_FINGERPRINT" "$@"
	else
		gpg --batch --yes --local-user="$GPG_FINGERPRINT" "$@"
	fi
}

gpg --armor --export "$GPG_FINGERPRINT" >"$OUT/nyxd.asc"
gpg --export "$GPG_FINGERPRINT" >"$OUT/nyxd.gpg"

# --- apt ----------------------------------------------------------------------
rm -rf "$OUT/apt"
mkdir -p "$OUT/apt/pool/main/n/nyxd"
cp "$PKGDIR"/*.deb "$OUT/apt/pool/main/n/nyxd/"
ctr docker.io/library/debian:12 '
	apt-get update -qq >/dev/null && apt-get install -y -qq apt-utils >/dev/null
	cd /out/apt
	for a in amd64 arm64; do
		d=dists/stable/main/binary-$a
		mkdir -p $d
		apt-ftparchive --arch $a packages pool >$d/Packages
		gzip -9kf $d/Packages
	done
	apt-ftparchive -o APT::FTPArchive::Release::Origin=nyxd -o APT::FTPArchive::Release::Label=nyxd \
		-o APT::FTPArchive::Release::Suite=stable -o APT::FTPArchive::Release::Codename=stable \
		-o APT::FTPArchive::Release::Architectures="amd64 arm64" -o APT::FTPArchive::Release::Components=main \
		release dists/stable >dists/stable/Release'
gpgsign --clearsign --output "$OUT/apt/dists/stable/InRelease" "$OUT/apt/dists/stable/Release"
gpgsign --armor --detach-sign --output "$OUT/apt/dists/stable/Release.gpg" "$OUT/apt/dists/stable/Release"

# --- rpm (dnf, yum, zypper) -------------------------------------------------------
rm -rf "$OUT/rpm"
for a in x86_64 aarch64; do
	mkdir -p "$OUT/rpm/$a"
	cp "$PKGDIR"/*."$a".rpm "$OUT/rpm/$a/"
done
ctr registry.fedoraproject.org/fedora:41 '
	dnf install -y -q createrepo_c >/dev/null
	for a in x86_64 aarch64; do createrepo_c -q /out/rpm/$a; done'
for a in x86_64 aarch64; do
	gpgsign --armor --detach-sign --output "$OUT/rpm/$a/repodata/repomd.xml.asc" "$OUT/rpm/$a/repodata/repomd.xml"
done
cat >"$OUT/rpm/nyxd.repo" <<'EOF'
[nyxd]
name=nyxd
baseurl=https://pkgs.nyxd.io/rpm/$basearch
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://pkgs.nyxd.io/nyxd.asc
EOF

# --- alpine -----------------------------------------------------------------------
rm -rf "$OUT/alpine"
if [ -n "${APK_KEY_FILE:-}" ]; then
	mkdir -p "$OUT/alpine"
	openssl rsa -in "$APK_KEY_FILE" -pubout -out "$OUT/alpine/nyxd.rsa.pub" 2>/dev/null
	keydir=$(mktemp -d)
	cp "$APK_KEY_FILE" "$keydir/nyxd.rsa"
	cp "$OUT/alpine/nyxd.rsa.pub" "$keydir/nyxd.rsa.pub"
	# apk fetches <pkgname>-<pkgver>.apk: name the files after their .PKGINFO.
	$ENGINE run --rm -v "$PKGDIR:/pkgs:ro" -v "$OUT:/out" -v "$keydir:/keys:ro" docker.io/library/alpine:3.20 sh -euc "
		apk add -q abuild >/dev/null
		cp /keys/nyxd.rsa.pub /etc/apk/keys/
		for a in x86_64 aarch64; do
			mkdir -p /out/alpine/\$a
			for f in /pkgs/*_\$a.apk; do
				v=\$(tar -xzOf \$f .PKGINFO 2>/dev/null | sed -n 's/^pkgver = //p')
				cp \$f /out/alpine/\$a/nyxd-\$v.apk
			done
			cd /out/alpine/\$a
			apk index -q --allow-untrusted --rewrite-arch \$a -o APKINDEX.tar.gz -d 'nyxd packages' *.apk
			abuild-sign -q -k /keys/nyxd.rsa -p nyxd.rsa.pub APKINDEX.tar.gz
		done
		chown -R $uid /out"
	rm -rf "$keydir"
else
	echo "build-repos: APK_KEY_FILE not set: no Alpine repository" >&2
fi

# --- pacman -------------------------------------------------------------------------
rm -rf "$OUT/arch"
for a in x86_64 aarch64; do
	mkdir -p "$OUT/arch/$a"
	cp "$PKGDIR"/*-"$a".pkg.tar.zst "$OUT/arch/$a/"
	cp "$PKGDIR"/*-"$a".pkg.tar.zst.sig "$OUT/arch/$a/" 2>/dev/null ||
		for f in "$OUT/arch/$a"/*.pkg.tar.zst; do gpgsign --detach-sign --output "$f.sig" "$f"; done
done
ctr docker.io/library/archlinux:latest '
	for a in x86_64 aarch64; do
		cd /out/arch/$a
		repo-add -q nyxd.db.tar.gz *.pkg.tar.zst
		rm -f nyxd.db nyxd.files
		cp nyxd.db.tar.gz nyxd.db
		cp nyxd.files.tar.gz nyxd.files
	done'
for a in x86_64 aarch64; do
	gpgsign --detach-sign --output "$OUT/arch/$a/nyxd.db.sig" "$OUT/arch/$a/nyxd.db"
	cp "$OUT/arch/$a/nyxd.db.sig" "$OUT/arch/$a/nyxd.db.tar.gz.sig"
done

echo "build-repos: repositories in $OUT"
