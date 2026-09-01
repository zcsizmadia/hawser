# Hawser

> **hawser** *(n.)* — the heavy line that moors a ship to the dock. It holds fast.

A minimal, invisible way to run the upstream open source **Docker Engine on Windows** via WSL2.
No license fees, no Electron, no Kubernetes — install once, `docker ps` works forever, on
laptops and CI runners alike.

**Status: v0.1 pre-release.** Installable and working; the always-on supervisor, tray,
and doctor are still ahead. Read [PLAN.md](PLAN.md) for the strategy and
[ROADMAP.md](ROADMAP.md) for the schedule; the
[issue tracker](https://github.com/zcsizmadia/hawser/issues) is the live state.

## Install (v0.1)

Requirements: Windows 11 with WSL2, and any `docker` CLI on PATH (Docker Desktop's works —
Hawser coexists with it rather than replacing it).

1. Download the zip for your architecture from the
   [latest release](https://github.com/zcsizmadia/hawser/releases) and verify it against
   `SHA256SUMS` (binaries are not yet signed; SmartScreen will warn)
2. `hawser.exe install` — downloads the checksum-verified engine rootfs, imports it as the
   `hawser-engine` WSL2 distro, starts the engine, and wires a `hawser` docker context
3. `hawser.exe proxy` — serves the named pipe (keep it running; the v0.2 supervisor
   replaces this step)
4. In another shell: `docker --context hawser run --rm hello-world`

`hawser.exe uninstall` removes everything Hawser created — the distro and all images and
volumes in it — and restores your previous docker context. Nothing else on the system is
touched.

Known v0.1 limits: a logged-on session is required (a WSL2 platform constraint that binds
every WSL-based engine), the proxy runs in the foreground, and Linux containers only.

## What it will be

- Upstream Docker Engine (Linux containers) in a dedicated WSL2 distro — the real API, byte for byte
- A named-pipe bridge so stock `docker.exe`, compose, buildx, Testcontainers, and IDE
  integrations work unmodified
- A real Windows service: boot start, crash recovery, sleep/resume survival, no login required
- Headless CI installs, version pinning as a contract, `doctor` for the WSL/VPN quirk zoo
- A single Go binary under 15 MB, no telemetry

## What it will never be

Windows containers, Kubernetes, or a management GUI. Because Hawser serves the standard
Docker API, existing frontends (Portainer, lazydocker, VS Code) already work against it.

## Repository layout

Standard Go project layout — the Go toolchain, not a framework, decides this shape:

```
cmd/hawser/     the binary; one entry point, three run modes (CLI, service, tray)
internal/       implementation packages, compiler-enforced private to this module
  wsl/          every wsl.exe call, behind an interface so tests run anywhere
  ...           provision, pipeproxy, engine, doctor, svc, tray as they land
guest/          Linux side: rootfs build scripts, vsock agent
installer/      winget/scoop/choco manifests, WiX MSI
test/e2e/       cross-package suite; the only part needing real WSL2
spike/          throwaway experiments, deleted once their issue closes
```

Two Go conventions worth stating, since they surprise people arriving from other ecosystems:
**tests live beside the code they test** (`wsl.go` and `wsl_test.go` in the same folder — the
`_test.go` suffix is how the toolchain finds them, and package-private tests need it), and
there is no `src/` — the module root *is* the source root, and `internal/` is a compiler
rule (nothing outside this module can import it), not a naming preference. `test/e2e/` exists
only for suites that belong to no single package.

## License

[Apache-2.0](LICENSE). Docker and the Docker logo are trademarks of Docker, Inc.
Hawser is not affiliated with or endorsed by Docker, Inc.
