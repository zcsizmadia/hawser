//go:build windows

package pipeproxy

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/zcsizmadia/hawser/internal/vsockproto"
	"golang.org/x/sys/windows/registry"
)

// VsockDialer reaches the engine through the in-distro hawser-agent over
// AF_HYPERV — the v0.2 transport (#40), validated by Spike C. Per-connection
// cost is ~0.6 ms against socat's ~165 ms, and both ends support half-close,
// which removes the leak class of #35 outright.
type VsockDialer struct {
	// Port is the agent's vsock port. Zero uses vsockproto.Port.
	Port uint32

	// DialTimeout bounds one dial+handshake attempt. Zero uses 2s: the VM is
	// local, so anything slower than that is a "not there", not a "be patient".
	DialTimeout time.Duration

	// mu guards vmid, the discovered-and-verified utility VM. Caching it
	// skips registry enumeration on every connection; a dial failure clears
	// it, so a restarted VM (new GUID) is re-discovered on the next attempt.
	mu   sync.Mutex
	vmid *guid.GUID
}

// computeSystemKey is the Host Compute Service's mirror of running compute
// systems. Readable by standard users — the property Spike C established that
// makes this transport possible without elevation (hcsdiag needs Hyper-V
// admin). The key only lists *running* systems: with the distro stopped there
// are no candidates, which correctly reads as "agent unreachable".
const computeSystemKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\HostComputeService\VolatileStore\ComputeSystem`

func discoverVMIDs() ([]guid.GUID, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, computeSystemKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, fmt.Errorf("pipeproxy: enumerating compute systems: %w", err)
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("pipeproxy: reading compute systems: %w", err)
	}
	ids := make([]guid.GUID, 0, len(names))
	for _, n := range names {
		if id, err := guid.FromString(n); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (d *VsockDialer) port() uint32 {
	if d.Port == 0 {
		return vsockproto.Port
	}
	return d.Port
}

func (d *VsockDialer) timeout() time.Duration {
	if d.DialTimeout == 0 {
		return 2 * time.Second
	}
	return d.DialTimeout
}

// dialVM makes one verified connection: transport dial plus the vsockproto
// handshake. The handshake is what turns "some listener answered on our port
// in some VM" into "this is a hawser agent" — non-WSL utility VMs can appear
// in the compute-system list, and the port space inside the WSL VM is shared
// with every other distro.
func (d *VsockDialer) dialVM(ctx context.Context, vmid guid.GUID) (io.ReadWriteCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	conn, err := winio.Dial(ctx, &winio.HvsockAddr{
		VMID:      vmid,
		ServiceID: winio.VsockServiceID(d.port()),
	})
	if err != nil {
		return nil, err
	}
	// The handshake shares the dial's deadline; clear it before handing the
	// transparent stream to the relay.
	conn.SetDeadline(time.Now().Add(d.timeout()))
	if _, err := vsockproto.ClientHandshake(conn); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

// Dial implements Dialer.
func (d *VsockDialer) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	d.mu.Lock()
	cached := d.vmid
	d.mu.Unlock()

	if cached != nil {
		if conn, err := d.dialVM(ctx, *cached); err == nil {
			return conn, nil
		}
		// The VM may have restarted under a new GUID; fall through to a
		// fresh discovery rather than failing on stale state.
		d.mu.Lock()
		d.vmid = nil
		d.mu.Unlock()
	}

	ids, err := discoverVMIDs()
	if err != nil {
		return nil, err
	}
	var lastErr error = fmt.Errorf("pipeproxy: no running compute systems")
	for i := range ids {
		conn, err := d.dialVM(ctx, ids[i])
		if err != nil {
			lastErr = err
			continue
		}
		d.mu.Lock()
		d.vmid = &ids[i]
		d.mu.Unlock()
		return conn, nil
	}
	return nil, fmt.Errorf("pipeproxy: no hawser agent reachable: %w", lastErr)
}
