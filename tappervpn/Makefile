IMAGE_REPO ?= tappervpn/tappervpn
SYNO_ARCH ?= "x86_64"
SYNO_DSM ?= "7"
TAGS ?= "latest"

PLATFORM ?= "flyio" ## flyio==linux/amd64. Set to "" to build all platforms.

vet: ## Run go vet
	./tool/go vet ./...

tidy: ## Run go mod tidy and update nix flake hashes
	./tool/go mod tidy
	./tool/go run ./tool/updateflakes

lint: ## Run golangci-lint
	./tool/go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint run

updatedeps: ## Update depaware deps
	# depaware (via x/tools/go/packages) shells back to "go", so make sure the "go"
	# it finds in its $$PATH is the right one.
	PATH="$$(./tool/go env GOROOT)/bin:$$PATH" ./tool/go run github.com/tailscale/depaware --update --vendor --internal \
		tappervpn.com/cmd/tappervpnd \
		tappervpn.com/cmd/tappervpn \
		tappervpn.com/cmd/derper \
		tappervpn.com/cmd/k8s-operator \
		tappervpn.com/cmd/stund \
		tappervpn.com/cmd/tsidp
	PATH="$$(./tool/go env GOROOT)/bin:$$PATH" ./tool/go run github.com/tailscale/depaware --update --goos=linux,darwin,windows,android,ios --vendor --internal \
		tappervpn.com/tsnet
	PATH="$$(./tool/go env GOROOT)/bin:$$PATH" ./tool/go run github.com/tailscale/depaware --update --file=depaware-minbox.txt --goos=linux --tags="$$(./tool/go run ./cmd/featuretags --min --add=cli)" --vendor --internal \
		tappervpn.com/cmd/tappervpnd
	PATH="$$(./tool/go env GOROOT)/bin:$$PATH" ./tool/go run github.com/tailscale/depaware --update --file=depaware-min.txt --goos=linux --tags="$$(./tool/go run ./cmd/featuretags --min)" --vendor --internal \
		tappervpn.com/cmd/tappervpnd

depaware: ## Run depaware checks
	# depaware (via x/tools/go/packages) shells back to "go", so make sure the "go"
	# it finds in its $$PATH is the right one.
	PATH="$$(./tool/go env GOROOT)/bin:$$PATH" ./tool/go run github.com/tailscale/depaware --check --vendor --internal \
		tappervpn.com/cmd/tappervpnd \
		tappervpn.com/cmd/tappervpn \
		tappervpn.com/cmd/derper \
		tappervpn.com/cmd/k8s-operator \
		tappervpn.com/cmd/stund \
		tappervpn.com/cmd/tsidp
	PATH="$$(./tool/go env GOROOT)/bin:$$PATH" ./tool/go run github.com/tailscale/depaware --check --goos=linux,darwin,windows,android,ios --vendor --internal \
		tappervpn.com/tsnet
	PATH="$$(./tool/go env GOROOT)/bin:$$PATH" ./tool/go run github.com/tailscale/depaware --check --file=depaware-minbox.txt --goos=linux --tags="$$(./tool/go run ./cmd/featuretags --min --add=cli)" --vendor --internal \
		tappervpn.com/cmd/tappervpnd
	PATH="$$(./tool/go env GOROOT)/bin:$$PATH" ./tool/go run github.com/tailscale/depaware --check --file=depaware-min.txt --goos=linux --tags="$$(./tool/go run ./cmd/featuretags --min)" --vendor --internal \
		tappervpn.com/cmd/tappervpnd

buildwindows: ## Build tappervpn CLI for windows/amd64
	GOOS=windows GOARCH=amd64 ./tool/go install tappervpn.com/cmd/tappervpn tappervpn.com/cmd/tappervpnd

build386: ## Build tappervpn CLI for linux/386
	GOOS=linux GOARCH=386 ./tool/go install tappervpn.com/cmd/tappervpn tappervpn.com/cmd/tappervpnd

buildlinuxarm: ## Build tappervpn CLI for linux/arm
	GOOS=linux GOARCH=arm ./tool/go install tappervpn.com/cmd/tappervpn tappervpn.com/cmd/tappervpnd

buildwasm: ## Build tappervpn CLI for js/wasm
	GOOS=js GOARCH=wasm ./tool/go install ./cmd/tsconnect/wasm ./cmd/tappervpn/cli

buildplan9:
	GOOS=plan9 GOARCH=amd64 ./tool/go install ./cmd/tappervpn ./cmd/tappervpnd

buildlinuxloong64: ## Build tappervpn CLI for linux/loong64
	GOOS=linux GOARCH=loong64 ./tool/go install tappervpn.com/cmd/tappervpn tappervpn.com/cmd/tappervpnd

buildmultiarchimage: ## Build (and optionally push) multiarch docker image
	./build_docker.sh

