//go:build windows

// Spike C host: dial AF_HYPERV from Windows into the WSL2 utility VM.
//
// The one genuinely uncertain step of #40 is discovering the utility VM's
// GUID without elevation (hcsdiag list refuses non-admin). Probed answer: the
// Host Compute Service mirrors running compute systems into
// HKLM\...\HostComputeService\VolatileStore\ComputeSystem, and that key is
// readable by standard users. Everything after that is known art: the
// service ID is the vsock port templated into Microsoft's wildcard GUID
// (go-winio's VsockServiceID), and WSLg ships on exactly this transport.
//
// Usage: host.exe [-vm GUID] [-port 5000] [-n 50]
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/pkg/guid"
	"golang.org/x/sys/windows/registry"
)

const computeSystemKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\HostComputeService\VolatileStore\ComputeSystem`

// discoverVMs lists running compute-system GUIDs without elevation.
func discoverVMs() ([]string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, computeSystemKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, fmt.Errorf("opening HCS VolatileStore (is any WSL distro running?): %w", err)
	}
	defer k.Close()
	return k.ReadSubKeyNames(-1)
}

func dialOnce(vmid guid.GUID, port uint32, timeout time.Duration) (banner string, connectCost time.Duration, err error) {
	addr := &winio.HvsockAddr{VMID: vmid, ServiceID: winio.VsockServiceID(port)}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	conn, err := winio.Dial(ctx, addr)
	if err != nil {
		return "", 0, err
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := r.ReadString('\n')
	if err != nil {
		return "", 0, fmt.Errorf("reading banner: %w", err)
	}
	connectCost = time.Since(start)

	// Echo round-trip proves both directions carry data.
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		return "", 0, fmt.Errorf("write: %w", err)
	}
	echo, err := r.ReadString('\n')
	if err != nil || echo != "ping\n" {
		return "", 0, fmt.Errorf("echo round-trip failed: %q, %v", echo, err)
	}
	return strings.TrimSpace(line), connectCost, nil
}

func main() {
	var (
		vmFlag  = flag.String("vm", "", "VM GUID (default: discover from the HCS registry mirror)")
		port    = flag.Uint("port", 5000, "vsock port the guest listens on")
		n       = flag.Int("n", 50, "connections for the per-connection latency measurement")
		timeout = flag.Duration("timeout", 5*time.Second, "per-dial timeout")
	)
	flag.Parse()

	var candidates []string
	if *vmFlag != "" {
		candidates = []string{*vmFlag}
	} else {
		var err error
		candidates, err = discoverVMs()
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("discovered %d compute system(s) without elevation: %v\n", len(candidates), candidates)
	}

	for _, c := range candidates {
		vmid, err := guid.FromString(c)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %q: %v\n", c, err)
			continue
		}
		banner, first, err := dialOnce(vmid, uint32(*port), *timeout)
		if err != nil {
			fmt.Printf("vm %s: no listener on vsock:%d (%v)\n", c, *port, err)
			continue
		}
		fmt.Printf("vm %s: CONNECTED, banner=%q, first connect+banner=%v\n", c, banner, first)

		// Per-connection cost: this is the number that replaces socat's
		// ~165 ms measured in Spike A.
		var total time.Duration
		ok := 0
		for i := 0; i < *n; i++ {
			_, d, err := dialOnce(vmid, uint32(*port), *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  connection %d failed: %v\n", i, err)
				continue
			}
			total += d
			ok++
		}
		if ok > 0 {
			fmt.Printf("per-connection cost over %d dials: avg %v\n", ok, total/time.Duration(ok))
		}
		fmt.Println("PASS")
		return
	}
	fmt.Fprintln(os.Stderr, "FAIL: no candidate VM accepted the connection")
	os.Exit(1)
}
