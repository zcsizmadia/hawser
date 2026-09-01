//go:build windows

package pipeproxy_test

import (
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/zcsizmadia/hawser/internal/pipeproxy"
)

func TestDockerHostFor(t *testing.T) {
	// The docker CLI wants forward slashes even on Windows, so the backslash
	// pipe name has to be rewritten rather than passed through.
	tests := []struct {
		in   string
		want string
	}{
		{`\\.\pipe\docker_engine`, "npipe:////./pipe/docker_engine"},
		{`\\.\pipe\hawser_engine`, "npipe:////./pipe/hawser_engine"},
		{"//./pipe/hawser_engine", "npipe:////./pipe/hawser_engine"},
		{"hawser_custom", "npipe://hawser_custom"},
	}
	for _, tt := range tests {
		if got := pipeproxy.DockerHostFor(tt.in); got != tt.want {
			t.Errorf("DockerHostFor(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPipeInUseDetectsALiveListener(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\hawser-inuse-%d`, os.Getpid())

	if pipeproxy.PipeInUse(name) {
		t.Fatalf("%s reported in use before anything listened", name)
	}

	l, err := pipeproxy.Listen(name, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	// Accept in the background: the probe dials, so something must answer.
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.(net.Conn).Close()
		}
	}()

	if !pipeproxy.PipeInUse(name) {
		t.Errorf("%s reported free while a listener held it", name)
	}

	l.Close()
	if pipeproxy.PipeInUse(name) {
		t.Errorf("%s still reported in use after the listener closed", name)
	}
}

func TestSelectPipeNameHonorsExplicitChoice(t *testing.T) {
	// An explicit non-default name is taken as given; the caller meant it.
	custom := `\\.\pipe\hawser-explicit`
	got, reason := pipeproxy.SelectPipeName(custom)
	if got != custom {
		t.Errorf("got %q, want %q", got, custom)
	}
	if reason == "" {
		t.Error("no reason given for the selection")
	}
}

func TestSelectPipeNameFallsBackWhenDefaultIsHeld(t *testing.T) {
	// The behavior that makes trying Hawser zero-risk: it never takes the
	// default pipe away from Docker Desktop.
	//
	// This machine's real state decides which branch runs, so both are
	// asserted for what they must be rather than which one occurs.
	got, reason := pipeproxy.SelectPipeName("")

	switch got {
	case pipeproxy.DefaultPipeName:
		if pipeproxy.PipeInUse(pipeproxy.DefaultPipeName) {
			t.Error("chose the default pipe while something else was serving it")
		}
		if reason == "" {
			t.Error("no reason given")
		}
	case pipeproxy.FallbackPipeName:
		if !pipeproxy.PipeInUse(pipeproxy.DefaultPipeName) {
			t.Error("fell back although the default pipe was free")
		}
		if reason == "" {
			t.Error("fallback must explain itself, since it decides whether the user needs DOCKER_HOST")
		}
	default:
		t.Errorf("chose an unexpected pipe %q", got)
	}
}

func TestDefaultAndFallbackPipesDiffer(t *testing.T) {
	// If these ever collided, coexistence with Docker Desktop would silently
	// stop working.
	if pipeproxy.DefaultPipeName == pipeproxy.FallbackPipeName {
		t.Fatal("default and fallback pipe names are identical")
	}
}
