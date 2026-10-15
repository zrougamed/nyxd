# nyxd Makefile
BINARY   := nyxd
NYX      := nyx
PACKAGE  := github.com/zrougamed/nyxd/cmd/nyxd
NYX_PKG  := github.com/zrougamed/nyxd/cmd/nyx
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE     := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -w -s \
	-X main.version=$(VERSION) \
	-X main.gitCommit=$(COMMIT) \
	-X main.buildDate=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help all build build-nyx build-windows wsl-rootfs vm-image test-vm snapshot lint vet test test-overlay test-rootless test-build test-transfer test-wsl test-repos test-stress coverage clean install install-usr scan scan-trivy scan-grype

BINDIR ?= /usr/local/bin

help: ## List the targets
	@awk 'BEGIN {FS = ":[^#]*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: build build-nyx ## Build nyxd and nyx into bin/

build: ## Build the nyxd daemon
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PACKAGE)

build-nyx: ## Build the nyx CLI
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(NYX) $(NYX_PKG)

# Static Linux binary (fully linked; useful for minimal rootfs)
build-static: ## Build a fully static linux/amd64 nyxd
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags "$(LDFLAGS) -extldflags '-static'" \
		-o bin/$(BINARY)-static $(PACKAGE)

# Cross-compile for Linux arm64 (e.g. Raspberry Pi 64-bit, other aarch64 hosts)
build-arm64: ## Cross-compile nyxd for linux/arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -trimpath -ldflags "$(LDFLAGS)" \
		-o bin/$(BINARY)-arm64 $(PACKAGE)

# nyx.exe for Windows (amd64 + arm64); talks to nyxd in the nyx WSL2 distro.
build-windows: ## Build nyx.exe for Windows (amd64, arm64)
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=windows GOARCH=$$arch \
			go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(NYX)-windows-$$arch.exe $(NYX_PKG) || exit 1; \
	done

# WSL2 distro tarball (static nyxd + pinned crun, no shell): bin/nyx-wsl-<arch>.tar.gz.
# WSL_ARCH=arm64 for Windows on ARM.
WSL_ARCH ?= amd64
wsl-rootfs: ## Build the WSL2 distro tarball (WSL_ARCH=amd64|arm64)
	scripts/build-wsl-rootfs.sh $(WSL_ARCH) bin $(VERSION)

# macOS VM image (Alpine + OpenRC + static nyxd and crun, booted by vfkit):
# bin/nyx-vm-<arch>.tar.gz. VM_ARCH=amd64 for Intel Macs; arm64 on a Linux
# amd64 host needs binfmt emulation. test-vm boots it under QEMU/KVM.
VM_ARCH ?= arm64
vm-image: ## Build the macOS VM image (VM_ARCH=arm64|amd64)
	scripts/build-vm-image.sh $(VM_ARCH) bin $(VERSION)

test-vm: build-nyx ## Build the amd64 VM image and boot it under QEMU/KVM
	scripts/build-vm-image.sh amd64 bin $(VERSION)
	scripts/vm-smoke.sh bin/nyx-vm-amd64.tar.gz

# Every release archive and Linux package (deb, rpm, apk, Arch) into dist/,
# unsigned, without publishing. Needs goreleaser v2; see docs/packaging.md.
snapshot: ## Build every release archive and package into dist/
	goreleaser release --snapshot --clean --skip=publish,sign

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

test: ## Run unit tests with the race detector
	go test -race -count=1 ./...

# Rootless end-to-end: daemon as current user, pull, run, exec, stop.
test-rootless: build build-nyx ## Rootless end-to-end smoke test
	scripts/rootless-smoke.sh

# Image build end-to-end against a rootless daemon.
test-build: build build-nyx ## Image build end-to-end test
	scripts/build-smoke.sh

# Image transfer end-to-end: registry:2 via nyx, push / pull / save / load.
test-transfer: build build-nyx ## Image push/pull/save/load end-to-end test
	scripts/transfer-smoke.sh

# nyx machine + the WSL distro, end to end on Linux: nyx drives a fake wsl.exe
# that boots the distro tarball in its own namespaces (see scripts/fake-wsl.sh).
test-wsl: build-nyx wsl-rootfs ## nyx machine + WSL distro end-to-end test
	scripts/wsl-smoke.sh

# The one-line install from the package repositories (pkgs.nyxd.io layout,
# throwaway keys, local server) in a container per distribution; 60s each.
test-repos: ## Package repository install test per distribution
	scripts/repo-smoke.sh

