//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// vsockConn wraps an accepted AF_VSOCK fd as an io.ReadWriteCloser with
// CloseWrite, which vsockproto.Relay uses to propagate EOFs. The fd is put in
// non-blocking mode before os.NewFile so reads park in Go's poller instead of
// pinning an OS thread each.
type vsockConn struct {
	f  *os.File
	fd int
}

func newVsockConn(fd int) (*vsockConn, error) {
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	return &vsockConn{f: os.NewFile(uintptr(fd), "vsock"), fd: fd}, nil
}

func (c *vsockConn) Read(p []byte) (int, error)  { return c.f.Read(p) }
func (c *vsockConn) Write(p []byte) (int, error) { return c.f.Write(p) }
func (c *vsockConn) Close() error                { return c.f.Close() }

// CloseWrite sends EOF to the peer while reads keep working.
func (c *vsockConn) CloseWrite() error {
	return unix.Shutdown(c.fd, unix.SHUT_WR)
}
