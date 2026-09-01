# Hawser rootfs

The Linux side of Hawser: an Alpine base plus the engine, built into a tarball that
`wsl --import` turns into the `hawser-engine` distro.

## Provenance

Engine binaries are **compiled from upstream source** (moby, containerd, runc, buildkit at
pinned git tags) rather than repackaged from `download.docker.com`. Two reasons: the SBOM
can point at a commit instead of someone else's zip, and redistributing Docker Inc.'s
compiled bundles raises a trademark question that building from Apache-2.0 source doesn't.
See PLAN §04 and the trademark risk row in §09.

## Build

Needs Linux + Docker — CI uses `ubuntu-latest`; locally a WSL2 Ubuntu distro works.

```bash
./build.sh [out-dir]        # default ./out — takes ~30 min, mostly compiling moby
./smoke-test.sh out         # verify the artifact before trusting it
```

Outputs, all three of which become release assets:

- `hawser-rootfs-<version>.tar.gz` — the distro image
- `hawser-rootfs-<version>.tar.gz.sha256` — what `hawser install` verifies before importing
- `hawser-rootfs-<version>.spdx.json` — SBOM, so a security team can diff what's inside

The tarball is built deterministically (sorted entries, fixed mtimes and ownership,
timestamp-free gzip), so the same pins reproduce the same checksum.

## Versions

Every pin lives in `versions.env` — nothing is ever fetched as "latest". The component
combination there is the one the acceptance suite validates; bumping any single pin means
re-running acceptance, because `hawser engine upgrade` refuses combinations outside the
tested matrix (PLAN §04).

## What's baked in

- `/usr/local/bin/` — dockerd, containerd, containerd-shim-runc-v2, ctr, runc, buildkitd, buildctl
- `/etc/docker/daemon.json` — log rotation on from the first run
- `/etc/wsl.conf` — systemd off (Hawser's Windows service supervises dockerd instead),
  metadata on automounts, Windows PATH not appended into the distro
- `/etc/hawser/engine-version` — what `hawser version` reports