check: staticcheck vet depaware buildwindows build386 buildlinuxarm buildwasm ## Perform basic checks and compilation tests

staticcheck: ## Run staticcheck.io checks
	./tool/go run honnef.co/go/tools/cmd/staticcheck -- $$(./tool/go run ./tool/listpkgs --ignore-3p  ./...)

kube-generate-all: kube-generate-deepcopy ## Refresh generated files for TapperVPN Kubernetes Operator
	./tool/go generate ./cmd/k8s-operator

# TapperVPN operator watches Connector custom resources in a Kubernetes cluster
# and caches them locally. Caching is done implicitly by controller-runtime
# library (the middleware used by TapperVPN operator to create kube control
# loops). When a Connector resource is GET/LIST-ed from within our control loop,
# the request goes through the cache. To ensure that cache contents don't get
# modified by control loops, controller-runtime deep copies the requested
# object. In order for this to work, Connector must implement deep copy
# functionality so we autogenerate it here.
# https://github.com/kubernetes-sigs/controller-runtime/blob/v0.16.3/pkg/cache/internal/cache_reader.go#L86-L89
kube-generate-deepcopy: ## Refresh generated deepcopy functionality for TapperVPN kube API types
	./scripts/kube-deepcopy.sh

spk: ## Build synology package for ${SYNO_ARCH} architecture and ${SYNO_DSM} DSM version
	./tool/go run ./cmd/dist build synology/dsm${SYNO_DSM}/${SYNO_ARCH}

spkall: ## Build synology packages for all architectures and DSM versions
	./tool/go run ./cmd/dist build synology

pushspk: spk ## Push and install synology package on ${SYNO_HOST} host
	echo "Pushing SPK to root@${SYNO_HOST} (env var SYNO_HOST) ..."
	scp tappervpn.spk root@${SYNO_HOST}:
	ssh root@${SYNO_HOST} /usr/syno/bin/synopkg install tappervpn.spk

.PHONY: check-image-repo
check-image-repo:
	@if [ -z "$(REPO)" ]; then \
		echo "REPO=... required; e.g. REPO=ghcr.io/$$USER/tappervpn" >&2; \
		exit 1; \
	fi
	@for repo in tappervpn/tappervpn ghcr.io/tappervpn/tappervpn \
		tappervpn/k8s-operator ghcr.io/tappervpn/k8s-operator \
		tappervpn/k8s-nameserver ghcr.io/tappervpn/k8s-nameserver \
		tappervpn/tsidp ghcr.io/tappervpn/tsidp \
		tappervpn/k8s-proxy ghcr.io/tappervpn/k8s-proxy; do \
		if [ "$(REPO)" = "$$repo" ]; then \
			echo "REPO=... must not be $$repo" >&2; \
			exit 1; \
		fi; \
	done

publishdevimage: check-image-repo ## Build and publish tappervpn image to location specified by ${REPO}
	TAGS="${TAGS}" REPOS=${REPO} PLATFORM=${PLATFORM} PUSH=true TARGET=client ./build_docker.sh

publishdevoperator: check-image-repo ## Build and publish k8s-operator image to location specified by ${REPO}
	TAGS="${TAGS}" REPOS=${REPO} PLATFORM=${PLATFORM} PUSH=true TARGET=k8s-operator ./build_docker.sh

publishdevnameserver: check-image-repo ## Build and publish k8s-nameserver image to location specified by ${REPO}
	TAGS="${TAGS}" REPOS=${REPO} PLATFORM=${PLATFORM} PUSH=true TARGET=k8s-nameserver ./build_docker.sh

publishdevtsidp: check-image-repo ## Build and publish tsidp image to location specified by ${REPO}
	TAGS="${TAGS}" REPOS=${REPO} PLATFORM=${PLATFORM} PUSH=true TARGET=tsidp ./build_docker.sh

publishdevproxy: check-image-repo ## Build and publish k8s-proxy image to location specified by ${REPO}
	TAGS="${TAGS}" REPOS=${REPO} PLATFORM=${PLATFORM} PUSH=true TARGET=k8s-proxy ./build_docker.sh

.PHONY: sshintegrationtest
sshintegrationtest: ## Run the SSH integration tests in various Docker containers
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 ./tool/go test -tags integrationtest -c ./ssh/tailssh -o ssh/tailssh/testcontainers/tailssh.test && \
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 ./tool/go build -o ssh/tailssh/testcontainers/tappervpnd ./cmd/tappervpnd && \
	echo "Testing on ubuntu:focal, ubuntu:jammy, ubuntu:noble, alpine:latest (in parallel)" && \
	docker build --build-arg="BASE=ubuntu:focal" -t ssh-ubuntu-focal ssh/tailssh/testcontainers & \
	docker build --build-arg="BASE=ubuntu:jammy" -t ssh-ubuntu-jammy ssh/tailssh/testcontainers & \
	docker build --build-arg="BASE=ubuntu:noble" -t ssh-ubuntu-noble ssh/tailssh/testcontainers & \
	docker build --build-arg="BASE=alpine:latest" -t ssh-alpine-latest ssh/tailssh/testcontainers & \
	wait

