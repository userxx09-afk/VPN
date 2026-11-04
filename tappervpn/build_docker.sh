#!/usr/bin/env sh
#
# This script builds TapperVPN container images using
# github.com/tailscale/mkctr.
# By default the images will be tagged with the current version and git
# hash of this repository as produced by ./cmd/mkversion.
# This is the image build mechanism used to build the official TapperVPN
# container images.
#
# If you want to build local images for testing, you can use make, which provides few convenience wrappers around this script.
#
# To build a TapperVPN image and push to the local docker registry:

#   $ REPO=local/tappervpn TAGS=v0.0.1 PLATFORM=local  make publishdevimage
#
# To build a TapperVPN image and push to a remote docker registry:
#
#   $ REPO=<your-registry>/<your-repo>/tappervpn TAGS=v0.0.1  make publishdevimage

set -eu

# Use the "go" binary from the "tool" directory (which is github.com/tailscale/go)
export PATH="$PWD"/tool:"$PATH"

eval "$(./build_dist.sh shellvars)"

DEFAULT_TARGET="client"
DEFAULT_TAGS="v${VERSION_SHORT},v${VERSION_MINOR}"
DEFAULT_BASE="tappervpn/alpine-base:3.22"
# Set a few pre-defined OCI annotations. The source annotation is used by tools such as Renovate that scan the linked
# Github repo to find release notes for any new image tags. Note that for official TapperVPN images the default
# annotations defined here will be overriden by release scripts that call this script.
# https://github.com/opencontainers/image-spec/blob/main/annotations.md#pre-defined-annotation-keys
DEFAULT_ANNOTATIONS="org.opencontainers.image.source=https://github.com/tailscale/tailscale/blob/main/build_docker.sh,org.opencontainers.image.vendor=TapperVPN"

PUSH="${PUSH:-false}"
TARGET="${TARGET:-${DEFAULT_TARGET}}"
TAGS="${TAGS:-${DEFAULT_TAGS}}"
BASE="${BASE:-${DEFAULT_BASE}}"
PLATFORM="${PLATFORM:-}" # default to all platforms
GOARCH="${GOARCH:-arm,arm64,amd64,386,riscv64}"
FILES="${FILES:-}" # default to no extra files
OUTPUT="${OUTPUT:-}" # optional OCI image archive path
# OCI annotations that will be added to the image.
# https://github.com/opencontainers/image-spec/blob/main/annotations.md
ANNOTATIONS="${ANNOTATIONS:-${DEFAULT_ANNOTATIONS}}"

