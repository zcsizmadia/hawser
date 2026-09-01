//go:build windows

package pipeproxy

import (
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
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
		// Message mode, for one specific reason: it is the only way a named
		// pipe can carry a half-close. The docker CLI signals stdin-EOF on a
		// hijacked interactive stream (docker run -i, exec -i, a pipe into a
		// container) by calling CloseWrite on its connection; on a byte-mode
		// winio pipe that is a silent no-op, so the container's stdin never
		// ends and the command hangs forever (#57). In message mode winio
		// implements CloseWrite as a zero-byte message the reader surfaces as
		// io.EOF — which the relay already propagates to the engine.
		//
		// This does NOT reframe large payloads: winio presents a message-mode
		// pipe as a byte stream for ordinary reads (it swallows
		// ERROR_MORE_DATA), and a zero-length data write sends nothing, so
		// only an explicit CloseWrite ever signals EOF. Image pull/push,
		// build contexts and every hijack path are exercised by the
		// acceptance suite under this mode.
		MessageMode: true,
	})
	if err != nil {
		return nil, fmt.Errorf("pipeproxy: listen %s: %w", pipeName, err)
	}
	return l, nil
}

func init() {
	// Ordinary shutdown shapes on Windows, not faults worth logging:
	// winio.ErrFileClosed is what a winio pipe returns when the relay closes
	// its other direction; ERROR_BROKEN_PIPE ("the pipe has been ended") and
	// ERROR_NO_DATA ("the pipe is being closed") are what a pipe or hvsock
	// read/write returns when the peer went away abruptly — the normal end of
	// a docker CLI that exits mid-connection.
	platformClosedErrors = append(platformClosedErrors,
		winio.ErrFileClosed, windows.ERROR_BROKEN_PIPE, windows.ERROR_NO_DATA)
}
