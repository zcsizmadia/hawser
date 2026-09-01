package tray

import "testing"

func TestStatusToState(t *testing.T) {
	cases := []struct {
		s    Status
		want State
	}{
		{Status{Installed: true, Engine: "running"}, StateRunning},
		{Status{Installed: true, Engine: "idle"}, StateIdle},
		{Status{Installed: true, Engine: "stopped"}, StateStopped},
		{Status{Installed: false}, StateNotInstalled},
		{Status{Installed: true, Engine: "wat"}, StateUnknown},
	}
	for _, c := range cases {
		if got := c.s.State(); got != c.want {
			t.Errorf("state(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestHealthy(t *testing.T) {
	// Idle is healthy: the engine is one docker command away, by design.
	for _, st := range []State{StateRunning, StateIdle} {
		if !Healthy(st) {
			t.Errorf("Healthy(%q) = false, want true", st)
		}
	}
	for _, st := range []State{StateStopped, StateNotInstalled, StateUnknown} {
		if Healthy(st) {
			t.Errorf("Healthy(%q) = true, want false", st)
		}
	}
}

func TestTooltipCoversEveryState(t *testing.T) {
	for _, st := range []State{StateRunning, StateIdle, StateStopped, StateNotInstalled, StateUnknown} {
		if Tooltip(st) == "" {
			t.Errorf("no tooltip for state %q", st)
		}
	}
}

func TestActionsAreCLICalls(t *testing.T) {
	// The tray holds no logic: every lifecycle action is a bare CLI verb.
	want := map[string]string{
		"Start engine":   "start",
		"Stop engine":    "stop",
		"Restart engine": "restart",
	}
	if len(Actions) != len(want) {
		t.Fatalf("got %d actions, want %d", len(Actions), len(want))
	}
	for _, a := range Actions {
		verb, ok := want[a.Label]
		if !ok {
			t.Errorf("unexpected action %q", a.Label)
			continue
		}
		if len(a.Args) != 1 || a.Args[0] != verb {
			t.Errorf("action %q runs %v, want [%s]", a.Label, a.Args, verb)
		}
	}
}
