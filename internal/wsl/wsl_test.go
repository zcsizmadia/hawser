package wsl_test

import (
	"context"
	"testing"

	"github.com/zcsizmadia/hawser/internal/wsl"
)

// fake proves the interface is implementable without wsl.exe — the pattern
// every consumer test will use.
type fake struct{ distros []string }

func (f *fake) Status(context.Context) (wsl.Status, error) {
	return wsl.Status{Installed: true, DefaultVersion: 2, Version: "2.7.8.0"}, nil
}
func (f *fake) Import(_ context.Context, distro, _, _ string) error {
	f.distros = append(f.distros, distro)
	return nil
}
func (f *fake) Unregister(context.Context, string) error { return nil }
func (f *fake) Terminate(context.Context, string) error  { return nil }
func (f *fake) List(context.Context) ([]string, error)   { return f.distros, nil }

var _ wsl.WSL = (*fake)(nil)

func TestFakeRoundTrip(t *testing.T) {
	var w wsl.WSL = &fake{}
	ctx := context.Background()

	st, err := w.Status(ctx)
	if err != nil || !st.Installed || st.DefaultVersion != 2 {
		t.Fatalf("Status() = %+v, %v", st, err)
	}
	if err := w.Import(ctx, "hawser-engine", `C:\data`, `C:\rootfs.tar`); err != nil {
		t.Fatalf("Import() = %v", err)
	}
	got, err := w.List(ctx)
	if err != nil || len(got) != 1 || got[0] != "hawser-engine" {
		t.Fatalf("List() = %v, %v", got, err)
	}
}
