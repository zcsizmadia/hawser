package migrate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type fakeEngine struct {
	images  []Image
	volumes []Volume
	imgErr  error
	volErr  error
}

func (f *fakeEngine) Images(context.Context) ([]Image, error)   { return f.images, f.imgErr }
func (f *fakeEngine) Volumes(context.Context) ([]Volume, error) { return f.volumes, f.volErr }

type fakeTransfer struct {
	hasImages  map[string]bool
	hasVolumes map[string]bool
	movedImgs  [][]string
	movedVols  []string
	moveImgErr error
	moveVolErr map[string]error
}

func (f *fakeTransfer) HasImage(_ context.Context, ref string) (bool, error) {
	return f.hasImages[ref], nil
}
func (f *fakeTransfer) HasVolume(_ context.Context, name string) (bool, error) {
	return f.hasVolumes[name], nil
}
func (f *fakeTransfer) MoveImages(_ context.Context, refs []string) error {
	if f.moveImgErr != nil {
		return f.moveImgErr
	}
	f.movedImgs = append(f.movedImgs, refs)
	return nil
}
func (f *fakeTransfer) MoveVolume(_ context.Context, name string) error {
	if err := f.moveVolErr[name]; err != nil {
		return err
	}
	f.movedVols = append(f.movedVols, name)
	return nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newMigrator(src *fakeEngine, tr *fakeTransfer) *Migrator {
	return &Migrator{Source: src, Dest: &fakeEngine{}, Transfer: tr, Logger: quiet()}
}

func TestPlanSkipsExistingAndDangling(t *testing.T) {
	src := &fakeEngine{
		images: []Image{
			{Ref: "app:v1", ID: "a", Size: 100},
			{Ref: "already:here", ID: "b", Size: 50},
			{Ref: "<none>:<none>", ID: "c", Size: 30}, // dangling
		},
		volumes: []Volume{
			{Name: "data", Size: 200},
			{Name: "cached", Size: 10},
		},
	}
	tr := &fakeTransfer{
		hasImages:  map[string]bool{"already:here": true},
		hasVolumes: map[string]bool{"cached": true},
	}
	p, err := newMigrator(src, tr).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Images) != 1 || p.Images[0].Ref != "app:v1" {
		t.Errorf("Images = %+v, want just app:v1", p.Images)
	}
	if len(p.SkippedImgs) != 1 || p.Unreferenced != 1 {
		t.Errorf("skipped=%d unreferenced=%d, want 1/1", len(p.SkippedImgs), p.Unreferenced)
	}
	if len(p.Volumes) != 1 || p.Volumes[0].Name != "data" {
		t.Errorf("Volumes = %+v, want just data", p.Volumes)
	}
	if got := p.TotalBytes(); got != 300 {
		t.Errorf("TotalBytes = %d, want 300 (100 image + 200 volume)", got)
	}
}

func TestPlanOrdersLargestFirst(t *testing.T) {
	src := &fakeEngine{images: []Image{
		{Ref: "small:1", Size: 10},
		{Ref: "big:1", Size: 1000},
		{Ref: "mid:1", Size: 100},
	}}
	p, _ := newMigrator(src, &fakeTransfer{}).Plan(context.Background())
	if p.Images[0].Ref != "big:1" || p.Images[2].Ref != "small:1" {
		t.Errorf("not largest-first: %+v", p.Images)
	}
}

func TestPlanPropagatesSourceErrors(t *testing.T) {
	src := &fakeEngine{imgErr: errors.New("desktop down")}
	if _, err := newMigrator(src, &fakeTransfer{}).Plan(context.Background()); err == nil {
		t.Fatal("Plan hid a source enumeration error")
	}
}

func TestRunMovesAndReports(t *testing.T) {
	src := &fakeEngine{
		images:  []Image{{Ref: "app:v1", Size: 100}, {Ref: "web:v1", Size: 50}},
		volumes: []Volume{{Name: "data", Size: 200}},
	}
	tr := &fakeTransfer{}
	m := newMigrator(src, tr)
	p, _ := m.Plan(context.Background())
	r := m.Run(context.Background(), p)

	if r.ImagesMoved != 2 || r.VolumesMoved != 1 || len(r.Errors) != 0 {
		t.Fatalf("result = %+v, want 2 images / 1 volume / no errors", r)
	}
	// Images move in ONE save|load batch (shared layers cross once).
	if len(tr.movedImgs) != 1 || len(tr.movedImgs[0]) != 2 {
		t.Errorf("images not batched into one transfer: %+v", tr.movedImgs)
	}
}

func TestRunContinuesPastAVolumeFailure(t *testing.T) {
	src := &fakeEngine{volumes: []Volume{
		{Name: "good1", Size: 1}, {Name: "bad", Size: 1}, {Name: "good2", Size: 1},
	}}
	tr := &fakeTransfer{moveVolErr: map[string]error{"bad": errors.New("tar broke")}}
	m := newMigrator(src, tr)
	p, _ := m.Plan(context.Background())
	r := m.Run(context.Background(), p)

	if r.VolumesMoved != 2 || len(r.Errors) != 1 {
		t.Errorf("result = %+v; a partial migration must move the good ones and report the bad", r)
	}
}

func TestRunImageBatchFailureDoesNotStopVolumes(t *testing.T) {
	src := &fakeEngine{
		images:  []Image{{Ref: "app:v1", Size: 1}},
		volumes: []Volume{{Name: "data", Size: 1}},
	}
	tr := &fakeTransfer{moveImgErr: errors.New("load failed")}
	m := newMigrator(src, tr)
	p, _ := m.Plan(context.Background())
	r := m.Run(context.Background(), p)

	if r.ImagesMoved != 0 || r.VolumesMoved != 1 || len(r.Errors) != 1 {
		t.Errorf("result = %+v; a failed image batch must not strand volumes", r)
	}
}

func TestByteSize(t *testing.T) {
	cases := map[int64]string{
		-1:              "unknown",
		512:             "512 B",
		2048:            "2.0 KiB",
		5 * 1024 * 1024: "5.0 MiB",
	}
	for in, want := range cases {
		if got := ByteSize(in); got != want {
			t.Errorf("ByteSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestVolumeUnknownSizeNotCountedInTotal(t *testing.T) {
	src := &fakeEngine{volumes: []Volume{{Name: "v", Size: -1}}}
	p, _ := newMigrator(src, &fakeTransfer{}).Plan(context.Background())
	if p.TotalBytes() != 0 {
		t.Errorf("unknown-size volume polluted the total: %d", p.TotalBytes())
	}
	if len(p.Volumes) != 1 {
		t.Error("unknown-size volume should still be migrated")
	}
}

func TestOnlyFilterScopesImagesAndVolumes(t *testing.T) {
	src := &fakeEngine{
		images: []Image{
			{Ref: "postgres:17", Size: 100},
			{Ref: "redis:7", Size: 50},
			{Ref: "myco/postgres-tools:1", Size: 30},
		},
		volumes: []Volume{
			{Name: "pgdata", Size: 200},
			{Name: "rediscache", Size: 10},
		},
	}
	m := newMigrator(src, &fakeTransfer{})
	m.Only = []string{"postgres", "pgdata"}

	p, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Images) != 2 {
		t.Errorf("filtered images = %+v, want the two postgres refs", p.Images)
	}
	for _, img := range p.Images {
		if !contains(img.Ref, "postgres") {
			t.Errorf("unfiltered image slipped through: %s", img.Ref)
		}
	}
	if len(p.Volumes) != 1 || p.Volumes[0].Name != "pgdata" {
		t.Errorf("filtered volumes = %+v, want just pgdata", p.Volumes)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
