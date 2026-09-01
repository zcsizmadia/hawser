// Package config is Hawser's user-tunable settings: a small JSON file in the
// state directory, edited through `hawser config` rather than by hand so
// every value is validated on the way in.
//
// Settings are read fresh at each use (the supervisor reads per tick), so a
// `hawser config set` takes effect without restarting anything.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// KeyIdleTimeout is how long the bridge must be quiet (no open connections,
// no running containers) before the supervisor stops the engine to return
// its RAM. "off" (the default) disables idle stops entirely.
const KeyIdleTimeout = "idle-timeout"

// path is the settings file inside the state dir.
func path(stateDir string) string {
	return filepath.Join(stateDir, "config.json")
}

// Config is the parsed, validated view.
type Config struct {
	// IdleTimeout of zero means idle stops are off.
	IdleTimeout time.Duration
}

// Load parses the settings file. A missing file is the default configuration,
// not an error; a corrupt one is an error, because silently reverting a
// user's settings to defaults is worse than telling them.
func Load(stateDir string) (Config, error) {
	raw, err := load(stateDir)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if v, ok := raw[KeyIdleTimeout]; ok {
		d, err := parseIdleTimeout(v)
		if err != nil {
			return Config{}, fmt.Errorf("config %s: %w", KeyIdleTimeout, err)
		}
		c.IdleTimeout = d
	}
	return c, nil
}

func load(stateDir string) (map[string]string, error) {
	b, err := os.ReadFile(path(stateDir))
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path(stateDir), err)
	}
	return m, nil
}

// validators parse-and-normalize each known key; unknown keys are refused so
// a typo ("idle-timout") fails loudly instead of configuring nothing.
var validators = map[string]func(string) (string, error){
	KeyIdleTimeout: func(v string) (string, error) {
		d, err := parseIdleTimeout(v)
		if err != nil {
			return "", err
		}
		if d == 0 {
			return "off", nil
		}
		return d.String(), nil
	},
}

func parseIdleTimeout(v string) (time.Duration, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "":
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration (try 20m, 1h30m, or off)", v)
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is negative", v)
	}
	if d < 10*time.Second {
		return 0, fmt.Errorf("%q is below the 10s minimum; use off to disable", v)
	}
	return d, nil
}

// Keys lists the settable keys, for help text.
func Keys() []string {
	ks := make([]string, 0, len(validators))
	for k := range validators {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Get returns the stored value for a known key, or its default.
func Get(stateDir, key string) (string, error) {
	if _, ok := validators[key]; !ok {
		return "", fmt.Errorf("unknown config key %q (known: %s)", key, strings.Join(Keys(), ", "))
	}
	raw, err := load(stateDir)
	if err != nil {
		return "", err
	}
	if v, ok := raw[key]; ok {
		return v, nil
	}
	return defaultFor(key), nil
}

func defaultFor(key string) string {
	switch key {
	case KeyIdleTimeout:
		return "off"
	}
	return ""
}

// Set validates and stores one key, atomically.
func Set(stateDir, key, value string) error {
	validate, ok := validators[key]
	if !ok {
		return fmt.Errorf("unknown config key %q (known: %s)", key, strings.Join(Keys(), ", "))
	}
	normalized, err := validate(value)
	if err != nil {
		return err
	}
	raw, err := load(stateDir)
	if err != nil {
		return err
	}
	raw[key] = normalized

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := path(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Rename(tmp, path(stateDir)); err != nil {
		return fmt.Errorf("committing config: %w", err)
	}
	return nil
}

// All returns every stored setting plus defaults for unset known keys.
func All(stateDir string) (map[string]string, error) {
	raw, err := load(stateDir)
	if err != nil {
		return nil, err
	}
	for _, k := range Keys() {
		if _, ok := raw[k]; !ok {
			raw[k] = defaultFor(k)
		}
	}
	return raw, nil
}
