// Package wsl hides every wsl.exe invocation behind an interface so the rest
// of the codebase is testable anywhere; only the e2e suite needs real WSL2.
package wsl

import "context"

// WSL is the seam between hawser and the wsl.exe binary. Production code
// depends on this interface, never on exec.Command directly.
type WSL interface {
	// Status reports whether WSL2 and virtualization are available.
	Status(ctx context.Context) (Status, error)
	// Import registers a distro from a rootfs tarball (wsl --import, version 2).
	Import(ctx context.Context, distro, installDir, rootfsPath string) error
	// Unregister removes a distro and its VHDX (wsl --unregister).
	Unregister(ctx context.Context, distro string) error
	// Terminate stops a running distro (wsl --terminate).
	Terminate(ctx context.Context, distro string) error
	// List returns registered distro names (wsl --list --quiet).
	List(ctx context.Context) ([]string, error)
}

// Status describes the host's WSL capability, from wsl --status and wsl --version.
type Status struct {
	Installed      bool
	DefaultVersion int
	Version        string // WSL release, e.g. "2.7.8.0"
}
