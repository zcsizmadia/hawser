package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/zcsizmadia/hawser/internal/config"
	"github.com/zcsizmadia/hawser/internal/provision"
)

func runConfig(args []string) int {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	stateDir := fs.String("state-dir", "", "override Hawser's state directory")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: hawser config                    list all settings
       hawser config get <key>          print one value
       hawser config set <key> <value>  change one value

Settings apply live: the supervisor re-reads them every few seconds, so no
restart is needed.

keys:
  %s   how long the bridge must be quiet (no connections, no running
                 containers) before the engine is stopped to reclaim its RAM;
                 the next docker command starts it again. A duration like 20m
                 or 1h, or "off" (the default).

Exit codes: 0 ok, %d error, %d usage.
`, config.KeyIdleTimeout, exitError, exitUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	opts := optsWithResolvedStateDir(provision.Options{StateDir: *stateDir})
	rest := fs.Args()

	switch {
	case len(rest) == 0:
		all, err := config.All(opts.StateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hawser: %v\n", err)
			return exitError
		}
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%s = %s\n", k, all[k])
		}
		return exitOK

	case rest[0] == "get" && len(rest) == 2:
		v, err := config.Get(opts.StateDir, rest[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "hawser: %v\n", err)
			return exitError
		}
		fmt.Println(v)
		return exitOK

	case rest[0] == "set" && len(rest) == 3:
		if err := config.Set(opts.StateDir, rest[1], rest[2]); err != nil {
			fmt.Fprintf(os.Stderr, "hawser: %v\n", err)
			return exitError
		}
		v, _ := config.Get(opts.StateDir, rest[1])
		fmt.Printf("%s = %s\n", rest[1], v)
		return exitOK

	default:
		fmt.Fprintf(os.Stderr, "hawser: config %s: unrecognized; see `hawser config --help`\n",
			strings.Join(rest, " "))
		return exitUsage
	}
}