# Load and failure test: many containers, churn, log flood, OOM kill, daemon
# crash. Run with sudo for every scenario (the OOM one needs cgroup limits).
test-stress: build build-nyx ## Load and failure test (sudo for every scenario)
	scripts/stress.sh

# Statement coverage of the module: unit tests + every e2e script, merged
# (report in coverage/; COVER_MIN=N fails below N%).
coverage: ## Merged unit + e2e coverage report (COVER_MIN=N)
	E2E=1 scripts/coverage.sh

# Real overlayfs mount test, run unprivileged inside a user + mount namespace.
test-overlay: ## Real overlayfs mount test in a user namespace
	unshare -Urm env NYXD_OVERLAY_IT=1 go test -count=1 -run OverlayMount -v ./internal/layer

# Vulnerability scans (install Trivy + Grype; see docs/INSTALL.md)
scan: scan-trivy scan-grype ## Run Trivy and Grype vulnerability scans

scan-trivy: ## Trivy filesystem scan
	@command -v trivy >/dev/null 2>&1 || { echo "install trivy: https://aquasecurity.github.io/trivy/latest/getting-started/installation/"; exit 1; }
	trivy fs --scanners vuln,secret,misconfig --severity HIGH,CRITICAL --exit-code 0 .

scan-grype: ## Grype dependency scan
	@command -v grype >/dev/null 2>&1 || { echo "install grype: https://github.com/anchore/grype#installation"; exit 1; }
	grype dir:. --fail-on high

clean: ## Remove bin/
	rm -rf bin/

install: build build-nyx ## Install nyxd and nyx into BINDIR (default /usr/local/bin)
	sudo install -m 755 bin/$(BINARY) $(BINDIR)/$(BINARY)
	sudo install -m 755 bin/$(NYX) $(BINDIR)/$(NYX)

# Same as install with BINDIR=/usr/bin (system-wide PATH)
install-usr: ## Install into /usr/bin
	$(MAKE) install BINDIR=/usr/bin

# Install crun from distro or build from source
install-crun: ## Install crun from the distro or from source
	apt-get install -y crun || \
	(cd /tmp && git clone https://github.com/containers/crun && cd crun && \
	./autogen.sh && ./configure && make && install -m 755 crun /usr/local/bin/crun)

# Install minimal CNI plugins
install-cni: ## Install the CNI plugins into /opt/cni/bin
	mkdir -p /opt/cni/bin
	cd /tmp && \
	ARCH=$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && \
	curl -sSL https://github.com/containernetworking/plugins/releases/latest/download/cni-plugins-linux-$$ARCH-v1.5.1.tgz \
	| tar -xz -C /opt/cni/bin

run: build ## Build and run nyxd in the foreground with debug logs
	./bin/$(BINARY) --log-level debug

# Install kernel modules for nyxd
setup-kernel: ## Load kernel modules and apply sysctls
	@echo "Loading kernel modules..."
	modprobe overlay bridge veth br_netfilter \
		ip_tables iptable_nat iptable_filter \
		nf_nat nf_conntrack nft_masq nft_nat nft_chain_nat
	@echo "Applying sysctls..."
	sysctl -p /etc/sysctl.d/99-nyxd.conf || \
		sysctl net.ipv4.ip_forward=1 net.bridge.bridge-nf-call-iptables=1
	@echo "Kernel ready."

# Run the pre-flight check
preflight: ## Check the host is ready for nyxd
	@bash docs/preflight.sh

# Install with native network (no CNI binaries needed)
install-native: build build-nyx ## Install nyxd as a service with native networking
	install -d /usr/local/bin /usr/local/lib/nyxd /usr/local/share/nyxd/init/runit
	install -m 755 bin/$(BINARY) bin/$(NYX) /usr/local/bin/
	install -m 755 packaging/linux/nyxd-service /usr/local/lib/nyxd/
	install -m 644 packaging/linux/nyxd.service packaging/linux/nyxd-user.service \
		packaging/linux/nyxd.default /usr/local/share/nyxd/init/
	install -m 755 packaging/linux/nyxd.openrc packaging/linux/nyxd.sysv /usr/local/share/nyxd/init/
	install -m 755 packaging/linux/runit/run /usr/local/share/nyxd/init/runit/
	getent group nyx >/dev/null || groupadd --system nyx || addgroup -S nyx
	/usr/local/lib/nyxd/nyxd-service setup
	@echo ""
	@echo "nyxd installed and running as a service (systemd, OpenRC, runit or SysV)."
	@echo "Native networking is built in; run 'make setup-kernel' if bridges fail."
