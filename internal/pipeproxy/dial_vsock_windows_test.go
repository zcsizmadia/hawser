//go:build windows

package pipeproxy

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Microsoft/go-winio/pkg/guid"
)

// fakeHV lets tests script the transport without a VM.
func fakeHV(d *VsockDialer, fn func() (io.ReadWriteCloser, error)) *int {
	calls := new(int)
	d.dialHV = func(ctx context.Context, vmid guid.GUID) (io.ReadWriteCloser, error) {
		*calls++
		return fn()
	}
	return calls
}

func TestVsockDialerCoolsDownAfterFailure(t *testing.T) {
	d := &VsockDialer{Cooldown: time.Hour}
	calls := fakeHV(d, func() (io.ReadWriteCloser, error) {
		return nil, errors.New("no listener")
	})

	// First dial really probes (and fails; discovery may or may not find
	// VMs on the test machine, both are failures).
	if _, err := d.Dial(context.Background()); err == nil {
		t.Fatal("dial succeeded with a failing transport")
	}
	probed := *calls

	// While cooling down, dials fail instantly without touching the
	// transport — this is what keeps an agent-less rootfs from paying a
	// dial timeout per docker command.
	for i := 0; i < 5; i++ {
		_, err := d.Dial(context.Background())
		if !errors.Is(err, ErrCoolingDown) {
			t.Fatalf("dial %d during cooldown: err = %v, want ErrCoolingDown", i, err)
		}
	}
	if *calls != probed {
		t.Errorf("transport dialed %d times during cooldown", *calls-probed)
	}
}

func TestVsockDialerRetriesAfterCooldown(t *testing.T) {
	d := &VsockDialer{Cooldown: time.Millisecond}
	fakeHV(d, func() (io.ReadWriteCloser, error) {
		return nil, errors.New("no listener")
	})

	d.Dial(context.Background())
	time.Sleep(5 * time.Millisecond)
	if _, err := d.Dial(context.Background()); errors.Is(err, ErrCoolingDown) {
		t.Fatal("still cooling down after the window passed")
	}
}

func TestVsockDialerNegativeCooldownDisables(t *testing.T) {
	d := &VsockDialer{Cooldown: -1}
	fakeHV(d, func() (io.ReadWriteCloser, error) {
		return nil, errors.New("no listener")
	})

	d.Dial(context.Background())
	if _, err := d.Dial(context.Background()); errors.Is(err, ErrCoolingDown) {
		t.Fatal("cooldown applied despite being disabled")
	}
}
