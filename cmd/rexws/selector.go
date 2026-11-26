package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/pathutil"
	"github.com/danrabinowitz/rexws/internal/selector"
)

// resolveError keeps the project/branch hints for a selector that matched no
// workstream, while preserving selector.ErrNoMatch for exit codes.
func resolveError(cfg config.Config, input string, err error) error {
	if !errors.Is(err, selector.ErrNoMatch) {
		return err
	}
	projectName, branch, split := strings.Cut(input, "/")
	if !split || projectName == "" || branch == "" ||
		strings.Contains(input, ":") ||
		strings.HasPrefix(input, "/") || strings.HasPrefix(input, ".") || strings.HasPrefix(input, "~") {
		return err
	}
	// An existing path that is not a worktree is a path-selection miss, not a
	// missing <project>/<branch>.
	if _, pathErr := pathutil.Canonical(input); pathErr == nil {
		return err
	}
	if _, ok := cfg.Project(projectName); ok {
		return hintError{
			message: fmt.Sprintf(
				"project %q has no worktree for branch %q; create it with `rexws new %s %s`",
				projectName, branch, projectName, branch),
			cause: err,
		}
	}
	return hintError{
		message: fmt.Sprintf("no registered project %q (registered: %s)", projectName, projectNames(cfg)),
		cause:   err,
	}
}

// hintError replaces a generic no-match message with a command hint while
// keeping both the sentinel and the typed cause reachable for errors.Is and
// errors.As.
type hintError struct {
	message string
	cause   error
}

func (e hintError) Error() string { return e.message }

// Unwrap exposes selector.ErrNoMatch and the original error.
func (e hintError) Unwrap() []error {
	if e.cause == nil {
		return []error{selector.ErrNoMatch}
	}
	return []error{selector.ErrNoMatch, e.cause}
}

func projectNames(cfg config.Config) string {
	names := make([]string, 0, len(cfg.Projects))
	for _, project := range cfg.SortedProjects() {
		names = append(names, project.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
