package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zcsizmadia/hawser/internal/wsl"
)

// DefaultDistro is the WSL distribution Hawser imports. Deliberately distinct
// so it never collides with a user's own Ubuntu (PLAN §04).
const DefaultDistro = "hawser-engine"

// EngineSocket is where dockerd listens inside the distro.
const EngineSocket = "/var/run/docker.sock"

// Options configures an install. Zero values get sensible defaults, so callers
// only set what they mean to change.
type Options struct {
	// Distro is the WSL distribution name. Defaults to DefaultDistro.
	Distro string
	// StateDir holds the manifest and the rootfs cache.
	// Defaults to %LOCALAPPDATA%\Hawser.
	StateDir string
	// DataDir is where the distro's VHDX lives. Defaults to StateDir\distro.
	// Exposed because "move it off C:" is a perennial request (PLAN §03).
	DataDir string
	// RootfsURL and RootfsSHA256 identify the rootfs to install. The checksum
	// is mandatory: an unverified rootfs becomes root inside the engine VM.
	RootfsURL    string
	RootfsSHA256 string
	// EngineVersion is recorded in the manifest for `hawser version`.
	EngineVersion string
	// Headless suppresses anything that would wait for a human.
	Headless bool
	// StartTimeout bounds the wait for dockerd's socket. Defaults to 60s.
	StartTimeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.Distro == "" {
		o.Distro = DefaultDistro
	}
	if o.StateDir == "" {
		o.StateDir = defaultStateDir()
	}
	if o.DataDir == "" {
		o.DataDir = filepath.Join(o.StateDir, "distro")
	}
	if o.StartTimeout == 0 {
		o.StartTimeout = 60 * time.Second
	}
	return o
}

func defaultStateDir() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(base, "Hawser")
	}
	// Non-Windows only happens in tests; keep it deterministic rather than
	// panicking so the package stays testable everywhere.
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "hawser")
	}
	return filepath.Join(home, ".hawser")
}

// Manifest records what an install put on the machine, so uninstall can remove
// exactly that and `hawser version` can report it without re-deriving anything.
type Manifest struct {
	Distro        string    `json:"distro"`
	DataDir       string    `json:"dataDir"`
	RootfsURL     string    `json:"rootfsUrl"`
	RootfsSHA256  string    `json:"rootfsSha256"`
	EngineVersion string    `json:"engineVersion"`
	InstalledAt   time.Time `json:"installedAt"`
	// WSLVersion is what WSL reported at install time, useful when diagnosing
	// a machine whose WSL was updated afterwards.
	WSLVersion string `json:"wslVersion,omitempty"`
}

// Provisioner performs installs and removals.
type Provisioner struct {
	// WSL drives wsl.exe. Defaults to the real implementation.
	WSL wsl.WSL
	// Fetcher retrieves the rootfs. Defaults to HTTP.
	Fetcher Fetcher
	// Logger receives progress. Defaults to slog.Default().
	Logger *slog.Logger
}

func (p *Provisioner) wsl() wsl.WSL {
	if p.WSL != nil {
		return p.WSL
	}
	return wsl.NewLocal()
}

func (p *Provisioner) fetcher() Fetcher {
	if p.Fetcher != nil {
		return p.Fetcher
	}
	return HTTPFetcher{}
}

func (p *Provisioner) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// PreflightError reports that installation cannot proceed, carrying every
// problem so the user sees the full picture rather than one issue per run.
type PreflightError struct{ Report Report }

func (e *PreflightError) Error() string {
	msg := "preflight failed:"
	for _, p := range e.Report.Problems {
		msg += "\n- " + p.String()
	}
	return msg
}

// Install provisions the engine distro: preflight, verified download, import,
// then start.
//
// It is deliberately not idempotent over an existing distro — preflight refuses
// when the target is already registered, because that distro holds the user's
// images and volumes and silently reimporting would destroy them.
func (p *Provisioner) Install(ctx context.Context, opts Options) (*Manifest, error) {
	opts = opts.withDefaults()
	if opts.RootfsURL == "" {
		return nil, fmt.Errorf("install: RootfsURL is required")
	}

	report, err := p.Preflight(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, w := range report.Warnings {
		p.logger().Warn(w.Summary, "fix", w.Remedy)
	}
	if !report.OK {
		return nil, &PreflightError{Report: report}
	}

	rootfs := filepath.Join(opts.StateDir, "rootfs", filepath.Base(opts.RootfsURL))
	if err := p.fetchRootfs(ctx, opts.RootfsURL, opts.RootfsSHA256, rootfs); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating data dir %s: %w", opts.DataDir, err)
	}

	p.logger().Info("importing distro", "distro", opts.Distro, "dataDir", opts.DataDir)
	if err := p.wsl().Import(ctx, opts.Distro, opts.DataDir, rootfs); err != nil {
		return nil, fmt.Errorf("importing %s: %w", opts.Distro, err)
	}

	engineVersion := opts.EngineVersion
	if engineVersion == "" {
		// The rootfs records what it actually contains, which is more
		// trustworthy than a flag and is the only source available when the
		// rootfs came from --rootfs-url rather than the release manifest.
		engineVersion = p.engineVersionFromDistro(ctx, opts)
	}

	m := &Manifest{
		Distro:        opts.Distro,
		DataDir:       opts.DataDir,
		RootfsURL:     opts.RootfsURL,
		RootfsSHA256:  opts.RootfsSHA256,
		EngineVersion: engineVersion,
		InstalledAt:   time.Now().UTC(),
		WSLVersion:    report.Status.Version,
	}
	// Written before the engine starts: if start fails, uninstall still knows
	// what to clean up rather than leaving an orphaned distro behind.
	if err := p.writeManifest(opts, m); err != nil {
		return nil, err
	}

	if err := p.StartEngine(ctx, opts); err != nil {
		return m, fmt.Errorf("starting engine: %w", err)
	}
	return m, nil
}

