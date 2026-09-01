package vsockproto

import (
	"io"
	"sync"
)

// WriteCloser is the half-close half of the story. Both vsock and unix
// sockets support shutdown(SHUT_WR), and propagating it is what makes a
// client's EOF explicit instead of inferred — the property socat lacked and
// the reason #35 could only be bounded, not fixed.
type WriteCloser interface {
	CloseWrite() error
}

// closeWrite half-closes when the conn supports it, else fully closes —
// the degraded-but-correct behavior for transports without shutdown.
func closeWrite(c io.Closer) {
	if hc, ok := c.(WriteCloser); ok {
		hc.CloseWrite()
		return
	}
	c.Close()
}

// Relay copies bytes both ways, propagating each side's EOF to the other as a
// write-shutdown, and returns when both directions are done. Errors other
// than the usual close-races are reported from the first direction to fail.
func Relay(a, b io.ReadWriteCloser) error {
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error

	cp := func(dst, src io.ReadWriteCloser) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err != nil {
			once.Do(func() { firstErr = err })
			// A broken direction cannot half-close its way out; tear down.
			dst.Close()
			src.Close()
			return
		}
		// src reached EOF: tell dst's reader there is nothing more coming,
		// while dst→src traffic keeps flowing.
		closeWrite(dst)
	}

	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()

	a.Close()
	b.Close()
	return firstErr
}
