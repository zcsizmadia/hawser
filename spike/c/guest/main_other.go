//go:build !linux

// Keeps `go build ./...` working in CI (windows runner); the listener itself
// is linux-only.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "spike c guest is linux-only; build with GOOS=linux")
	os.Exit(2)
}
