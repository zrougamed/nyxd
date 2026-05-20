# QEMU + Alpine 3.23 for nyxd hacking

Use a small **Alpine Linux 3.23** VM when you want to try **nyxd** on your laptop (x86_64) or exercise **aarch64** under QEMU before deploying to bare metal or an ARM board (e.g. a Raspberry Pi).

## Why Alpine virt ISO?

The **virt** flavour is small (~67 MiB), ships a kernel that works well under QEMU, and includes serial console support—handy for headless bring-up.

## x86_64 (typical laptop / CI host)

1. Download the latest **3.23.x** virt ISO from the [Alpine 3.23 x86_64 release directory](https://dl-cdn.alpinelinux.org/alpine/v3.23/releases/x86_64/) (pick the current `alpine-virt-3.23.*-x86_64.iso`).

2. Run the helper script (see [scripts/qemu-alpine-nyxd.sh](https://github.com/zrougamed/nyxd/blob/main/scripts/qemu-alpine-nyxd.sh)) or manually:

```bash
qemu-system-x86_64 \
  -machine accel=kvm:tcg \
  -m 2048 -smp 2 \
  -netdev user,id=n0,hostfwd=tcp::5555-:22 \
  -device virtio-net-pci,netdev=n0 \
  -drive file=./alpine-virt-3.23.4-x86_64.iso,media=cdrom,if=virtio \
  -serial mon:stdio
```

Adjust ISO path, `-machine accel=tcg` if KVM is unavailable (e.g. macOS without hvf), and **hostfwd** if you want SSH port forwarding.

3. Inside Alpine, install Go, crun, git, build **nyxd** from this repo, and run the daemon as root (same flow as [Install](/docs/getting-started/install)).

## aarch64 (ARM64 guest on a PC)

Use **`qemu-system-aarch64`** with a **virt** machine and a **3.23** aarch64 ISO from [releases/aarch64](https://dl-cdn.alpinelinux.org/alpine/v3.23/releases/aarch64/). You will need a firmware blob (e.g. **edk2** UEFI package for QEMU) depending on your distro—consult your OS QEMU docs.

## Scripted helper

```bash
chmod +x scripts/qemu-alpine-nyxd.sh
./scripts/qemu-alpine-nyxd.sh              # x86_64, downloads 3.23.4 virt ISO if missing
ARCH=aarch64 ./scripts/qemu-alpine-nyxd.sh /path/to/alpine-virt-3.23.x-aarch64.iso
```

Environment variables are documented at the top of the script.
