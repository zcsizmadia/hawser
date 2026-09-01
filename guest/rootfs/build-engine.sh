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

# Clone at a pinned tag and record the commit it resolved to.
#
# These are annotated tags, so refs/tags/<tag> points at a tag object rather
# than a commit and git prints "warning: refs/tags/X ... is not a commit!"
# during a shallow clone. It is cosmetic — git peels the tag and checks out the
# right tree — but a tag can be moved upstream, so we pin the belt and record
# the resolved SHA as the suspenders: that SHA is what the SBOM reports.
clone() { # repo tag dir
    git -c advice.detachedHead=false clone --depth 1 --branch "$2" "$1" "/src/$3" 2>&1 |
        grep -v 'is not a commit!' || true
    sha="$(git -C "/src/$3" rev-parse HEAD)"
    printf '%s %s %s\n' "$3" "$2" "$sha" >> /out/commits.txt
    echo "    $3 $2 -> $sha"
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
# VERSION is what `dockerd --version` reports; without it moby stamps "dev",
# which the smoke test rejects and `hawser version` would misreport.
(cd /src/moby && VERSION="$ENGINE_VERSION" ./hack/make.sh binary-daemon && \
    find bundles -name dockerd -type f -exec cp {} /out/bin/ \;)

echo "--- buildkit $BUILDKIT_VERSION"
clone https://github.com/moby/buildkit.git "$BUILDKIT_VERSION" buildkit
# Without these ldflags buildkit reports "v0.0.0+unknown" — same trap as moby's
# VERSION, and `hawser version` is supposed to report the truth.
bk_rev="$(git -C /src/buildkit rev-parse HEAD)"
bk_ld="-X github.com/moby/buildkit/version.Version=${BUILDKIT_VERSION#v}"
bk_ld="$bk_ld -X github.com/moby/buildkit/version.Revision=$bk_rev"
(cd /src/buildkit && go build -ldflags "$bk_ld" -o /out/bin/buildkitd ./cmd/buildkitd && \
    go build -ldflags "$bk_ld" -o /out/bin/buildctl ./cmd/buildctl)

strip /out/bin/* 2>/dev/null || true
chmod 0755 /out/bin/*
# Hand the artifacts back to the invoking user; we are root inside the container
# but /out is a host directory (see the HOST_UID comment in build.sh).
chown -R "${HOST_UID:-0}:${HOST_GID:-0}" /out
ls -la /out/bin
