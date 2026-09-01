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

echo "==> building engine binaries from source (this is the slow part)"
# Each component is built in a pinned golang image from its upstream git tag —
# no download.docker.com artifacts, so provenance is source -> binary end to end.
# HOST_UID/GID: the build runs as root in the container but writes into a host
# directory, so it hands ownership back before exiting — otherwise cleanup and
# the tar step hit permission errors on the CI runner.
docker run --rm \
  -v "$work:/out" \
  -e "MOBY_TAG=$MOBY_TAG" \
  -e "CONTAINERD_VERSION=$CONTAINERD_VERSION" \
  -e "RUNC_VERSION=$RUNC_VERSION" \
  -e "BUILDKIT_VERSION=$BUILDKIT_VERSION" \
  -e "HOST_UID=$(id -u)" \
  -e "HOST_GID=$(id -g)" \
  -v "$here/build-engine.sh:/build-engine.sh:ro" \
  "golang:${GO_VERSION}-alpine" sh /build-engine.sh

echo "==> assembling rootfs"
root="$work/rootfs"
mkdir -p "$root"
tar -xzf "$work/$base" -C "$root"
install -d "$root/usr/local/bin" "$root/etc/docker" "$root/etc/hawser"
install -m 0755 "$work/bin/"* "$root/usr/local/bin/"

# Engine defaults: log rotation on from the first run (PLAN §05 v0.1).
cat > "$root/etc/docker/daemon.json" <<'JSON'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-files": "3" }
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