case "$TARGET" in
  client)
    DEFAULT_REPOS="tappervpn/tappervpn"
    REPOS="${REPOS:-${DEFAULT_REPOS}}"
    go run github.com/tailscale/mkctr \
      --output="${OUTPUT}" \
      --gopaths="\
        tappervpn.com/cmd/tappervpn:/usr/local/bin/tappervpn, \
        tappervpn.com/cmd/tappervpnd:/usr/local/bin/tappervpnd, \
        tappervpn.com/cmd/containerboot:/usr/local/bin/containerboot" \
      --ldflags="\
        -X tappervpn.com/version.longStamp=${VERSION_LONG} \
        -X tappervpn.com/version.shortStamp=${VERSION_SHORT} \
        -X tappervpn.com/version.gitCommitStamp=${VERSION_GIT_HASH}" \
      --base="${BASE}" \
      --tags="${TAGS}" \
      --gotags="ts_kube,ts_package_container" \
      --repos="${REPOS}" \
      --push="${PUSH}" \
      --target="${PLATFORM}" \
      --goarch="${GOARCH}" \
      --annotations="${ANNOTATIONS}" \
      --files="${FILES}" \
      /usr/local/bin/containerboot
    ;;
  k8s-operator)
    DEFAULT_REPOS="tappervpn/k8s-operator"
    REPOS="${REPOS:-${DEFAULT_REPOS}}"
    go run github.com/tailscale/mkctr \
      --output="${OUTPUT}" \
      --gopaths="tappervpn.com/cmd/k8s-operator:/usr/local/bin/operator" \
      --ldflags="\
        -X tappervpn.com/version.longStamp=${VERSION_LONG} \
        -X tappervpn.com/version.shortStamp=${VERSION_SHORT} \
        -X tappervpn.com/version.gitCommitStamp=${VERSION_GIT_HASH}" \
      --base="${BASE}" \
      --tags="${TAGS}" \
      --gotags="ts_kube,ts_package_container" \
      --repos="${REPOS}" \
      --push="${PUSH}" \
      --target="${PLATFORM}" \
      --goarch="${GOARCH}" \
      --annotations="${ANNOTATIONS}" \
      --files="${FILES}" \
      /usr/local/bin/operator
    ;;
  k8s-nameserver)
    DEFAULT_REPOS="tappervpn/k8s-nameserver"
    REPOS="${REPOS:-${DEFAULT_REPOS}}"
    go run github.com/tailscale/mkctr \
      --output="${OUTPUT}" \
      --gopaths="tappervpn.com/cmd/k8s-nameserver:/usr/local/bin/k8s-nameserver" \
      --ldflags=" \
        -X tappervpn.com/version.longStamp=${VERSION_LONG} \
        -X tappervpn.com/version.shortStamp=${VERSION_SHORT} \
        -X tappervpn.com/version.gitCommitStamp=${VERSION_GIT_HASH}" \
      --base="${BASE}" \
      --tags="${TAGS}" \
      --gotags="ts_kube,ts_package_container" \
      --repos="${REPOS}" \
      --push="${PUSH}" \
      --target="${PLATFORM}" \
      --goarch="${GOARCH}" \
      --annotations="${ANNOTATIONS}" \
      --files="${FILES}" \
      /usr/local/bin/k8s-nameserver
    ;;
  tsidp)
    DEFAULT_REPOS="tappervpn/tsidp"
    REPOS="${REPOS:-${DEFAULT_REPOS}}"
    go run github.com/tailscale/mkctr \
      --output="${OUTPUT}" \
      --gopaths="tappervpn.com/cmd/tsidp:/usr/local/bin/tsidp" \
      --ldflags=" \
        -X tappervpn.com/version.longStamp=${VERSION_LONG} \
        -X tappervpn.com/version.shortStamp=${VERSION_SHORT} \
        -X tappervpn.com/version.gitCommitStamp=${VERSION_GIT_HASH}" \
      --base="${BASE}" \
      --tags="${TAGS}" \
      --gotags="ts_package_container" \
      --repos="${REPOS}" \
      --push="${PUSH}" \
      --target="${PLATFORM}" \
      --goarch="${GOARCH}" \
      --annotations="${ANNOTATIONS}" \
      --files="${FILES}" \
      /usr/local/bin/tsidp
    ;;
  k8s-proxy)
    DEFAULT_REPOS="tappervpn/k8s-proxy"
    REPOS="${REPOS:-${DEFAULT_REPOS}}"
    go run github.com/tailscale/mkctr \
      --output="${OUTPUT}" \
      --gopaths="tappervpn.com/cmd/k8s-proxy:/usr/local/bin/k8s-proxy" \
      --ldflags=" \
        -X tappervpn.com/version.longStamp=${VERSION_LONG} \
        -X tappervpn.com/version.shortStamp=${VERSION_SHORT} \
        -X tappervpn.com/version.gitCommitStamp=${VERSION_GIT_HASH}" \
      --base="${BASE}" \
      --tags="${TAGS}" \
      --gotags="ts_kube,ts_package_container" \
      --repos="${REPOS}" \
      --push="${PUSH}" \
      --target="${PLATFORM}" \
      --goarch="${GOARCH}" \
      --annotations="${ANNOTATIONS}" \
      --files="${FILES}" \
      /usr/local/bin/k8s-proxy
    ;;
  *)
    echo "unknown target: $TARGET"
    exit 1
    ;;
esac
