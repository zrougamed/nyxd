# Pinned third-party files shared by the WSL distro and the VM image builds
# (sourced). The crun files were checked against the crun release GPG
# signatures (Giuseppe Scrivano, key AC40 4C1C 0BF7 35C6 3FF4 D562 263D 6DF2 E163 E1EA).
# shellcheck shell=sh disable=SC2034

# Mozilla CA certificates as extracted by curl (https://curl.se/docs/caextract.html);
# nyxd needs them for registry pulls/pushes and ADD <url>. Pinned by date and sha256.
CACERT_DATE=2026-09-25
CACERT_SHA256=a41b5d356aea97a529fe27e0f7316d2f9d946d75927476cf9cf1b90637d00505

CRUN_VERSION=1.30.1
CRUN_SHA256_amd64=e7b059390e27d0f283e1debbe89d9c5847870f3d8325f31ccd954533fdd6ea78
CRUN_SHA256_arm64=6ac05ea41343cf91b12832c69f9a2a8c6dc5380032f4c0e02a46cc6aba8804f5

# Alpine release the VM image is built from.
ALPINE_VERSION=3.20
