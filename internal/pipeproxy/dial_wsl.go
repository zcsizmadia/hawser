package pipeproxy

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// WSLDialer reaches the engine socket by running socat inside the distro and
// relaying its stdio — the v0.1 transport Spike A validated.
//
// One process per connection is the known cost: ~165 ms of wsl.exe startup,
// measured in #2. It is accepted deliberately because it needs no guest agent,
// no vsock plumbing, and no open port; v0.2 replaces it with a persistent agent
// once the correctness this buys is locked in by tests.
type WSLDialer struct {
	// Distro is the WSL distribution name, e.g. "hawser-engine".
	Distro string

	// SocketPath is the engine socket inside the distro.
	// Defaults to DefaultSocketPath.
	SocketPath string

	// Exe overrides the wsl.exe path. Empty uses PATH.
	Exe string
}

// DefaultSocketPath is where dockerd listens inside the Hawser distro.
const DefaultSocketPath = "/var/run/docker.sock"

// Dial starts the relay process and returns its stdio as a connection.
func (d *WSLDialer) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	if d.Distro == "" {
		return nil, fmt.Errorf("pipeproxy: WSLDialer.Distro is required")
	}
	socket := d.SocketPath
	if socket == "" {
		socket = DefaultSocketPath
	}
	exe := d.Exe
	if exe == "" {
		exe = "wsl.exe"
	}

	// --exec skips the shell, so nothing re-interprets the socket path and
	// there is no shell process between us and socat.
	cmd := exec.CommandContext(ctx, exe,
		"-d", d.Distro,
		"-u", "root",
		"--exec", "socat", "STDIO", "UNIX-CONNECT:"+socket,
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pipeproxy: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("pipeproxy: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("pipeproxy: start %s: %w", exe, err)
	}

	return &procConn{cmd: cmd, in: stdin, out: stdout}, nil
}

// procConn presents a child process's stdio as a connection. Closing stdin is a
// genuine half-close — socat sees EOF and shuts down its write side to the
// engine socket, which is what makes request-body-then-read work.
type procConn struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  io.ReadCloser
	done bool
}

func (p *procConn) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *procConn) Write(b []byte) (int, error) { return p.in.Write(b) }

// CloseWrite propagates end-of-request without ending the response.
func (p *procConn) CloseWrite() error { return p.in.Close() }

// Close tears down the process and reaps it, so a busy client cannot leave a
// trail of zombie wsl.exe processes behind.
func (p *procConn) Close() error {
	if p.done {
		return nil
	}
	p.done = true

	p.in.Close()
	p.out.Close()
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.cmd.Wait()
	return nil
}
