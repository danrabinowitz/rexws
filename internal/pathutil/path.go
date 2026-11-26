// Package pathutil holds small helpers for working with worktree paths.
package pathutil

import (
	"fmt"
	"path/filepath"
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
