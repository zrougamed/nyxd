#!/usr/bin/env bash
# Boot Alpine Linux 3.23 (virt ISO) under QEMU for nyxd development.
# Typical: ./scripts/qemu-alpine-nyxd.sh
# Or pass a local ISO path: ./scripts/qemu-alpine-nyxd.sh ~/Downloads/alpine-virt-3.23.4-x86_64.iso
set -euo pipefail

ARCH="${ARCH:-$(uname -m)}"
# Default patch release for Alpine v3.23 (bump if directory listing moves).
ALPINE_PATCH="${ALPINE_PATCH:-3.23.4}"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}/nyxd-qemu"
ISO_URL_X86="https://dl-cdn.alpinelinux.org/alpine/v3.23/releases/x86_64/alpine-virt-${ALPINE_PATCH}-x86_64.iso"
ISO_URL_AARCH64="https://dl-cdn.alpinelinux.org/alpine/v3.23/releases/aarch64/alpine-virt-${ALPINE_PATCH}-aarch64.iso"

mkdir -p "$CACHE"

pick_iso() {
  if [[ -n "${1:-}" ]]; then
    echo "$1"
    return
  fi
  case "$ARCH" in
    x86_64|amd64)
      echo "$CACHE/alpine-virt-${ALPINE_PATCH}-x86_64.iso"
      ;;
    aarch64|arm64)
      echo "$CACHE/alpine-virt-${ALPINE_PATCH}-aarch64.iso"
      ;;
    *)
      echo "Unsupported ARCH=$ARCH (set ARCH=x86_64 or aarch64, or pass an ISO path)" >&2
      exit 1
      ;;
  esac
}

ISO="$(pick_iso "${1:-}")"

if [[ ! -f "$ISO" ]]; then
  case "$ARCH" in
    x86_64|amd64) url="$ISO_URL_X86" ;;
    aarch64|arm64) url="$ISO_URL_AARCH64" ;;
  esac
  echo "Downloading: $url" >&2
  curl -fL --retry 3 -o "$ISO.partial" "$url"
  mv "$ISO.partial" "$ISO"
fi

MEM="${QEMU_MEM:-2048}"
SMP="${QEMU_SMP:-2}"
FWD="${QEMU_SSH_FWD:-5555}"

if [[ "$ARCH" == "x86_64" || "$ARCH" == "amd64" ]]; then
  accel="${QEMU_ACCEL:-kvm:tcg}"
  exec qemu-system-x86_64 \
    -machine "accel=$accel" \
    -m "$MEM" -smp "$SMP" \
    -netdev "user,id=n0,hostfwd=tcp::${FWD}-:22" \
    -device virtio-net-pci,netdev=n0 \
    -drive "file=$ISO,media=cdrom,if=virtio" \
    -serial mon:stdio
fi

if [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
  echo "aarch64: pass your own QEMU machine/firmware flags if this default fails on your host." >&2
  exec qemu-system-aarch64 \
    -machine virt \
    -cpu cortex-a72 -m "$MEM" -smp "$SMP" \
    -netdev "user,id=n0,hostfwd=tcp::${FWD}-:22" \
    -device virtio-net-device,netdev=n0 \
    -drive "file=$ISO,media=cdrom,if=virtio" \
    -serial mon:stdio
fi
