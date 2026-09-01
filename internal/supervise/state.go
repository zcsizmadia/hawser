// Package supervise keeps the engine alive: a health loop with backoff,
// driven by a desired state the CLI writes and the supervisor honors.
//
// The desired state lives in a file rather than a control socket on purpose.
// `hawser stop` must mean *stays* stopped — without a recorded intent, the
// health loop would helpfully restart the engine the user just stopped, and
// the two would fight. A file also works when the supervisor is not running
// at all, which is exactly when `hawser start` needs somewhere to leave its
// instruction.
package supervise

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Desired is what the user last asked for.
type Desired string

const (
	// DesiredRunning means the health loop keeps the engine up. The default:
	// installing Hawser is itself the request to have a working engine.
	DesiredRunning Desired = "running"
	// DesiredStopped means the health loop keeps the engine down.
	DesiredStopped Desired = "stopped"
)

func statePath(stateDir string) string {
	return filepath.Join(stateDir, "desired-state")
}

// ReadDesired returns the recorded intent, defaulting to running when nothing
// was ever recorded.
func ReadDesired(stateDir string) Desired {
	b, err := os.ReadFile(statePath(stateDir))
	if err != nil {
		return DesiredRunning
	}
	if Desired(strings.TrimSpace(string(b))) == DesiredStopped {
		return DesiredStopped
	}
	return DesiredRunning
}

// WriteDesired records the intent atomically, so a crash mid-write cannot
// leave a state the reader misparses into an unintended restart.
func WriteDesired(stateDir string, d Desired) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	tmp := statePath(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, []byte(d+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing desired state: %w", err)
	}
	if err := os.Rename(tmp, statePath(stateDir)); err != nil {
		return fmt.Errorf("committing desired state: %w", err)
	}
	return nil
}
