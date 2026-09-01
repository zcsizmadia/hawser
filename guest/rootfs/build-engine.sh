#!/bin/sh
# Runs INSIDE a pinned golang:alpine container (invoked by build.sh).
# Clones each engine component at its pinned tag and builds a static binary.
# Result: /out/bin/{dockerd,containerd,containerd-shim-runc-v2,ctr,runc,buildkitd,buildctl}
set -eu

apk add --no-cache git make bash musl-dev gcc libseccomp-dev libseccomp-static \
    btrfs-progs-dev linux-headers pkgconf

mkdir -p /out/bin /src
export CGO_ENABLED=0
export GOFLAGS=-trimpath

clone() { # repo tag dir
    git -c advice.detachedHead=false clone --depth 1 --branch "$2" "$1" "/src/$3"
}

echo "--- runc $RUNC_VERSION"
clone https://github.com/opencontainers/runc.git "$RUNC_VERSION" runc
# runc needs cgo for libseccomp; static via the seccomp buildtag.
(cd /src/runc && CGO_ENABLED=1 make static BUILDTAGS="seccomp" && cp runc /out/bin/)

echo "--- containerd $CONTAINERD_VERSION"
clone https://github.com/containerd/containerd.git "$CONTAINERD_VERSION" containerd
(cd /src/containerd && make STATIC=1 binaries && \
    cp bin/containerd bin/containerd-shim-runc-v2 bin/ctr /out/bin/)

echo "--- moby (dockerd) $MOBY_TAG"
clone https://github.com/moby/moby.git "$MOBY_TAG" moby
(cd /src/moby && ./hack/make.sh binary-daemon && \
    find bundles -name dockerd -type f -exec cp {} /out/bin/ \;)

echo "--- buildkit $BUILDKIT_VERSION"
clone https://github.com/moby/buildkit.git "$BUILDKIT_VERSION" buildkit
(cd /src/buildkit && go build -o /out/bin/buildkitd ./cmd/buildkitd && \
    go build -o /out/bin/buildctl ./cmd/buildctl)

strip /out/bin/* 2>/dev/null || true
chmod 0755 /out/bin/*
ls -la /out/bin
