// Package git discovers worktrees with `git worktree list --porcelain`.
package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Worktree is one record from `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short name, empty for a detached or bare worktree
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// ParseWorktreeList parses porcelain output. Unknown attributes are ignored so
// newer Git versions keep working with older rexws builds.
func ParseWorktreeList(data []byte) ([]Worktree, error) {
	var (
		worktrees []Worktree
		current   *Worktree
	)

	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if current != nil {
				worktrees = append(worktrees, *current)
				current = nil
			}
			continue
		}

		key, value, _ := strings.Cut(line, " ")
		if key == "worktree" {
			if current != nil {
				return nil, fmt.Errorf("line %d: worktree record has no blank-line separator", lineNo)
			}
			if value == "" {
				return nil, fmt.Errorf("line %d: worktree path is empty", lineNo)
			}
			current = &Worktree{Path: value}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("line %d: %q appears before the worktree path", lineNo, key)
		}

		switch key {
		case "HEAD":
			current.Head = value
		case "branch":
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "bare":
			current.Bare = true
		case "detached":
			current.Detached = true
		case "locked":
			current.Locked = true
		case "prunable":
			current.Prunable = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse worktree list: %w", err)
	}
	if current != nil {
		worktrees = append(worktrees, *current)
	}
	return worktrees, nil
}

// ListWorktrees runs `git worktree list --porcelain` in repoPath.
func ListWorktrees(ctx context.Context, repoPath string) ([]Worktree, error) {
	out, err := runGit(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return ParseWorktreeList(out)
}

// ProjectRoot resolves any path inside a repository or its worktrees to the
// main worktree. Registering a linked worktree by mistake therefore registers
// the repository it belongs to.
func ProjectRoot(ctx context.Context, path string) (string, error) {
	worktrees, err := ListWorktrees(ctx, path)
	if err != nil {
		return "", err
	}
	if len(worktrees) == 0 {
		return "", fmt.Errorf("%s: git reported no worktrees", path)
	}
	if worktrees[0].Bare {
		return "", fmt.Errorf("%s is a bare repository; rexws needs a checkout", path)
	}
	return worktrees[0].Path, nil
}

// ShortHead returns the abbreviated HEAD commit, used to name detached
// workstreams.
func (w Worktree) ShortHead() string {
	if len(w.Head) > 7 {
		return w.Head[:7]
	}
	return w.Head
}

func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("git %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return out, nil
}
