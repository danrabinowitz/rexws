package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Commit is one commit's identity and subject, used to name what a forced
// cleanup would discard.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// DefaultBranch returns the ref that merged and patch-equivalent branches are
// compared against. It prefers origin/HEAD's target, then a local main or
// master, then the matching remote-tracking ref. The returned value is a ref
// name such as "main" or "origin/main".
func DefaultBranch(ctx context.Context, repoPath string) (string, error) {
	if out, err := runGit(ctx, repoPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		remote := strings.TrimSpace(string(out))
		if remote != "" {
			// Prefer the local branch the remote HEAD points at, matching
			// git-worktree-prune-safe. An unpushed local main is still the
			// branch the developer calls the default.
			if local := strings.TrimPrefix(remote, "origin/"); local != remote && refExists(ctx, repoPath, local) {
				return local, nil
			}
			if refExists(ctx, repoPath, remote) {
				return remote, nil
			}
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if refExists(ctx, repoPath, candidate) {
			return candidate, nil
		}
	}
	for _, candidate := range []string{"origin/main", "origin/master"} {
		if refExists(ctx, repoPath, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s: no default branch found (checked origin/HEAD, main, master, origin/main, origin/master)", repoPath)
}

func refExists(ctx context.Context, repoPath, ref string) bool {
	_, err := runGit(ctx, repoPath, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// ListBranches returns local branch names in stable order.
func ListBranches(ctx context.Context, repoPath string) ([]string, error) {
	out, err := runGit(ctx, repoPath, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	branches := splitLines(out)
	sort.Strings(branches)
	return branches, nil
}

// BranchSHA returns the commit a branch points at.
func BranchSHA(ctx context.Context, repoPath, branch string) (string, error) {
	out, err := runGit(ctx, repoPath, "rev-parse", "--verify", branch+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// IsAncestor reports whether ancestor is reachable from descendant. A Git exit
// status of 1 means "no"; any other failure is an error.
func IsAncestor(ctx context.Context, repoPath, ancestor, descendant string) (bool, error) {
	_, err := runGit(ctx, repoPath, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// PatchEquivalent reports whether merging branch into base would change base at
// all. That is the second pass of git-cleanbranches: it catches branches merged
// upstream by rebase or cherry-pick, whose commit IDs differ.
//
// It intentionally uses merge-tree rather than `git cherry`. cherry ignores
// merge commits and matches patch IDs, so it calls an evil merge (unique
// conflict-resolution content) and an upstream-reverted patch "equivalent",
// although deleting those branches loses the only copy of that content.
// merge-tree requires a clean merge and an unchanged base tree, which rejects
// both. The merge writes unreachable objects; it changes no refs, worktrees, or
// branches.
func PatchEquivalent(ctx context.Context, repoPath, base, branch string) (bool, error) {
	baseTree, err := runGit(ctx, repoPath, "rev-parse", "--verify", base+"^{tree}")
	if err != nil {
		return false, err
	}

	// An unrelated history has no merge base. There is no tree comparison that
	// proves anything about it, so callers must treat it as unknown rather than
	// unmerged.
	if _, err := runGit(ctx, repoPath, "merge-base", base, branch); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, fmt.Errorf("base %s and branch %s have no common ancestor", base, branch)
		}
		return false, err
	}

	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "merge-tree", "--write-tree", "--no-messages", base, branch)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			// Conflicts: the branch cannot be proven to contribute nothing to
			// base. It stays unmerged and is never deleted without --force.
			return false, nil
		}
		message := strings.TrimSpace(stderr.String())
		tooOld := false
		if errors.As(err, &exit) && exit.ExitCode() == 129 {
			tooOld = true
		}
		if tooOld || strings.Contains(message, "unknown option") || strings.Contains(message, "usage: git merge-tree") {
			return false, fmt.Errorf("patch-equivalence requires Git 2.38 or newer (git merge-tree --write-tree): %s", message)
		}
		if message != "" {
			return false, fmt.Errorf("git merge-tree %s %s: %w: %s", base, branch, err, message)
		}
		return false, fmt.Errorf("git merge-tree %s %s: %w", base, branch, err)
	}

	lines := splitLines(stdout.Bytes())
	if len(lines) == 0 {
		return false, errors.New("git merge-tree returned no tree")
	}
	return lines[0] == strings.TrimSpace(string(baseTree)), nil
}

// CommitsOutside returns the commits reachable from branch but not base.
func CommitsOutside(ctx context.Context, repoPath, base, branch string) ([]Commit, error) {
	out, err := runGit(ctx, repoPath, "log", "--format=%H%x09%s", base+".."+branch)
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

// UnpushedCommits returns the commits the branch adds beyond base that are not
// reachable from any remote-tracking ref. "Unpushed" therefore means commits
// new on this local branch that no remote has, not the whole shared history. A
// branch merged into base has none.
func UnpushedCommits(ctx context.Context, repoPath, base, branch string) ([]Commit, error) {
	// --not applies to everything after it: branch minus base minus every
	// refs/remotes/* ref. Without a remote, every commit the branch adds is
	// unpushed.
	out, err := runGit(ctx, repoPath, "log", "--format=%H%x09%s", branch, "--not", base, "--remotes")
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

func parseCommits(out []byte) []Commit {
	var commits []Commit
	for _, line := range splitLines(out) {
		sha, subject, _ := strings.Cut(line, "\t")
		if sha == "" {
			continue
		}
		commits = append(commits, Commit{SHA: sha, Subject: subject})
	}
	return commits
}

// CommitsNotInRefs returns commits reachable from rev but from no local branch,
// remote-tracking ref, or tag. It is the detached-HEAD loss check: these
// commits have no ref other than HEAD, so removing the worktree strands them.
func CommitsNotInRefs(ctx context.Context, repoPath, rev string) ([]Commit, error) {
	// --not applies to the ref groups after it. Do not use --all: it includes
	// HEAD, which would make the result empty even for unique commits.
	out, err := runGit(ctx, repoPath, "log", "--format=%H%x09%s", rev, "--not", "--branches", "--tags", "--remotes")
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

// StatusLines returns the worktree's porcelain status lines: one entry per
// modified or untracked path. Ignored files are not included. Callers that
// delete a directory, such as rm and cleanup, use
// StatusLinesIncludingIgnored instead.
func StatusLines(ctx context.Context, worktreePath string) ([]string, error) {
	return statusLines(ctx, worktreePath)
}

// StatusLinesIncludingIgnored returns modified, untracked, and ignored paths.
// Cleanup uses it because `git worktree remove` deletes ignored files too, so
// a worktree holding only an ignored .env is not safe to remove.
func StatusLinesIncludingIgnored(ctx context.Context, worktreePath string) ([]string, error) {
	return statusLines(ctx, worktreePath, "--ignored=matching")
}

func statusLines(ctx context.Context, worktreePath string, extra ...string) ([]string, error) {
	args := append([]string{"status", "--porcelain"}, extra...)
	out, err := runGit(ctx, worktreePath, args...)
	if err != nil {
		return nil, err
	}
	lines := splitLines(out)
	sort.Strings(lines)
	return lines, nil
}

// RemoveWorktree removes a registered worktree. force also discards dirty and
// untracked files; callers must have reported the loss first.
func RemoveWorktree(ctx context.Context, repoPath, worktreePath string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktreePath)
	_, err := runGit(ctx, repoPath, args...)
	return err
}

// PruneWorktrees drops bookkeeping for worktrees whose directories are gone.
func PruneWorktrees(ctx context.Context, repoPath string) error {
	_, err := runGit(ctx, repoPath, "worktree", "prune")
	return err
}

// DeleteBranch deletes a local branch. force maps to git branch -D; callers
// use it only after proving the branch is merged or patch-equivalent, or after
// the user accepted an unmerged branch's loss.
func DeleteBranch(ctx context.Context, repoPath, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := runGit(ctx, repoPath, "branch", flag, "--", branch)
	return err
}

// DeleteBranchAt atomically deletes branch only if it still points at
// expectedSHA. It closes the window between a fresh proof and the deletion.
func DeleteBranchAt(ctx context.Context, repoPath, branch, expectedSHA string) error {
	if expectedSHA == "" || strings.Trim(expectedSHA, "0") == "" {
		// git treats an all-zero old value as "the ref must not exist", which
		// would delete unconditionally. Require a real commit id.
		return errors.New("DeleteBranchAt requires a non-zero expected commit")
	}
	_, err := runGit(ctx, repoPath, "update-ref", "-d", "refs/heads/"+branch, expectedSHA)
	return err
}

// ForgetBranchConfig best-effort removes branch.<name> config after an atomic
// ref deletion, so a recreated branch does not inherit stale upstream settings.
// The ref is already gone; a missing section is not an error.
func ForgetBranchConfig(ctx context.Context, repoPath, branch string) {
	_, _ = runGit(ctx, repoPath, "config", "--remove-section", "branch."+branch)
}

func splitLines(out []byte) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}
