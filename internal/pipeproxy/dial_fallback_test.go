package pipeproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type stubConn struct{ io.ReadWriteCloser }

func stubDialer(err error) (Dialer, *int) {
	calls := new(int)
	return DialerFunc(func(ctx context.Context) (io.ReadWriteCloser, error) {
		*calls++
		if err != nil {
			return nil, err
		}
		return stubConn{}, nil
	}), calls
}

func TestFallbackUsesPrimaryWhenHealthy(t *testing.T) {
	primary, pCalls := stubDialer(nil)
	secondary, sCalls := stubDialer(nil)
	d := &FallbackDialer{Primary: primary, Secondary: secondary}

	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *pCalls != 1 || *sCalls != 0 {
		t.Errorf("primary=%d secondary=%d calls, want 1/0", *pCalls, *sCalls)
	}
}

func TestFallbackDegradesAndRetriesPrimary(t *testing.T) {
	primary, pCalls := stubDialer(errors.New("no agent"))
	secondary, sCalls := stubDialer(nil)
	d := &FallbackDialer{Primary: primary, Secondary: secondary}

	for i := 0; i < 3; i++ {
		if _, err := d.Dial(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// The primary is re-attempted every connection — never latched off —
	// so recovery needs no state reset.
	if *pCalls != 3 || *sCalls != 3 {
		t.Errorf("primary=%d secondary=%d calls, want 3/3", *pCalls, *sCalls)
	}
}

func TestFallbackReportsSecondaryError(t *testing.T) {
	primary, _ := stubDialer(errors.New("no agent"))
	secondary, _ := stubDialer(errors.New("socat gone too"))
	d := &FallbackDialer{Primary: primary, Secondary: secondary}

	if _, err := d.Dial(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "socat gone too") {
		t.Errorf("want the secondary's error surfaced, got %v", err)
	}
}

func TestFallbackLogsEdgesNotEveryFailure(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	fail := errors.New("no agent")
	var primaryErr error = fail
	primary := DialerFunc(func(ctx context.Context) (io.ReadWriteCloser, error) {
		if primaryErr != nil {
			return nil, primaryErr
		}
		return stubConn{}, nil
	})
	secondary, _ := stubDialer(nil)
	d := &FallbackDialer{Primary: primary, Secondary: secondary, Logger: log}

	for i := 0; i < 5; i++ {
		d.Dial(context.Background())
	}
	if n := strings.Count(buf.String(), "fallback transport"); n != 1 {
		t.Errorf("degradation logged %d times over 5 failures, want 1:\n%s", n, buf.String())
	}

	primaryErr = nil
	d.Dial(context.Background())
	if n := strings.Count(buf.String(), "recovered"); n != 1 {
		t.Errorf("recovery logged %d times, want 1:\n%s", n, buf.String())
	}

	// A second outage is a new edge and logs again.
	primaryErr = fail
	d.Dial(context.Background())
	if n := strings.Count(buf.String(), "fallback transport"); n != 2 {
		t.Errorf("second degradation edge logged %d times total, want 2", n)
	}
}
