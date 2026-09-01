package migrate

import "testing"

// Runtime helpers so the expected sizes are not untyped constants (Go rejects
// converting a fractional constant float to int64).
func mib(f float64) int64 { return int64(f * float64(int64(1)<<20)) }
func gib(f float64) int64 { return int64(f * float64(int64(1)<<30)) }

func TestParseHumanSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", -1},
		{"N/A", -1},
		{"0B", 0},
		{"512B", 512},
		{"1KB", 1024},
		{"1.5MB", mib(1.5)},
		{"2GB", 2 << 30},
		{"1.2GB", gib(1.2)},
	}
	for _, c := range cases {
		if got := parseHumanSize(c.in); got != c.want {
			t.Errorf("parseHumanSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestArgsIncludeContext(t *testing.T) {
	d := DockerCLI{Context: "desktop-linux"}
	got := d.args("images", "-q")
	want := []string{"--context", "desktop-linux", "images", "-q"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if a := (DockerCLI{}).args("ps"); len(a) != 1 || a[0] != "ps" {
		t.Errorf("contextless args = %v, want [ps]", a)
	}
}
