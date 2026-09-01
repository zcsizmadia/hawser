// Command hawser runs the upstream Docker Engine on Windows via WSL2:
// a provisioner, a named-pipe bridge, and a supervisor in one binary.
package main

import (
	"fmt"
	"os"
)

// version is stamped by the release build (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("hawser %s\n", version)
		return
	}
	fmt.Fprintln(os.Stderr, "hawser: pre-alpha — see PLAN.md and ROADMAP.md")
	os.Exit(1)
}
