package supervise

import (
	"context"
	"log/slog"
	"time"
)

// Engine is what the supervisor drives. The seam matches what the provisioner
// and wsl packages already offer, and lets the loop be tested without WSL.
type Engine interface {
	// Running reports whether the engine socket answers.
	Running(ctx context.Context) bool
	// Start brings the engine up (idempotent; provisioner.StartEngine).
	Start(ctx context.Context) error
	// Stop terminates the engine's own distro — and only that distro. Stopping
	// anything wider (another distro, wsl --shutdown) is off the table by
	// design: Hawser shares the machine (PLAN §02, the Docker Desktop incident
	// on #35).
	Stop(ctx context.Context) error
}

// Config tunes the loop. Zero values get defaults.
type Config struct {
	// StateDir is where the desired state is read from.
	StateDir string
	// Interval between health checks. Also bounds how quickly a CLI-written
	// desired-state change is noticed. Default 3s.
	Interval time.Duration
	// BackoffMax caps the restart backoff. Default 60s.
	BackoffMax time.Duration
}

func (c Config) interval() time.Duration {
	if c.Interval <= 0 {
		return 3 * time.Second
	}
	return c.Interval
}

func (c Config) backoffMax() time.Duration {
	if c.BackoffMax <= 0 {
		return 60 * time.Second
	}
	return c.BackoffMax
}

// Supervisor reconciles the engine with the desired state.
type Supervisor struct {
	Engine Engine
	Config Config
	Log    *slog.Logger

	// failures counts consecutive start failures, for backoff.
	failures int
	// nextTry is the earliest moment another start attempt is allowed.
	nextTry time.Time
}

func (s *Supervisor) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Run reconciles until the context ends. It never returns an error for the
// engine being down — that is a condition to repair, not a reason to exit —
// and it survives sleep/resume for free: a resumed machine simply fails the
// next health check and gets repaired like any other crash.
func (s *Supervisor) Run(ctx context.Context) {
	t := time.NewTicker(s.Config.interval())
	defer t.Stop()

	// Reconcile immediately rather than waiting out the first tick: the
	// supervisor usually starts at logon, and the user is waiting.
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			s.log().Info("supervisor stopping", "reason", ctx.Err())
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Supervisor) tick(ctx context.Context) {
	desired := ReadDesired(s.Config.StateDir)
	up := s.Engine.Running(ctx)

	switch {
	case desired == DesiredRunning && !up:
		if time.Now().Before(s.nextTry) {
			return // still backing off
		}
		s.log().Warn("engine is down; starting it", "consecutiveFailures", s.failures)
		if err := s.Engine.Start(ctx); err != nil {
			s.failures++
			delay := s.backoff()
			s.nextTry = time.Now().Add(delay)
			s.log().Error("engine start failed",
				"error", err, "retryIn", delay, "consecutiveFailures", s.failures)
			return
		}
		s.failures = 0
		s.nextTry = time.Time{}
		s.log().Info("engine recovered")

	case desired == DesiredStopped && up:
		s.log().Info("desired state is stopped; stopping the engine")
		if err := s.Engine.Stop(ctx); err != nil {
			s.log().Error("engine stop failed", "error", err)
		}

	case desired == DesiredRunning && up:
		// Healthy: a success observed by the loop also resets backoff, so one
		// bad patch (a WSL update mid-flight, say) does not tax the next.
		s.failures = 0
		s.nextTry = time.Time{}
	}
}

// backoff is exponential from 2s, capped: crash loops must not hammer WSL,
// but a transient failure (sleep/resume races, `wsl --shutdown`) should be
// repaired in seconds, not minutes.
func (s *Supervisor) backoff() time.Duration {
	d := 2 * time.Second
	for i := 1; i < s.failures; i++ {
		d *= 2
		if d >= s.Config.backoffMax() {
			return s.Config.backoffMax()
		}
	}
	return d
}
