package release_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readVersionsEnv parses guest/rootfs/versions.env so the embedded manifest can
// be checked against the pins the rootfs is actually built from. Keeping the
// two in step by hand is exactly the kind of thing that drifts silently.
func readVersionsEnv(t *testing.T) map[string]string {
	t.Helper()

	// The test binary runs in the package directory; walk up to the repo root.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	var path string
	for i := 0; i < 5; i++ {
		candidate := filepath.Join(dir, "guest", "rootfs", "versions.env")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
		dir = filepath.Dir(dir)
	}
	if path == "" {
		t.Skip("guest/rootfs/versions.env not found; skipping cross-check")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		// Strip a trailing comment, then quotes.
		if i := strings.Index(value, " #"); i >= 0 {
			value = value[:i]
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return out
}
