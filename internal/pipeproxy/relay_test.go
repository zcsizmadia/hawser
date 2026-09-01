package pipeproxy_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/zcsizmadia/hawser/internal/pipeproxy"
)

// echoServer stands in for dockerd: a local TCP listener whose connections
// support CloseWrite, so half-close semantics are exercised for real rather
// than mocked. net.Pipe would not do — it has no CloseWrite.
type echoServer struct {
	l      net.Listener
	handle func(net.Conn)
}

func newServer(t *testing.T, handle func(net.Conn)) *echoServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &echoServer{l: l, handle: handle}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				s.handle(c)
			}()
		}
	}()
	t.Cleanup(func() { l.Close() })
	return s
}

func (s *echoServer) dialer() pipeproxy.Dialer {
	return pipeproxy.DialerFunc(func(ctx context.Context) (io.ReadWriteCloser, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", s.l.Addr().String())
	})
}

// startProxy runs a Server on a local listener and returns its address.
func startProxy(t *testing.T, srv *pipeproxy.Server) (addr string, stop func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, l) }()

	return l.Addr().String(), func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after cancel")
		}
	}
}

func TestRelayEchoesBothDirections(t *testing.T) {
	srv := newServer(t, func(c net.Conn) { io.Copy(c, c) })
	addr, stop := startProxy(t, &pipeproxy.Server{Dialer: srv.dialer()})
	defer stop()

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()

	want := "GET /_ping HTTP/1.1\r\n\r\n"
	if _, err := io.WriteString(c, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(want))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != want {
		t.Errorf("got %q, want %q", buf, want)
	}
}

func TestRelayPropagatesHalfClose(t *testing.T) {
	// The docker build / docker save shape: the client streams a body, signals
	// end-of-request with a half-close, and only then expects a response. If
	// the relay turns that half-close into a full close, or fails to propagate
	// it, the server either never responds or the response is truncated.
	srv := newServer(t, func(c net.Conn) {
		body, err := io.ReadAll(c) // returns only when the half-close arrives
		if err != nil {
			return
		}
		fmt.Fprintf(c, "received %d bytes", len(body))
	})
	addr, stop := startProxy(t, &pipeproxy.Server{Dialer: srv.dialer()})
	defer stop()

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()

	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if _, err := c.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}

	resp, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	want := fmt.Sprintf("received %d bytes", len(payload))
	if string(resp) != want {
		t.Errorf("got %q, want %q", resp, want)
	}
}

func TestRelayStreamsIncrementally(t *testing.T) {
	// The docker logs -f shape: output must arrive as it is produced, not
	// buffered until the connection closes.
	srv := newServer(t, func(c net.Conn) {
		for i := 0; i < 3; i++ {
			fmt.Fprintf(c, "line-%d\n", i)
			time.Sleep(20 * time.Millisecond)
		}
	})
	addr, stop := startProxy(t, &pipeproxy.Server{Dialer: srv.dialer()})
	defer stop()

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()

	buf := make([]byte, 7)
	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read first line: %v", err)
	}
	if string(buf) != "line-0\n" {
		t.Errorf("first read = %q, want %q", buf, "line-0\n")
	}
}

func TestRelayBinarySafe(t *testing.T) {
	// docker save / image push carry arbitrary bytes; Spike A checked this
	// against a real engine, and this keeps it true without one.
	srv := newServer(t, func(c net.Conn) { io.Copy(c, c) })
	addr, stop := startProxy(t, &pipeproxy.Server{Dialer: srv.dialer()})
	defer stop()

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()

	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte(i) // every byte value, including 0x00 and 0xFF
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Write(payload)
		c.(*net.TCPConn).CloseWrite()
	}()

	got, err := io.ReadAll(c)
	wg.Wait()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("got %d bytes, want %d", len(got), len(payload))
	}
	for i := range payload {
		if got[i] != payload[i] {
			t.Fatalf("byte %d differs: got %#x want %#x", i, got[i], payload[i])
		}
	}
}

func TestRelayConcurrentConnections(t *testing.T) {
	// Compose opens many connections at once; each must stay independent.
	srv := newServer(t, func(c net.Conn) { io.Copy(c, c) })
	addr, stop := startProxy(t, &pipeproxy.Server{Dialer: srv.dialer()})
	defer stop()

	const n = 25
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := net.Dial("tcp", addr)
			if err != nil {
				errCh <- err
				return
			}
			defer c.Close()

			want := fmt.Sprintf("connection-%03d", i)
			if _, err := io.WriteString(c, want); err != nil {
				errCh <- err
				return
			}
			buf := make([]byte, len(want))
			if _, err := io.ReadFull(c, buf); err != nil {
				errCh <- err
				return
			}
			if string(buf) != want {
				errCh <- fmt.Errorf("crossed streams: got %q want %q", buf, want)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestServeShutsDownWithStreamingClient(t *testing.T) {
	// The docker logs -f / docker events shape: a connection neither side
	// wants to end. Cancelling the context must still stop Serve promptly,
	// or stopping the Windows service would hang whenever someone left a
	// follow running.
	srv := newServer(t, func(c net.Conn) {
		for {
			if _, err := io.WriteString(c, "tick\n"); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (&pipeproxy.Server{Dialer: srv.dialer()}).Serve(ctx, l) }()

	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// Read one chunk so the relay is definitely established and streaming.
	buf := make([]byte, 5)
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return while a client was streaming")
	}
}

func TestServeRequiresDialer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	if err := (&pipeproxy.Server{}).Serve(context.Background(), l); err == nil {
		t.Error("Serve with no Dialer succeeded, want error")
	}
}

func TestServeStopsOnContextCancel(t *testing.T) {
	srv := newServer(t, func(c net.Conn) { io.Copy(c, c) })
	_, stop := startProxy(t, &pipeproxy.Server{Dialer: srv.dialer()})
	stop() // asserts Serve returns nil, not an accept error, on cancellation
}

func TestDialFailureClosesClient(t *testing.T) {
	// If the engine is down, the client must see a closed connection rather
	// than hang — docker reports "error during connect", which is honest.
	failing := pipeproxy.DialerFunc(func(context.Context) (io.ReadWriteCloser, error) {
		return nil, errors.New("engine unavailable")
	})
	addr, stop := startProxy(t, &pipeproxy.Server{Dialer: failing})
	defer stop()

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()

	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("expected clean EOF, got %v", err)
	}
}

func TestCustomHandlerReplacesRelay(t *testing.T) {
	// The seam RewriteBinds plugs into: a handler fully replaces the default
	// byte relay rather than wrapping the connection.
	srv := newServer(t, func(c net.Conn) { io.Copy(c, c) })
	var called bool
	var mu sync.Mutex
	proxy := &pipeproxy.Server{
		Dialer: srv.dialer(),
		Handler: func(c net.Conn, e io.ReadWriteCloser) error {
			mu.Lock()
			called = true
			mu.Unlock()
			return pipeproxy.Relay(c, e)
		},
	}
	addr, stop := startProxy(t, proxy)
	defer stop()

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	io.WriteString(c, "x")
	buf := make([]byte, 1)
	io.ReadFull(c, buf)
	c.Close()

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Error("custom Handler was not called")
	}
}
