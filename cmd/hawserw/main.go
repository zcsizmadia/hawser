// Command hawserw is the windowless launcher for the supervisor — the javaw
// convention. It exists because hawser.exe is a console binary, and anything
// that starts a console binary at logon (a Run key, an interactive scheduled
// task) flashes a console window at the user. hawserw is built for the GUI
// subsystem (-H=windowsgui), so no console is ever created; it spawns
// `hawser supervise` hidden and exits.
//
// If a supervisor is already running, supervise itself exits via its
// single-instance lock; hawserw does not need to care.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func main() {
	self, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	// The sibling console binary does the real work; keeping the launcher a
	// spawner rather than a second copy of the CLI keeps the zip honest about
	// where behavior lives.
	hawser := filepath.Join(filepath.Dir(self), "hawser.exe")
	if _, err := os.Stat(hawser); err != nil {
		os.Exit(1)
	}

	cmd := exec.Command(hawser, append([]string{"supervise"}, os.Args[1:]...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		os.Exit(1)
	}
	cmd.Process.Release()
}
