//go:build windows

// Command hawsertray is the Hawser status-light tray (PLAN §03, #44): a fixed,
// tiny menu whose every action shells out to the `hawser` CLI. It is built as
// a GUI-subsystem binary (-H=windowsgui) so launching it flashes no console.
//
// The tray deliberately holds no engine logic. It polls `hawser status
// --json` to colour a dot, and each menu item runs a CLI verb; anything that
// would need a decision belongs in the CLI, which is tested. The menu never
// grows past its six groups — that cap is the scope moat.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"fyne.io/systray"
	"github.com/zcsizmadia/hawser/internal/tray"
)

func main() {
	systray.Run(onReady, func() {})
}

// hawserExe resolves the sibling hawser.exe next to this binary, falling back
// to PATH. The tray ships beside the CLI in the release zip.
func hawserExe() string {
	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), "hawser.exe")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return "hawser"
}

func onReady() {
	cli := tray.CLI{Exe: hawserExe()}

	systray.SetTitle("Hawser")
	systray.SetIcon(iconGrey)

	// A disabled header shows the state in words; the icon is the dot.
	header := systray.AddMenuItem("Engine: …", "")
	header.Disable()
	systray.AddSeparator()

	// Lifecycle: the three CLI verbs, as data from the logic package.
	lifecycle := make([]*systray.MenuItem, len(tray.Actions))
	for i, a := range tray.Actions {
		lifecycle[i] = systray.AddMenuItem(a.Label, "")
	}
	systray.AddSeparator()

	openLogs := systray.AddMenuItem("Open logs", "Open the supervisor log")
	doctor := systray.AddMenuItem("Run doctor (v0.3)", "Diagnostics arrive in v0.3")
	doctor.Disable() // honest stub: `hawser doctor` is not built yet
	updates := systray.AddMenuItem("Check for updates", "Open the releases page")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit Hawser tray", "Close this tray (the engine keeps running)")

	// Wire each item to a CLI call in its own goroutine so the click returns
	// immediately and a slow start does not freeze the menu.
	for i, item := range lifecycle {
		go func(item *systray.MenuItem, a tray.Action) {
			for range item.ClickedCh {
				go cli.Run(context.Background(), a)
			}
		}(item, tray.Actions[i])
	}
	go func() {
		for range openLogs.ClickedCh {
			go openLog(cli)
		}
	}()
	go func() {
		for range updates.ClickedCh {
			go browse("https://github.com/zcsizmadia/hawser/releases")
		}
	}()
	go func() {
		<-quit.ClickedCh
		systray.Quit()
	}()

	// The status loop: poll now, then every few seconds, and repaint the dot.
	go func() {
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		paint := func() {
			st := cli.Poll(context.Background())
			s := st.State()
			header.SetTitle("Engine: " + label(s))
			systray.SetTooltip(tray.Tooltip(s))
			systray.SetIcon(iconFor(s))
		}
		paint()
		for range t.C {
			paint()
		}
	}()
}

func iconFor(s tray.State) []byte {
	switch {
	case tray.Healthy(s):
		return iconGreen
	case s == tray.StateStopped:
		return iconGrey
	default:
		return iconRed
	}
}

func label(s tray.State) string {
	switch s {
	case tray.StateRunning:
		return "running"
	case tray.StateIdle:
		return "idle (starts on demand)"
	case tray.StateStopped:
		return "stopped"
	case tray.StateNotInstalled:
		return "not installed"
	default:
		return "unknown"
	}
}

func openLog(cli tray.CLI) {
	// The supervisor writes supervisor.log under the default state dir.
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return
	}
	browse(filepath.Join(base, "Hawser", "supervisor.log"))
}

func browse(target string) {
	// rundll32 avoids a shell and handles both URLs and file paths.
	exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
}
