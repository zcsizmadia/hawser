// Spike B probe: answers whether WSL2 and dockerd can run from a Windows
// service with nobody logged in (issue #3).
//
// Runs either as a console program (for a baseline in the interactive session)
// or as a Windows service, and appends JSON lines to a log both SYSTEM and the
// interactive user can read. The heartbeat is the point: it records socket
// health every few seconds, so after logging off and back on the log shows
// what happened during the window when no user was present — which is the one
// thing you cannot observe live.
//
// Throwaway spike code. Not product quality, deliberately dependency-light.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const (
	serviceName = "HawserSpikeB"
	logDir      = `C:\ProgramData\hawser-spike-b`
)

// distro is the WSL distribution to probe, from HAWSER_SPIKE_DISTRO.
func distro() string {
	if d := os.Getenv("HAWSER_SPIKE_DISTRO"); d != "" {
		return d
	}
	return "hawser-spike-b"
}

type event struct {
	Time    string `json:"time"`
	Phase   string `json:"phase"`
	Mode    string `json:"mode"`
	User    string `json:"user,omitempty"`
	Session uint32 `json:"session,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Output  string `json:"output,omitempty"`
	Err     string `json:"error,omitempty"`
	OK      *bool  `json:"ok,omitempty"`
}

var mode = "console"

func logEvent(e event) {
	e.Time = time.Now().Format(time.RFC3339)
	e.Mode = mode
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(logDir, "probe.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
}

func boolp(b bool) *bool { return &b }

// identity records who we are and which session we are in. Session 0 is the
// non-interactive services session — seeing it here is the whole point.
func identity() (string, uint32) {
	var user string
	if t, err := windows.OpenCurrentProcessToken(); err == nil {
		defer t.Close()
		if u, err := t.GetTokenUser(); err == nil {
			if account, domain, _, err := u.User.Sid.LookupAccount(""); err == nil {
				user = domain + `\` + account
			} else {
				user = u.User.Sid.String()
			}
		}
	}
	// Session 0 is the services session; an interactive login is 1 or higher.
	var session uint32
	windows.ProcessIdToSessionId(uint32(os.Getpid()), &session)
	return user, session
}

// run executes a command and returns combined output, trimmed for the log.
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	// wsl.exe emits UTF-16; strip NULs so the log stays readable.
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) > 4000 {
		s = s[:4000] + "...(truncated)"
	}
	return s, err
}

// probe runs the questions the spike must answer, in order of increasing
// dependence on the previous one succeeding.
func probe() {
	user, session := identity()
	logEvent(event{Phase: "identity", User: user, Session: session,
		Detail: fmt.Sprintf("session %d (0 = non-interactive services session)", session)})

	// 1. Does wsl.exe run at all in this context?
	out, err := run("wsl.exe", "--status")
	logEvent(event{Phase: "wsl-status", Output: out, Err: errString(err), OK: boolp(err == nil)})

	// 2. Which distros does THIS account see? Registration lives in HKCU, so a
	// service account may see none even though the interactive user has several.
	// If this comes back empty, `hawser install` must import the distro as the
	// service account rather than as the installing user.
	out, err = run("wsl.exe", "--list", "--verbose")
	logEvent(event{Phase: "wsl-list", Output: out, Err: errString(err), OK: boolp(err == nil)})

	d := distro()
	visible := err == nil && strings.Contains(out, d)
	logEvent(event{Phase: "distro-visible", Detail: d, OK: boolp(visible)})
	if !visible {
		logEvent(event{Phase: "conclusion",
			Detail: "target distro not visible in this account - per-user registration confirmed as the blocker",
			OK:     boolp(false)})
		return
	}

	// 3. Can we start the distro and reach a shell?
	out, err = run("wsl.exe", "-d", d, "-u", "root", "--exec", "echo", "alive")
	logEvent(event{Phase: "distro-exec", Output: out, Err: errString(err), OK: boolp(err == nil)})
	if err != nil {
		return
	}

	// 4. Start dockerd if it is not already running.
	out, _ = run("wsl.exe", "-d", d, "-u", "root", "--exec", "sh", "-c", "pgrep dockerd || echo none")
	if strings.Contains(out, "none") {
		go run("wsl.exe", "-d", d, "-u", "root", "--exec", "sh", "-c",
			"dockerd >/var/log/dockerd.log 2>&1")
		logEvent(event{Phase: "dockerd-start", Detail: "launched"})
	} else {
		logEvent(event{Phase: "dockerd-start", Detail: "already running: " + out})
	}

	// 5. Wait for the socket, recording how long it took.
	start := time.Now()
	for i := 0; i < 60; i++ {
		if _, err := run("wsl.exe", "-d", d, "-u", "root", "--exec",
			"test", "-S", "/var/run/docker.sock"); err == nil {
			logEvent(event{Phase: "socket-up",
				Detail: fmt.Sprintf("after %s", time.Since(start).Round(time.Millisecond)),
				OK:     boolp(true)})
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	out, _ = run("wsl.exe", "-d", d, "-u", "root", "--exec", "tail", "-30", "/var/log/dockerd.log")
	logEvent(event{Phase: "socket-up", Output: out, OK: boolp(false)})
}

// heartbeat records socket health until told to stop. Reading these lines after
// logging back in is how the no-login window gets evaluated.
func heartbeat(stop <-chan struct{}) {
	d := distro()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			logEvent(event{Phase: "heartbeat-stop"})
			return
		case <-t.C:
			_, err := run("wsl.exe", "-d", d, "-u", "root", "--exec",
				"test", "-S", "/var/run/docker.sock")
			logEvent(event{Phase: "heartbeat", OK: boolp(err == nil), Err: errString(err)})
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type spikeService struct{}

func (spikeService) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending}
	stop := make(chan struct{})

	logEvent(event{Phase: "service-start"})
	go func() {
		probe()
		heartbeat(stop)
	}()

	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			logEvent(event{Phase: "service-stop", Detail: fmt.Sprintf("cmd %d", c.Cmd)})
			close(stop)
			s <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}

func main() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine service context:", err)
		os.Exit(1)
	}

	if isService {
		mode = "service"
		if err := svc.Run(serviceName, spikeService{}); err != nil {
			logEvent(event{Phase: "service-error", Err: err.Error()})
			os.Exit(1)
		}
		return
	}

	// Console mode: the interactive baseline to compare the service run against.
	mode = "console"
	fmt.Println("running probe in console mode; results ->", filepath.Join(logDir, "probe.log"))
	probe()
	fmt.Println("done")
}
