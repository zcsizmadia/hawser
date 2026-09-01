package main

import (
	"log/slog"
	"os"

	"github.com/zcsizmadia/hawser/internal/pipeproxy"
)

// engineDialer builds the transport to the engine socket: the vsock agent
// (#40) first, the socat relay as automatic per-connection fallback — one
// dialer serves a new rootfs with the agent, an old rootfs without it, and an
// agent mid-restart, with no configuration.
//
// HAWSER_NO_VSOCK=1 pins the socat path, as the support lever for a machine
// where the fast path misbehaves.
func engineDialer(distro, socketPath string, log *slog.Logger) pipeproxy.Dialer {
	socat := &pipeproxy.WSLDialer{Distro: distro, SocketPath: socketPath}
	if os.Getenv("HAWSER_NO_VSOCK") == "1" {
		log.Info("vsock transport disabled by HAWSER_NO_VSOCK")
		return socat
	}
	return &pipeproxy.FallbackDialer{
		Primary:   &pipeproxy.VsockDialer{},
		Secondary: socat,
		Logger:    log,
	}
}
