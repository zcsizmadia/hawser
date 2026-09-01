#!/usr/bin/env bash
# Build the Hawser rootfs: Alpine + engine binaries compiled from upstream source.
#
# Runs in CI (ubuntu-latest, Docker available) and on any Linux host with Docker —
# including a WSL2 Ubuntu distro, which is how it gets tested locally.
#
#   ./build.sh [output-dir]     default: ./out
#
# Output: hawser-rootfs-<version>.tar.gz, its .sha256, and an SPDX SBOM.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out="${1:-$here/out}"
# shellcheck source=versions.env
. "$here/versions.env"

mkdir -p "$out"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "==> fetching Alpine minirootfs $ALPINE_ROOTFS_VERSION"
base="alpine-minirootfs-${ALPINE_ROOTFS_VERSION}-x86_64.tar.gz"
url="https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/releases/x86_64/${base}"
curl -fsSL -o "$work/$base" "$url"
curl -fsSL -o "$work/$base.sha256" "$url.sha256"
(cd "$work" && sha256sum -c "$base.sha256")

# Engine binaries are the slow part (~4 min of compiling). BIN_DIR lets a caller
# supply a directory that persists across runs — CI points it at an actions/cache
# entry keyed on the pins below, so a config-only change re-assembles the rootfs
# without recompiling anything. Layout: $BIN_DIR/bin/* plus $BIN_DIR/commits.txt.
engine="${BIN_DIR:-$work/engine}"
mkdir -p "$engine"

if [ -x "$engine/bin/dockerd" ] && [ -s "$engine/commits.txt" ]; then
    echo "==> reusing engine binaries from $engine"
    # The cache key covers versions.env, but a stale or mismatched entry would
    # otherwise ship silently — so confirm the binary is the pinned version.
    if ! "$engine/bin/dockerd" --version | grep -q "$ENGINE_VERSION"; then
        echo "cached dockerd is not $ENGINE_VERSION - rebuilding" >&2
        rm -rf "$engine"
        mkdir -p "$engine"
    fi
fi

if [ ! -x "$engine/bin/dockerd" ]; then
    echo "==> building engine binaries from source (this is the slow part)"
    # Each component is built in a pinned golang image from its upstream git tag —
    # no download.docker.com artifacts, so provenance is source -> binary end to end.
    # HOST_UID/GID: the build runs as root in the container but writes into a host
    # directory, so it hands ownership back before exiting — otherwise cleanup and
    # the tar step hit permission errors on the CI runner.
    docker run --rm \
      -v "$engine:/out" \
      -e "MOBY_TAG=$MOBY_TAG" \
      -e "ENGINE_VERSION=$ENGINE_VERSION" \
      -e "CONTAINERD_VERSION=$CONTAINERD_VERSION" \
      -e "RUNC_VERSION=$RUNC_VERSION" \
      -e "BUILDKIT_VERSION=$BUILDKIT_VERSION" \
      -e "HOST_UID=$(id -u)" \
      -e "HOST_GID=$(id -g)" \
      -v "$here/build-engine.sh:/build-engine.sh:ro" \
      "golang:${GO_VERSION}-alpine" sh /build-engine.sh
fi

echo "==> assembling rootfs"
root="$work/rootfs"
mkdir -p "$root"
tar -xzf "$work/$base" -C "$root"

# Runtime packages the engine and the bridge need. The Alpine minirootfs has
# none of them, and their absence is invisible until the artifact is actually
# booted: without iptables dockerd cannot set up container networking, and
# without socat the v0.1 pipe relay has nothing to connect the Windows side to.
# Installed with apk --root from a matching Alpine container, so the versions
# come from the same branch as the base.
echo "==> installing guest packages"
# The chown matters: apk writes as root into a host directory, and without it
# cleanup and the tar step fail with "Permission denied" on the runner. The tar
# below forces owner 0:0 into the archive regardless, so host ownership here has
# no effect on the shipped rootfs.
docker run --rm -v "$root:/rootfs" \
    -e "HOST_UID=$(id -u)" -e "HOST_GID=$(id -g)" \
    "alpine:${ALPINE_BRANCH#v}" sh -c '
    set -eu
    cp /etc/apk/repositories /rootfs/etc/apk/repositories
    apk add --root /rootfs --no-cache \
        socat iptables ip6tables iproute2 ca-certificates
    chown -R "${HOST_UID}:${HOST_GID}" /rootfs
'

install -d "$root/usr/local/bin" "$root/etc/docker" "$root/etc/hawser"
install -m 0755 "$engine/bin/"* "$root/usr/local/bin/"

# Engine defaults: log rotation on from the first run (PLAN §05 v0.1).
cat > "$root/etc/docker/daemon.json" <<'JSON'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" }
}
JSON

# WSL-side distro config. systemd is off: hawser supervises dockerd itself
# from the Windows service, so an init system inside the distro buys nothing.
cat > "$root/etc/wsl.conf" <<'CONF'
[boot]
systemd=false

[automount]
enabled=true
options="metadata"

[interop]
enabled=true
appendWindowsPath=false
CONF

printf '%s\n' "$ENGINE_VERSION" > "$root/etc/hawser/engine-version"
# Exact upstream commit each binary was built from — the provenance record
# `hawser version` and a security review both read.
install -m 0644 "$engine/commits.txt" "$root/etc/hawser/commits"

tarball="$out/hawser-rootfs-${ENGINE_VERSION}.tar.gz"
echo "==> writing $tarball"
# Deterministic tar: sorted, fixed mtimes/ownership, gzip without a timestamp,
# so identical inputs produce an identical checksum.
tar --sort=name --owner=0 --group=0 --numeric-owner \
    --mtime='UTC 2020-01-01' -C "$root" -cf - . | gzip -n > "$tarball"
(cd "$out" && sha256sum "$(basename "$tarball")" > "$(basename "$tarball").sha256")

echo "==> SBOM"
"$here/sbom.sh" "$here/versions.env" "$out/hawser-rootfs-${ENGINE_VERSION}.spdx.json"

ls -la "$out"
echo "OK"
