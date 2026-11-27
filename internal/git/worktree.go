// Package git discovers worktrees with `git worktree list --porcelain -z`.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Worktree is one record from `git worktree list --porcelain -z`.
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short name, empty for a detached or bare worktree
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// ParseWorktreeList parses `git worktree list --porcelain -z` output. Each
// attribute is a NUL-terminated field, and an empty field ends a record. The
// -z form leaves values unquoted, so a path containing a newline survives
// intact. Unknown attributes are ignored so newer Git versions keep working
// with older rexws builds.
func ParseWorktreeList(data []byte) ([]Worktree, error) {
	var (
		worktrees []Worktree
		current   *Worktree
	)

	fieldNo := 0
	for _, field := range bytes.Split(data, []byte{0}) {
		if len(field) == 0 {
			if current != nil {
				worktrees = append(worktrees, *current)
				current = nil
			}
			continue
		}
		fieldNo++

		key, value, _ := strings.Cut(string(field), " ")
		if key == "worktree" {
			if current != nil {
				return nil, fmt.Errorf("field %d: worktree record has no empty-field separator", fieldNo)
			}
			if value == "" {
				return nil, fmt.Errorf("field %d: worktree path is empty", fieldNo)
			}
			current = &Worktree{Path: value}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("field %d: %q appears before the worktree path", fieldNo, key)
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
	if current != nil {
		worktrees = append(worktrees, *current)
	}
	return worktrees, nil
}

// worktreeListZMajor and worktreeListZMinor are the first Git release whose
// `worktree list` supports -z.
const (
	worktreeListZMajor = 2
	worktreeListZMinor = 36
)

// ListWorktrees runs `git worktree list --porcelain -z` in repoPath.
func ListWorktrees(ctx context.Context, repoPath string) ([]Worktree, error) {
	out, err := runGit(ctx, repoPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, explainWorktreeListError(ctx, repoPath, err)
	}
	return ParseWorktreeList(out)
}

// explainWorktreeListError adds an actionable requirement when the installed
// Git predates -z, instead of leaving only "unknown option 'z'".
func explainWorktreeListError(ctx context.Context, repoPath string, err error) error {
	major, minor, ok := gitVersion(ctx, repoPath)
	if !ok || major > worktreeListZMajor ||
		(major == worktreeListZMajor && minor >= worktreeListZMinor) {
		return err
	}
	return fmt.Errorf("%w; rexws requires Git 2.36 or newer for `git worktree list --porcelain -z`", err)
}

// gitVersion reads the running Git's major and minor version.
func gitVersion(ctx context.Context, repoPath string) (major, minor int, ok bool) {
	out, err := runGit(ctx, repoPath, "version")
	if err != nil {
		return 0, 0, false
	}
	return parseGitVersion(string(out))
}

// parseGitVersion accepts `git version 2.36.0` and vendor variants such as
// `git version 2.39.5 (Apple Git-154)`.
func parseGitVersion(out string) (major, minor int, ok bool) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return 0, 0, false
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
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

// TopLevel returns the absolute path of the worktree containing dir. It fails
// when dir is not inside a Git worktree, which is how state reconciliation
// proves a path is not a worktree.
func TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ShortHead returns the abbreviated HEAD commit, used to name detached
// workstreams.
func (w Worktree) ShortHead() string {
	if len(w.Head) > 7 {
		return w.Head[:7]
	}
	return w.Head
}

// CheckBranchName asks Git whether branch is a valid branch name.
func CheckBranchName(ctx context.Context, repoPath, branch string) error {
	if branch == "" {
		return errors.New("branch name is empty")
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	if _, err := runGit(ctx, repoPath, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid branch name %q: %w", branch, err)
	}
	return nil
}

// WorktreesForBranch returns the non-bare, non-prunable worktrees checked out
// on branch.
func WorktreesForBranch(worktrees []Worktree, branch string) []Worktree {
	var out []Worktree
	for _, wt := range worktrees {
		if wt.Bare || wt.Prunable {
			continue
		}
		if wt.Branch == branch {
			out = append(out, wt)
		}
	}
	return out
}

// IsDirty reports whether the worktree has uncommitted or untracked changes.
func IsDirty(ctx context.Context, worktreePath string) (bool, error) {
	lines, err := StatusLines(ctx, worktreePath)
	if err != nil {
		return false, err
	}
	return len(lines) > 0, nil
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
