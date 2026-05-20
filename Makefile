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

.PHONY: all build build-nyx lint vet clean install install-usr scan scan-trivy scan-grype

BINDIR ?= /usr/local/bin

all: build build-nyx

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PACKAGE)

build-nyx:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(NYX) $(NYX_PKG)

# Static Linux binary (fully linked; useful for minimal rootfs)
build-static:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags "$(LDFLAGS) -extldflags '-static'" \
		-o bin/$(BINARY)-static $(PACKAGE)

# Cross-compile for Linux arm64 (e.g. Raspberry Pi 64-bit, other aarch64 hosts)
build-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -trimpath -ldflags "$(LDFLAGS)" \
		-o bin/$(BINARY)-arm64 $(PACKAGE)

vet:
	go vet ./...

lint:
	golangci-lint run ./...

test:
	go test -race -count=1 ./...

# Vulnerability scans (install Trivy + Grype; see docs/INSTALL.md)
scan: scan-trivy scan-grype

scan-trivy:
	@command -v trivy >/dev/null 2>&1 || { echo "install trivy: https://aquasecurity.github.io/trivy/latest/getting-started/installation/"; exit 1; }
	trivy fs --scanners vuln,secret,misconfig --severity HIGH,CRITICAL --exit-code 0 .

scan-grype:
	@command -v grype >/dev/null 2>&1 || { echo "install grype: https://github.com/anchore/grype#installation"; exit 1; }
	grype dir:. --fail-on high

clean:
	rm -rf bin/

install: build build-nyx
	sudo install -m 755 bin/$(BINARY) $(BINDIR)/$(BINARY)
	sudo install -m 755 bin/$(NYX) $(BINDIR)/$(NYX)

# Same as install with BINDIR=/usr/bin (system-wide PATH)
install-usr:
	$(MAKE) install BINDIR=/usr/bin

# Install crun from distro or build from source
install-crun:
	apt-get install -y crun || \
	(cd /tmp && git clone https://github.com/containers/crun && cd crun && \
	./autogen.sh && ./configure && make && install -m 755 crun /usr/local/bin/crun)

# Install minimal CNI plugins
install-cni:
	mkdir -p /opt/cni/bin
	cd /tmp && \
	ARCH=$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && \
	curl -sSL https://github.com/containernetworking/plugins/releases/latest/download/cni-plugins-linux-$$ARCH-v1.5.1.tgz \
	| tar -xz -C /opt/cni/bin

run: build
	./bin/$(BINARY) --log-level debug

# Install kernel modules for nyxd
setup-kernel:
	@echo "Loading kernel modules..."
	modprobe overlay bridge veth br_netfilter \
		ip_tables iptable_nat iptable_filter \
		nf_nat nf_conntrack nft_masq nft_nat nft_chain_nat
	@echo "Applying sysctls..."
	sysctl -p /etc/sysctl.d/99-nyxd.conf || \
		sysctl net.ipv4.ip_forward=1 net.bridge.bridge-nf-call-iptables=1
	@echo "Kernel ready."

# Run the pre-flight check
preflight:
	@bash docs/preflight.sh

# Install with native network (no CNI binaries needed)
install-native: build
	install -m 755 bin/$(BINARY) /usr/local/bin/$(BINARY)
	install -m 644 nyxd.service /etc/systemd/system/
	install -m 644 docs/kernel-requirements.md /etc/nyxd/
	cp -n nyx-compose.example.yaml /etc/nyxd/nyx-compose.yaml 2>/dev/null || true
	systemctl daemon-reload
	@echo ""
	@echo "nyxd installed. Run 'make setup-kernel' then 'systemctl enable --now nyxd'."
	@echo "No CNI binaries needed — native network plugin is built-in."
