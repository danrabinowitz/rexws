// Package pathutil holds small helpers for working with worktree paths.
package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Canonical returns an absolute, symlink-resolved path. It fails if the path
// does not exist, so callers never persist a state key they cannot revisit.
func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	return resolved, nil
}

// ExpandHome replaces a leading ~ or ~/ with the user's home directory. Any
// other path is returned unchanged.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[len("~/"):])
}
