package supervise

import "context"

// TickForTest runs one reconcile pass; the idle tests drive ticks directly
// instead of racing a real ticker.
func (s *Supervisor) TickForTest(ctx context.Context) { s.tick(ctx) }
