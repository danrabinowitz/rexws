package config

import (
	"os"
	"path/filepath"
)

// EnvDir overrides the rexws configuration directory. It exists for tests and
// for running against a scratch directory; normal use reads ~/.config/rexws.
const EnvDir = "REXWS_CONFIG_DIR"

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
