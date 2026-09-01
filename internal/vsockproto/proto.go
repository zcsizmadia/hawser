// Package vsockproto is the tiny wire protocol between the Windows host and
// the in-distro hawser-agent (#40), shared by both ends.
//
// The connection starts line-oriented — client sends a magic hello, the agent
// answers with its identity — and then goes fully transparent, a byte relay to
// the engine socket. The handshake exists because the WSL2 utility VM's vsock
// port space is shared by every distro in it (Docker Desktop's included): a
// dial that reaches the wrong listener must fail closed, not speak Docker HTTP
// at a stranger.
package vsockproto

import (
	"fmt"
	"io"
	"strings"
)

// Port is the vsock port the agent listens on: ASCII "haws". Vsock ports are
// a full 32-bit space with no well-known registry, so an implausible value is
// the collision strategy.
const Port uint32 = 0x68617773

const (
	hello    = "HAWSER/1\n"
	okPrefix = "OK "
	// maxLine bounds handshake reads; anything longer is not our peer.
	maxLine = 256
)

// readLine reads up to and including '\n' one byte at a time. Byte-wise on
// purpose: a buffered reader could swallow bytes that belong to the
// transparent phase that follows.
func readLine(r io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for b.Len() < maxLine {
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		if buf[0] == '\n' {
			return b.String(), nil
		}
		b.WriteByte(buf[0])
	}
	return "", fmt.Errorf("handshake line exceeds %d bytes", maxLine)
}

// ServerHandshake validates the client hello and replies with this agent's
// identity. On a bad hello the error is returned without a reply: the caller
// closes, and the stranger learns nothing.
func ServerHandshake(c io.ReadWriter, version string) error {
	line, err := readLine(c)
	if err != nil {
		return fmt.Errorf("reading hello: %w", err)
	}
	if line+"\n" != hello {
		return fmt.Errorf("unexpected hello %q", line)
	}
	if _, err := c.Write([]byte(okPrefix + version + "\n")); err != nil {
		return fmt.Errorf("writing banner: %w", err)
	}
	return nil
}

// ClientHandshake sends the hello and returns the agent's identity line.
func ClientHandshake(c io.ReadWriter) (string, error) {
	if _, err := c.Write([]byte(hello)); err != nil {
		return "", fmt.Errorf("writing hello: %w", err)
	}
	line, err := readLine(c)
	if err != nil {
		return "", fmt.Errorf("reading banner: %w", err)
	}
	if !strings.HasPrefix(line, okPrefix) {
		return "", fmt.Errorf("peer is not a hawser agent: %q", line)
	}
	return strings.TrimPrefix(line, okPrefix), nil
}
