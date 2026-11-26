package config

import (
	"os"
	"path/filepath"
)

// Environment overrides. EnvDir exists for tests and for running against a
// scratch directory; the Rex values are read at call time so they are never
// written back to config.json.
const (
	EnvDir       = "REXWS_CONFIG_DIR"
	EnvRexBin    = "REXWS_REX_BIN"
	EnvRexClient = "REXWS_REX_CLIENT"
)

// Dir returns the directory that holds config.json and state.json.
func Dir() (string, error) {
	if dir := os.Getenv(EnvDir); dir != "" {
		return dir, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "rexws"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "rexws"), nil
}

// ConfigPath returns the path of config.json.
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// StatePath returns the path of state.json.
func StatePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}
