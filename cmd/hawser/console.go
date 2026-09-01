package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// consoleHandler renders slog records as short human lines.
//
// The default text handler emits logfmt, which is right for a service and wrong
// for someone watching an install: "time=... level=INFO msg=importing distro
// distro=hawser-engine" buries the sentence in metadata. This prints the
// message, then only the attributes that add something.
type consoleHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Level
}

func newConsoleHandler(w io.Writer, level slog.Level) slog.Handler {
	return &consoleHandler{mu: &sync.Mutex{}, w: w, level: level}
}

func (h *consoleHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder

	switch {
	case r.Level >= slog.LevelError:
		b.WriteString("error: ")
	case r.Level >= slog.LevelWarn:
		b.WriteString("warning: ")
	default:
		b.WriteString("  ")
	}
	b.WriteString(r.Message)

	r.Attrs(func(a slog.Attr) bool {
		// "fix" carries a remedy, which belongs on its own line where it can
		// be read rather than appended to a key=value tail.
		if a.Key == "fix" {
			return true
		}
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	b.WriteString("\n")

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "fix" {
			fmt.Fprintf(&b, "    fix: %v\n", a.Value)
		}
		return true
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *consoleHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *consoleHandler) WithGroup(string) slog.Handler      { return h }

// emitJSON writes v to stdout for scripting, and is the only place --json
// output is produced so the shape stays consistent across commands.
func emitJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "hawser: %v\n", err)
		return exitError
	}
	return exitOK
}

// interruptCtx is a plain background context for short-lived setup calls that
// should not be cancelled by the Ctrl-C that stops the long-running server.
func interruptCtx() context.Context { return context.Background() }
