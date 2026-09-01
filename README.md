# Hawser

> **hawser** *(n.)* — the heavy line that moors a ship to the dock. It holds fast.

A minimal, invisible way to run the upstream open source **Docker Engine on Windows** via WSL2.
No license fees, no Electron, no Kubernetes — install once, `docker ps` works forever, on
laptops and CI runners alike.

**Status: pre-alpha.** Nothing installable yet — the architecture spikes are in progress.
Read [PLAN.md](PLAN.md) for the strategy and [ROADMAP.md](ROADMAP.md) for the schedule;
the [issue tracker](https://github.com/zcsizmadia/hawser/issues) is the live state.

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

## License

[Apache-2.0](LICENSE). Docker and the Docker logo are trademarks of Docker, Inc.
Hawser is not affiliated with or endorsed by Docker, Inc.
