//go:build windows

package supervise

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// Lock is a held single-instance claim. Release with Close; the OS also
// releases it if the process dies, which is the property a PID file cannot
// offer without staleness heuristics.
type Lock struct {
	handle windows.Handle
}

// lockName derives a per-install mutex name: two Hawser installs with
// different state dirs (the e2e suite next to a real install, say) must not
// exclude each other. Session-local (`Local\`), because the supervisor is a
// per-user, per-session concern.
func lockName(stateDir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(stateDir)))
	return `Local\hawser-supervisor-` + hex.EncodeToString(sum[:8])
}

// ErrAlreadyRunning reports a second supervisor for the same install.
type ErrAlreadyRunning struct{ StateDir string }

func (e *ErrAlreadyRunning) Error() string {
	return fmt.Sprintf("a supervisor for %s is already running "+
		"(two would fight over the pipe); use `hawser status` to see it", e.StateDir)
}

// Acquire claims the single-instance lock, failing fast if held elsewhere.
func Acquire(stateDir string) (*Lock, error) {
	name, err := windows.UTF16PtrFromString(lockName(stateDir))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, true, name)
	// CreateMutex succeeds even when the mutex exists; ownership is the part
	// that matters, reported via ERROR_ALREADY_EXISTS.
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, &ErrAlreadyRunning{StateDir: stateDir}
	}
	if err != nil {
		return nil, fmt.Errorf("acquiring supervisor lock: %w", err)
	}
	return &Lock{handle: h}, nil
}

// Held reports whether some process holds the lock, without taking it. Used by
// `hawser status` and by `hawser start` to decide whether to spawn a
// supervisor.
func Held(stateDir string) bool {
	l, err := Acquire(stateDir)
	if err != nil {
		return true // held (or unqueryable, which reads the same to a caller)
	}
	l.Close()
	return false
}

// Close releases the claim.
func (l *Lock) Close() error {
	if l.handle != 0 {
		windows.ReleaseMutex(l.handle)
		windows.CloseHandle(l.handle)
		l.handle = 0
	}
	return nil
}