.PHONY: generate
generate: ## Generate code
	./tool/go generate ./...

.PHONY: tsapp-build-and-flash-pi
tsapp-build-and-flash-pi: ## Build a tsapp-pi.arm64 GAF from HEAD and flash a local SD card (macOS auto-detects the disk; pass DISK=/dev/sdX on Linux)
	cd gokrazy && ../tool/go run build.go --gaf --app=tsapp-pi.arm64
	./tool/go run --exec=sudo ./cmd/tappervpn configure flash-appliance \
		--variant=pi-arm64 \
		--gaf=gokrazy/tsapp-pi.arm64.gaf \
		$(if $(DISK),--disk=$(DISK)) \
		$(if $(wildcard $(HOME)/.ssh/id_ed25519.pub),--add-ssh-authorized-keys=$(HOME)/.ssh/id_ed25519.pub)

.PHONY: tsapp-qemu-pi
tsapp-qemu-pi: ## Build tsapp-pi.arm64 and boot it under qemu-system-aarch64 with a framebuffer GUI window and working network (requires mtools, dtc, qemu-efi-aarch64)
	cd gokrazy && ../tool/go run build.go --build --app=tsapp-pi.arm64
	# Extract the kernel from the FAT boot partition for direct -kernel boot.
	rm -f gokrazy/tsapp-pi.arm64.vmlinuz
	mcopy -i gokrazy/tsapp-pi.arm64.img@@4194304 ::vmlinuz gokrazy/tsapp-pi.arm64.vmlinuz
	# Use the "virt" machine (not raspi3b) because it provides working
	# PCI e1000 networking and, with UEFI firmware, an EFI framebuffer
	# via the ramfb device. The raspi3b machine's USB NIC emulation is
	# too broken for DHCP and its SoC watchdog reboots the guest.
	#
	# Find the UEFI firmware. Common paths:
	#   Debian/Ubuntu: /usr/share/qemu-efi-aarch64/QEMU_EFI.fd
	#   Homebrew:      /opt/homebrew/share/qemu/edk2-aarch64-code.fd
	#   Fedora:        /usr/share/edk2/aarch64/QEMU_EFI.fd
	QEMU_EFI=$$(for f in \
		/usr/share/qemu-efi-aarch64/QEMU_EFI.fd \
		/opt/homebrew/share/qemu/edk2-aarch64-code.fd \
		/usr/share/edk2/aarch64/QEMU_EFI.fd \
		$$(dirname $$(which qemu-system-aarch64))/../share/qemu/edk2-aarch64-code.fd; do \
		[ -f "$$f" ] && echo "$$f" && break; \
	done) && \
	[ -n "$$QEMU_EFI" ] || { echo "error: cannot find QEMU EFI firmware (install qemu-efi-aarch64)"; exit 1; } && \
	qemu-system-aarch64 \
		-M virt -cpu cortex-a53 -m 1G \
		-bios "$$QEMU_EFI" \
		-device ramfb \
		-device e1000,netdev=net0 -netdev user,id=net0 \
		-kernel gokrazy/tsapp-pi.arm64.vmlinuz \
		-append "console=ttyAMA0,115200 nowatchdog gokrazy.log_to_serial=1 root=PARTUUID=60c24cc1-f3f9-427a-8199-dd02023b0001/PARTNROFF=1 ro init=/gokrazy/init rootwait" \
		-drive file=gokrazy/tsapp-pi.arm64.img,format=raw,if=none,id=disk0 \
		-device virtio-blk-device,drive=disk0 \
		-serial mon:stdio

.PHONY: tsapp-push-pi
tsapp-push-pi: ## Build a tsapp-pi.arm64 GAF from HEAD and push it to a running Pi over the network (pass PI=<ip>)
	@[ -n "$(PI)" ] || { echo "usage: make tsapp-push-pi PI=<ip-address>"; exit 1; }
	cd gokrazy && ../tool/go run build.go --gaf --app=tsapp-pi.arm64
	./tool/go run ./gokrazy/gafpush --gaf=gokrazy/tsapp-pi.arm64.gaf --pi=$(PI)

.PHONY: pin-github-actions
pin-github-actions:
	./tool/go tool github.com/stacklok/frizbee actions .github/workflows

help: ## Show this help
	@echo ""
	@echo "Specify a command. The choices are:"
	@echo ""
	@grep -hE '^[0-9a-zA-Z_-]+:.*?## .*$$' ${MAKEFILE_LIST} | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[0;36m%-20s\033[m %s\n", $$1, $$2}'
	@echo ""
.PHONY: help

.DEFAULT_GOAL := help