// EngineVersionFile is where the rootfs records the engine it carries.
const EngineVersionFile = "/etc/hawser/engine-version"

// engineVersionFromDistro reads the version the rootfs declares. Best-effort:
// a rootfs without the marker is unusual but not a reason to fail an install,
// and `hawser version` reports an unknown engine rather than a wrong one.
func (p *Provisioner) engineVersionFromDistro(ctx context.Context, opts Options) string {
	out, err := p.wsl().Exec(ctx, opts.Distro, "root", "cat", EngineVersionFile)
	if err != nil {
		p.logger().Debug("rootfs declares no engine version",
			"file", EngineVersionFile, "error", err)
		return ""
	}
	return strings.TrimSpace(out)
}

// StartEngine launches dockerd and waits for its socket.
func (p *Provisioner) StartEngine(ctx context.Context, opts Options) error {
	opts = opts.withDefaults()

	if running, _ := p.engineRunning(ctx, opts); running {
		p.logger().Info("engine already running", "distro", opts.Distro)
		return nil
	}

	p.logger().Info("starting dockerd", "distro", opts.Distro)
	// Output goes to a log inside the distro; the caller gets it via
	// `hawser logs` rather than having it interleaved here.
	if _, err := p.wsl().Start(ctx, opts.Distro, "root",
		"sh", "-c", "dockerd >>/var/log/dockerd.log 2>&1"); err != nil {
		return fmt.Errorf("launching dockerd: %w", err)
	}

	deadline := time.Now().Add(opts.StartTimeout)
	for time.Now().Before(deadline) {
		if running, _ := p.engineRunning(ctx, opts); running {
			p.logger().Info("engine socket is up", "distro", opts.Distro)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}

	// Include the daemon's own last words; without them this is undiagnosable.
	log, _ := p.wsl().Exec(ctx, opts.Distro, "root", "tail", "-30", "/var/log/dockerd.log")
	return fmt.Errorf("dockerd did not create %s within %s. Last log lines:\n%s",
		EngineSocket, opts.StartTimeout, log)
}

func (p *Provisioner) engineRunning(ctx context.Context, opts Options) (bool, error) {
	_, err := p.wsl().Exec(ctx, opts.Distro, "root", "test", "-S", EngineSocket)
	return err == nil, err
}

// Uninstall removes the distro and Hawser's own state, and nothing else.
//
// Best-effort by design: a partially installed machine must still come clean,
// so a missing distro or absent state directory is not an error. Errors are
// collected and reported together.
func (p *Provisioner) Uninstall(ctx context.Context, opts Options) error {
	opts = opts.withDefaults()

	// A recorded manifest is more trustworthy than the caller's options: it says
	// where this install actually put things.
	if m, err := p.ReadManifest(opts); err == nil {
		if m.Distro != "" {
			opts.Distro = m.Distro
		}
		if m.DataDir != "" {
			opts.DataDir = m.DataDir
		}
	}

	var errs []error

	distros, err := p.wsl().List(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("listing distros: %w", err))
	}
	registered := false
	for _, d := range distros {
		if d.Name == opts.Distro {
			registered = true
		}
	}

	if registered {
		p.logger().Info("terminating distro", "distro", opts.Distro)
		if err := p.wsl().Terminate(ctx, opts.Distro); err != nil {
			// Not fatal: unregister stops it anyway.
			p.logger().Warn("terminate failed, continuing", "error", err)
		}
		p.logger().Info("unregistering distro", "distro", opts.Distro)
		if err := p.wsl().Unregister(ctx, opts.Distro); err != nil {
			errs = append(errs, fmt.Errorf("unregistering %s: %w", opts.Distro, err))
		}
	} else {
		p.logger().Info("distro not registered, nothing to unregister", "distro", opts.Distro)
	}

	// Only paths Hawser created. DataDir is removed because wsl --unregister
	// deletes the VHDX but leaves the directory.
	if err := os.RemoveAll(opts.DataDir); err != nil {
		errs = append(errs, fmt.Errorf("removing %s: %w", opts.DataDir, err))
	}
	if err := os.Remove(p.manifestPath(opts)); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("removing manifest: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("uninstall completed with errors: %w", errsJoin(errs))
	}
	return nil
}

func errsJoin(errs []error) error {
	msg := ""
	for i, e := range errs {
		if i > 0 {
			msg += "; "
		}
		msg += e.Error()
	}
	return fmt.Errorf("%s", msg)
}

func (p *Provisioner) manifestPath(opts Options) string {
	return filepath.Join(opts.withDefaults().StateDir, "manifest.json")
}

func (p *Provisioner) writeManifest(opts Options, m *Manifest) error {
	path := p.manifestPath(opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}
	return nil
}

// ReadManifest returns what a previous install recorded.
func (p *Provisioner) ReadManifest(opts Options) (*Manifest, error) {
	b, err := os.ReadFile(p.manifestPath(opts))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	return &m, nil
}
