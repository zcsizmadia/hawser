//go:build windows

package pipeproxy

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

// DefaultPipeName is what stock docker.exe connects to when DOCKER_HOST is
// unset. Hawser claims it only when Docker Desktop has not; the installer
// falls back to FallbackPipeName so the two coexist (PLAN §02).
const DefaultPipeName = `\\.\pipe\docker_engine`

// FallbackPipeName is Hawser's own pipe, used when the default is taken.
const FallbackPipeName = `\\.\pipe\hawser_engine`

// DefaultSDDL restricts the pipe to SYSTEM, local administrators, and
// interactive users.
//
// Pipe access is equivalent to root inside the engine VM, which in turn can
// read and write anything the automounted drives expose — the same trust
// boundary Docker Desktop has, and the reason this is not left at the default
// descriptor. A service running as SYSTEM would otherwise own the pipe and shut
// the logged-in user out.
//
// TODO(#8): once `hawser install` creates the local "Hawser Users" group, its
// SID replaces IU here, matching Docker's docker-users pattern so non-admin
// developer accounts can be granted access deliberately rather than by virtue
// of being interactive.
const DefaultSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;IU)"

// Listen creates the named pipe. An empty sddl applies DefaultSDDL; pass a
// custom descriptor only with a reason, since this is the security boundary.
func Listen(pipeName, sddl string) (net.Listener, error) {
	if pipeName == "" {
		return nil, fmt.Errorf("pipeproxy: empty pipe name")
	}
	if sddl == "" {
		sddl = DefaultSDDL
	}

	l, err := winio.ListenPipe(pipeName, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		// The engine API carries large payloads (image push/pull, build
		// contexts); message mode would frame them wrongly. Byte mode is what
		// dockerd's own Windows listener uses.
		MessageMode: false,
	})
	if err != nil {
		return nil, fmt.Errorf("pipeproxy: listen %s: %w", pipeName, err)
	}
	return l, nil
}

func init() {
	// What a winio pipe returns when the relay closes its other direction —
	// ordinary shutdown, not a fault worth logging.
	platformClosedErrors = append(platformClosedErrors, winio.ErrFileClosed)
}
