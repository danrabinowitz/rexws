// Package ops holds the operations shared by the rexws CLI and TUI so both
// frontends resolve projects, Rex state, and mutations the same way.
package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// ProjectError is one project whose worktrees could not be listed. The other
// projects remain usable.
type ProjectError struct {
	Project string
	Err     error
}

func (e ProjectError) Error() string { return fmt.Sprintf("project %s: %v", e.Project, e.Err) }

// Unwrap exposes the underlying Git error.
func (e ProjectError) Unwrap() error { return e.Err }

// Discovery is the worktree list for every project that could be read.
type Discovery struct {
	Inputs []core.Input
	Errors []ProjectError
}

// Err joins the per-project errors. It is nil when every project was read.
func (d Discovery) Err() error {
	if len(d.Errors) == 0 {
		return nil
	}
	errs := make([]error, len(d.Errors))
	for i, projectErr := range d.Errors {
		errs[i] = projectErr
	}
	return errors.Join(errs...)
}

// Discover lists the worktrees of every registered project. A project that
// cannot be read is reported in Errors and does not hide the others.
func Discover(ctx context.Context, cfg config.Config) Discovery {
	discovery := Discovery{Inputs: make([]core.Input, 0, len(cfg.Projects))}
	for _, project := range cfg.SortedProjects() {
		worktrees, err := git.ListWorktrees(ctx, project.Path)
		if err != nil {
			discovery.Errors = append(discovery.Errors, ProjectError{Project: project.Name, Err: err})
			continue
		}
		discovery.Inputs = append(discovery.Inputs, core.Input{Project: project, Worktrees: worktrees})
	}
	return discovery
}

// Worktree finds a non-bare, non-prunable worktree by canonical path.
func (d Discovery) Worktree(path string) (config.Project, git.Worktree, bool, error) {
	for _, input := range d.Inputs {
		for _, wt := range input.Worktrees {
			if wt.Bare || wt.Prunable {
				continue
			}
			candidate, err := pathutil.Canonical(wt.Path)
			if err != nil {
				return config.Project{}, git.Worktree{}, false, err
			}
			if candidate == path {
				return input.Project, wt, true, nil
			}
		}
	}
	return config.Project{}, git.Worktree{}, false, nil
}

// notFoundError explains a path that is not a discovered worktree, including
// any project discovery errors that might explain why it was not found.
func notFoundError(discovery Discovery, path string) error {
	if err := discovery.Err(); err != nil {
		return fmt.Errorf("%s is not a worktree of a readable registered project: %w", path, err)
	}
	return fmt.Errorf("%s is not a worktree of a registered project; run `rexws list` to see known paths", path)
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
