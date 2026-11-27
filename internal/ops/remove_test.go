package ops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
)

// emptyRex makes PrepareRemove's session pre-check succeed with no live
// sessions.
func emptyRex(t *testing.T) {
	t.Helper()
	writeFakeRex(t, `case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[]}' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)
}

func commitIn(t *testing.T, dir, file, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "--", file)
	gitRun(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", message)
	return runGitOutput(t, dir, "rev-parse", "HEAD")
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// removeFixture creates a repository with a worktree on branch. The worktree
// starts clean at the default branch's HEAD.
func removeFixture(t *testing.T, branch string) (repo, target, targetPath string, cfg config.Config) {
	t.Helper()
	repo = testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg = testConfig(t, repo, root)
	target = filepath.Join(root, branch)
	gitRun(t, repo, "worktree", "add", "-q", "-b", branch, target)
	targetPath = canonical(t, target)
	emptyRex(t)
	return repo, target, targetPath, cfg
}

func TestPrepareRemoveForceReportsExactLoss(t *testing.T) {
	_, target, _, cfg := removeFixture(t, "feat/loss")

	// Commit a file and .gitignore on the branch so the branch is unmerged and
	// the ignored file is ignored in the worktree.
	if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "tracked.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, target, "add", ".gitignore", "tracked.txt")
	gitRun(t, target, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "branch work")
	if err := os.WriteFile(filepath.Join(target, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "staged.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, target, "add", "staged.txt")
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "ignored.txt"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := PrepareRemove(context.Background(), cfg, target); err == nil || !strings.Contains(err.Error(), "uncommitted or untracked") {
		t.Fatalf("dirty refusal = %v", err)
	}

	plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("PrepareRemoveWithOptions: %v", err)
	}
	if !plan.Force || !plan.RequiresConfirmation() {
		t.Errorf("plan force = %v, confirmation = %v", plan.Force, plan.RequiresConfirmation())
	}
	if plan.Loss.BranchState != CleanupBranchUnmerged {
		t.Errorf("branch state = %q, want unmerged", plan.Loss.BranchState)
	}
	if len(plan.Loss.Unmerged) != 1 || len(plan.Loss.Unpushed) != 1 {
		t.Errorf("unmerged = %#v, unpushed = %#v", plan.Loss.Unmerged, plan.Loss.Unpushed)
	}
	joined := strings.Join(plan.Loss.DirtyFiles, "\n")
	for _, want := range []string{"tracked.txt", "staged.txt", "untracked.txt", "ignored.txt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("dirty files missing %q:\n%s", want, joined)
		}
	}
	loss := strings.Join(plan.LossLines(), "\n")
	if !strings.Contains(loss, "uncommitted, untracked, or ignored") {
		t.Errorf("loss lines missing discarded changes:\n%s", loss)
	}
	retained := strings.Join(plan.RetainedLines(), "\n")
	for _, want := range []string{"remote-tracking branch", "branch work", "is unmerged"} {
		if !strings.Contains(retained, want) {
			t.Errorf("retained lines missing %q:\n%s", want, retained)
		}
	}
	actions := strings.Join(plan.ActionLines(), "\n")
	for _, want := range []string{"run provider", "retain branch feat/loss", "clear workflow state"} {
		if !strings.Contains(actions, want) {
			t.Errorf("action lines missing %q:\n%s", want, actions)
		}
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("prepare mutated the worktree: %v", err)
	}
}

func TestPrepareRemoveDeleteBranchRefusesUnmerged(t *testing.T) {
	_, target, _, cfg := removeFixture(t, "feat/unmerged")
	commitIn(t, target, "work.txt", "work\n", "unmerged work")

	_, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true})
	if err == nil {
		t.Fatal("expected an unmerged --delete-branch refusal")
	}
	for _, want := range []string{"unmerged", "--delete-branch", "retain the branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("worktree was removed despite the refusal: %v", statErr)
	}
}

func TestPrepareRemoveDeleteBranchAllowsMergedAndPatchEquivalent(t *testing.T) {
	t.Run("merged", func(t *testing.T) {
		_, target, _, cfg := removeFixture(t, "feat/merged")
		plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true})
		if err != nil {
			t.Fatalf("PrepareRemoveWithOptions: %v", err)
		}
		if plan.Loss.BranchState != CleanupBranchMerged || !plan.DeleteBranch {
			t.Errorf("state = %q, delete = %v", plan.Loss.BranchState, plan.DeleteBranch)
		}
		if !strings.Contains(strings.Join(plan.ActionLines(), "\n"), "git branch -d") {
			t.Errorf("merged action list should use -d:\n%s", strings.Join(plan.ActionLines(), "\n"))
		}
	})

	t.Run("patch-equivalent", func(t *testing.T) {
		repo, target, _, cfg := removeFixture(t, "feat/patch")
		sha := commitIn(t, target, "patch.txt", "patch\n", "patch work")
		commitIn(t, repo, "main.txt", "main\n", "advance main")
		gitRun(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "cherry-pick", sha)

		plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true})
		if err != nil {
			t.Fatalf("PrepareRemoveWithOptions: %v", err)
		}
		if plan.Loss.BranchState != CleanupBranchPatchEquivalent || !plan.DeleteBranch {
			t.Errorf("state = %q, delete = %v", plan.Loss.BranchState, plan.DeleteBranch)
		}
		if !strings.Contains(strings.Join(plan.ActionLines(), "\n"), "atomic update-ref -d") {
			t.Errorf("patch-equivalent action list should use atomic update-ref:\n%s", strings.Join(plan.ActionLines(), "\n"))
		}
	})
}

func TestExecuteRemoveForceRemovesDirtyWorktree(t *testing.T) {
	_, target, targetPath, cfg := removeFixture(t, "feat/forced")
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("PrepareRemoveWithOptions: %v", err)
	}
	if !strings.Contains(strings.Join(plan.Command, " "), "worktree remove --force") {
		t.Errorf("forced git provider command = %#v", plan.Command)
	}
	var stdout, stderr strings.Builder
	result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if result.Path != targetPath {
		t.Errorf("result path = %q", result.Path)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
}

func TestExecuteRemoveRetainsBranchWithState(t *testing.T) {
	t.Run("unmerged", func(t *testing.T) {
		repo, target, _, cfg := removeFixture(t, "feat/keep-unmerged")
		commitIn(t, target, "work.txt", "work\n", "unmerged work")
		plan, err := PrepareRemove(context.Background(), cfg, target)
		if err != nil {
			t.Fatalf("PrepareRemove: %v", err)
		}
		var stdout, stderr strings.Builder
		result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
		if err != nil {
			t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
		}
		if result.DeletedBranch != "" || len(result.RetainedBranches) != 1 {
			t.Fatalf("result = %#v", result)
		}
		retained := result.RetainedBranches[0]
		if retained.Branch != "feat/keep-unmerged" || retained.State != CleanupBranchUnmerged || len(retained.Commits) != 1 {
			t.Errorf("retained = %#v", retained)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Errorf("worktree still exists: %v", err)
		}
		if _, err := git.BranchSHA(context.Background(), repo, "feat/keep-unmerged"); err != nil {
			t.Errorf("branch was not retained: %v", err)
		}
	})

	t.Run("merged", func(t *testing.T) {
		_, target, _, cfg := removeFixture(t, "feat/keep-merged")
		plan, err := PrepareRemove(context.Background(), cfg, target)
		if err != nil {
			t.Fatalf("PrepareRemove: %v", err)
		}
		var stdout, stderr strings.Builder
		result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
		if err != nil {
			t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
		}
		if len(result.RetainedBranches) != 1 {
			t.Fatalf("result = %#v", result)
		}
		retained := result.RetainedBranches[0]
		if retained.State != CleanupBranchMerged || len(retained.Commits) != 0 {
			t.Errorf("retained = %#v", retained)
		}
	})
}

func TestExecuteRemoveDeletesBranchOnlyWithProof(t *testing.T) {
	t.Run("merged uses -d", func(t *testing.T) {
		repo, target, _, cfg := removeFixture(t, "feat/delete-merged")
		plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true})
		if err != nil {
			t.Fatalf("PrepareRemoveWithOptions: %v", err)
		}
		var stdout, stderr strings.Builder
		result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
		if err != nil {
			t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
		}
		if result.DeletedBranch != "feat/delete-merged" || len(result.RetainedBranches) != 0 {
			t.Errorf("result = %#v", result)
		}
		if _, err := git.BranchSHA(context.Background(), repo, "feat/delete-merged"); err == nil {
			t.Error("merged branch was not deleted")
		}
	})

	t.Run("patch-equivalent uses -D after a fresh proof", func(t *testing.T) {
		repo, target, _, cfg := removeFixture(t, "feat/delete-patch")
		sha := commitIn(t, target, "patch.txt", "patch\n", "patch work")
		commitIn(t, repo, "main.txt", "main\n", "advance main")
		gitRun(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "cherry-pick", sha)
		plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true})
		if err != nil {
			t.Fatalf("PrepareRemoveWithOptions: %v", err)
		}
		var stdout, stderr strings.Builder
		result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
		if err != nil {
			t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
		}
		if result.DeletedBranch != "feat/delete-patch" {
			t.Errorf("result = %#v", result)
		}
		if _, err := git.BranchSHA(context.Background(), repo, "feat/delete-patch"); err == nil {
			t.Error("patch-equivalent branch was not deleted")
		}
	})

	t.Run("unmerged is never deleted", func(t *testing.T) {
		repo, target, _, cfg := removeFixture(t, "feat/no-delete")
		commitIn(t, target, "work.txt", "work\n", "unmerged work")
		plan, err := PrepareRemove(context.Background(), cfg, target)
		if err != nil {
			t.Fatalf("PrepareRemove: %v", err)
		}
		plan.DeleteBranch = true // simulate a stale plan that was allowed
		var stdout, stderr strings.Builder
		result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected the branch proof to refuse")
		}
		if !strings.Contains(err.Error(), "no longer merged or patch-equivalent") {
			t.Errorf("err = %v", err)
		}
		if result.DeletedBranch != "" {
			t.Errorf("result = %#v", result)
		}
		if _, err := git.BranchSHA(context.Background(), repo, "feat/no-delete"); err != nil {
			t.Errorf("unmerged branch was deleted: %v", err)
		}
	})
}

func TestExecuteRemoveRechecksBeforeMutation(t *testing.T) {
	t.Run("dirty after planning", func(t *testing.T) {
		_, target, _, cfg := removeFixture(t, "feat/recheck-dirty")
		plan, err := PrepareRemove(context.Background(), cfg, target)
		if err != nil {
			t.Fatalf("PrepareRemove: %v", err)
		}
		if err := os.WriteFile(filepath.Join(target, "late.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr strings.Builder
		_, err = ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "changed after planning") {
			t.Fatalf("err = %v", err)
		}
		if _, statErr := os.Stat(target); statErr != nil {
			t.Errorf("worktree was removed despite the re-check: %v", statErr)
		}
	})

	t.Run("reported loss goes stale", func(t *testing.T) {
		_, target, _, cfg := removeFixture(t, "feat/recheck-loss")
		plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{Force: true})
		if err != nil {
			t.Fatalf("PrepareRemoveWithOptions: %v", err)
		}
		if err := os.WriteFile(filepath.Join(target, "late.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr strings.Builder
		_, err = ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "reported loss is stale") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("new Rex session", func(t *testing.T) {
		_, target, _, cfg := removeFixture(t, "feat/recheck-rex")
		plan, err := PrepareRemove(context.Background(), cfg, target)
		if err != nil {
			t.Fatalf("PrepareRemove: %v", err)
		}
		writeFakeRex(t, `case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[{"session_id":"session:late","label":"repo/feat/recheck-rex"}]}' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)
		var stdout, stderr strings.Builder
		_, err = ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "started after planning") {
			t.Fatalf("err = %v", err)
		}
		if _, statErr := os.Stat(target); statErr != nil {
			t.Errorf("worktree was removed despite the live session: %v", statErr)
		}
	})
}

func TestExecuteRemoveRechecksBranchProof(t *testing.T) {
	repo, target, targetPath, cfg := removeFixture(t, "feat/proof")
	base := strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "HEAD"))
	sha := commitIn(t, target, "work.txt", "work\n", "branch work")
	gitRun(t, repo, "merge", "-q", "--ff-only", "feat/proof")

	// After planning, the fake provider removes the worktree and rewinds the
	// default branch so the branch is no longer merged or patch-equivalent.
	script := fmt.Sprintf("git -C %q worktree remove %q && git -C %q reset --hard %s", repo, targetPath, repo, base)
	cfg.Providers["fake"] = config.Provider{Remove: []string{"sh", "-c", script}}
	cfg.Projects[0].Provider = "fake"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	emptyRex(t)

	plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true})
	if err != nil {
		t.Fatalf("PrepareRemoveWithOptions: %v", err)
	}
	if plan.HeadSHA != sha {
		t.Fatalf("plan head = %q, want %q", plan.HeadSHA, sha)
	}
	var stdout, stderr strings.Builder
	result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no longer merged or patch-equivalent") {
		t.Fatalf("err = %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if result.DeletedBranch != "" {
		t.Errorf("result = %#v", result)
	}
	if _, err := git.BranchSHA(context.Background(), repo, "feat/proof"); err != nil {
		t.Errorf("branch was deleted without a fresh proof: %v", err)
	}
}

func TestForceGitWorktreeRemove(t *testing.T) {
	in := []string{"git", "-C", "repo", "worktree", "remove", "path"}
	got := strings.Join(forceGitWorktreeRemove(in), " ")
	if got != "git -C repo worktree remove --force path" {
		t.Errorf("command = %q", got)
	}
	already := []string{"git", "worktree", "remove", "--force", "path"}
	if got := strings.Join(forceGitWorktreeRemove(already), " "); got != strings.Join(already, " ") {
		t.Errorf("already forced = %q", got)
	}
	other := []string{"rm", "-rf", "path"}
	if got := strings.Join(forceGitWorktreeRemove(other), " "); got != strings.Join(other, " ") {
		t.Errorf("other provider = %q", got)
	}
}

func TestPrepareRemoveDetachedRequiresForceAndListsUniqueCommits(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	emptyRex(t)

	target := filepath.Join(root, "detached")
	gitRun(t, repo, "worktree", "add", "-q", "--detach", target, "HEAD")
	commitIn(t, target, "unique.txt", "unique\n", "detached work")

	if _, err := PrepareRemove(context.Background(), cfg, target); err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("plain detached refusal = %v", err)
	}
	if _, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true}); err == nil || !strings.Contains(err.Error(), "not on a local branch") {
		t.Fatalf("detached delete-branch refusal = %v", err)
	} else if strings.Contains(err.Error(), "is checked out in worktree") {
		t.Errorf("detached refusal named a checked-out branch: %v", err)
	}

	plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("PrepareRemoveWithOptions: %v", err)
	}
	if !plan.Loss.Detached || len(plan.Loss.DetachedCommits) != 1 {
		t.Fatalf("loss = %#v", plan.Loss)
	}
	if !plan.RequiresConfirmation() {
		t.Error("detached unique commits did not require confirmation")
	}
	if !strings.Contains(strings.Join(plan.LossLines(), "\n"), "detached HEAD") {
		t.Errorf("loss lines = %#v", plan.LossLines())
	}

	var stdout, stderr strings.Builder
	result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if len(result.RetainedBranches) != 0 {
		t.Errorf("detached worktree reported a retained branch: %#v", result.RetainedBranches)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("detached worktree still exists: %v", err)
	}
}

func TestPrepareRemoveWithoutResolvableDefaultBranch(t *testing.T) {
	repo := testRepo(t)
	gitRun(t, repo, "branch", "-M", "work")
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	emptyRex(t)

	target := filepath.Join(root, "feat", "plain")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/plain", target)

	// Plain rm must not require a default branch; the retained report degrades.
	plan, err := PrepareRemove(context.Background(), cfg, target)
	if err != nil {
		t.Fatalf("plain PrepareRemove: %v", err)
	}
	if plan.Loss.BranchState != "" || plan.Loss.AssessmentError != "" {
		t.Errorf("plain loss = %#v", plan.Loss)
	}
	var stdout, stderr strings.Builder
	result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if len(result.RetainedBranches) != 1 || !strings.Contains(result.RetainedBranches[0].Error, "no default branch") {
		t.Errorf("retained = %#v, want a no-default-branch report", result.RetainedBranches)
	}

	// Force degrades to an assessment error instead of failing.
	dirty := filepath.Join(root, "feat", "dirty")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/dirty", dirty)
	if err := os.WriteFile(filepath.Join(dirty, "late.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err = PrepareRemoveWithOptions(context.Background(), cfg, dirty, RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("forced PrepareRemove: %v", err)
	}
	if plan.Loss.AssessmentError == "" || !plan.RequiresConfirmation() {
		t.Errorf("forced loss = %#v", plan.Loss)
	}

	// --delete-branch still needs the default branch.
	if _, err := PrepareRemoveWithOptions(context.Background(), cfg, dirty, RemoveOptions{DeleteBranch: true}); err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("delete-branch without a default branch = %v", err)
	}
}

func TestPrepareRemoveOrphanBranchDegrades(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	emptyRex(t)

	gitRun(t, repo, "checkout", "-q", "--orphan", "orphan")
	commitIn(t, repo, "orphan.txt", "orphan\n", "orphan work")
	gitRun(t, repo, "checkout", "-q", "main")
	target := filepath.Join(root, "orphan")
	gitRun(t, repo, "worktree", "add", "-q", target, "orphan")

	// Plain rm skips the branch proof.
	if _, err := PrepareRemove(context.Background(), cfg, target); err != nil {
		t.Fatalf("plain orphan PrepareRemove: %v", err)
	}
	// Force attempts the proof and degrades to an assessment error.
	plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("forced orphan PrepareRemove: %v", err)
	}
	if plan.Loss.AssessmentError == "" || !plan.RequiresConfirmation() {
		t.Errorf("orphan loss = %#v", plan.Loss)
	}
	// Branch deletion cannot be proven, so it refuses.
	if _, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true}); err == nil || !strings.Contains(err.Error(), "cannot prove branch") {
		t.Fatalf("orphan delete-branch = %v", err)
	}
}

func TestExecuteRemoveVerifiesWorktreeBeforeClosingSession(t *testing.T) {
	_, target, _, cfg := removeFixture(t, "feat/order")
	logPath := filepath.Join(t.TempDir(), "rex.log")
	t.Setenv("FAKE_REX_LOG", logPath)
	writeFakeRex(t, `log="$FAKE_REX_LOG"
case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[{"session_id":"session:order","label":"repo/feat/order"}]}' ;;
  "session kill session:order") printf '%s\n' killed >> "$log" ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)

	plan, err := PrepareRemove(context.Background(), cfg, target)
	if err != nil {
		t.Fatalf("PrepareRemove: %v", err)
	}
	if plan.SessionID != "session:order" {
		t.Fatalf("session = %q", plan.SessionID)
	}
	if err := os.WriteFile(filepath.Join(target, "late.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if _, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("err = %v", err)
	}
	if calls, err := os.ReadFile(logPath); err == nil && strings.Contains(string(calls), "killed") {
		t.Errorf("Rex session was killed before the worktree re-check: %q", calls)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("worktree was removed despite the re-check: %v", err)
	}
}

func TestExecuteRemoveDeletesMergedBranchWhenHeadIsElsewhere(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	emptyRex(t)

	commitIn(t, repo, "main.txt", "main\n", "advance main")
	gitRun(t, repo, "branch", "other", "main~1")
	target := filepath.Join(root, "feat", "head-elsewhere")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/head-elsewhere", target, "main")
	gitRun(t, repo, "checkout", "-q", "other")

	plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true})
	if err != nil {
		t.Fatalf("PrepareRemoveWithOptions: %v", err)
	}
	if plan.Loss.BranchState != CleanupBranchMerged {
		t.Fatalf("state = %q, want merged", plan.Loss.BranchState)
	}
	var stdout, stderr strings.Builder
	result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if result.DeletedBranch != "feat/head-elsewhere" {
		t.Errorf("result = %#v", result)
	}
	if _, err := git.BranchSHA(context.Background(), repo, "feat/head-elsewhere"); err == nil {
		t.Error("merged branch was not deleted when HEAD was on another branch")
	}
}

func TestExecuteRemoveDeleteBranchRechecksProtection(t *testing.T) {
	repo, target, _, cfg := removeFixture(t, "develop")
	plan, err := PrepareRemove(context.Background(), cfg, target)
	if err != nil {
		t.Fatalf("PrepareRemove: %v", err)
	}
	plan.DeleteBranch = true // bypass the prepare-time protection check
	var stdout, stderr strings.Builder
	if _, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "protected branch") {
		t.Fatalf("err = %v", err)
	}
	if _, err := git.BranchSHA(context.Background(), repo, "develop"); err != nil {
		t.Errorf("protected branch was deleted: %v", err)
	}
}

func TestExecuteRemoveWarnsWhenProviderLeavesDirectory(t *testing.T) {
	repo, target, targetPath, cfg := removeFixture(t, "feat/leftover")
	backup := target + ".bak"
	script := fmt.Sprintf("cp -R %q %q && git -C %q worktree remove %q && mv %q %q", target, backup, repo, target, backup, target)
	cfg.Providers["fake"] = config.Provider{Remove: []string{"sh", "-c", script}}
	cfg.Projects[0].Provider = "fake"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	state := config.NewState()
	state.SetStatus(targetPath, config.StatusInProgress, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}

	plan, err := PrepareRemove(context.Background(), cfg, target)
	if err != nil {
		t.Fatalf("PrepareRemove: %v", err)
	}
	var stdout, stderr strings.Builder
	result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "directory remains") {
		t.Errorf("warnings = %#v", result.Warnings)
	}
	worktrees, err := git.ListWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range worktrees {
		if wt.Path == targetPath {
			t.Errorf("worktree is still registered: %#v", wt)
		}
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("leftover directory was not preserved: %v", err)
	}
	loaded, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Workstreams[targetPath]; ok {
		t.Errorf("state entry was not cleared: %#v", loaded.Workstreams)
	}
}

func TestPrepareRemoveRefusalNamesEveryReason(t *testing.T) {
	_, target, _, cfg := removeFixture(t, "feat/multiple-reasons")
	commitIn(t, target, "work.txt", "work\n", "unmerged work")
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{DeleteBranch: true})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"uncommitted or untracked", "unmerged", "--delete-branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q:\n%v", want, err)
		}
	}
}

func TestExecuteRemoveRechecksDetachedLoss(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	emptyRex(t)

	target := filepath.Join(root, "detached-recheck")
	gitRun(t, repo, "worktree", "add", "-q", "--detach", target, "HEAD")
	commitIn(t, target, "unique.txt", "unique\n", "detached work")
	sha := runGitOutput(t, target, "rev-parse", "HEAD")
	gitRun(t, repo, "branch", "pin", sha)

	plan, err := PrepareRemoveWithOptions(context.Background(), cfg, target, RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("PrepareRemoveWithOptions: %v", err)
	}
	if len(plan.Loss.DetachedCommits) != 0 {
		t.Fatalf("pinned commit was already unique at plan time: %#v", plan.Loss.DetachedCommits)
	}

	// Removing the pin makes the commit unique while the confirmation could
	// still be on screen.
	gitRun(t, repo, "branch", "-D", "pin")
	var stdout, stderr strings.Builder
	if _, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "became unique") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("detached worktree was removed despite the stale loss: %v", err)
	}
}

func TestExecuteRemoveWarnsWhenProviderDeletesBranch(t *testing.T) {
	repo, target, targetPath, cfg := removeFixture(t, "feat/provider-branch")
	commitIn(t, target, "work.txt", "work\n", "unmerged work")
	script := fmt.Sprintf("git -C %q worktree remove %q && git -C %q branch -D %q", repo, target, repo, "feat/provider-branch")
	cfg.Providers["fake"] = config.Provider{Remove: []string{"sh", "-c", script}}
	cfg.Projects[0].Provider = "fake"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	plan, err := PrepareRemove(context.Background(), cfg, target)
	if err != nil {
		t.Fatalf("PrepareRemove: %v", err)
	}
	var stdout, stderr strings.Builder
	result, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("ExecuteRemove: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "disappeared") || !strings.Contains(result.Warnings[0], "unmerged work") {
		t.Errorf("warnings = %#v", result.Warnings)
	}
	if _, err := git.BranchSHA(context.Background(), repo, "feat/provider-branch"); err == nil {
		t.Error("provider-deleted branch still resolves")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
	_ = targetPath
}

// TestPrepareRemoveNewlinePathWorktree proves a worktree whose path contains a
// newline resolves through rm planning and the direct re-list used after a
// provider runs. The provider is a no-op, so the worktree must survive.
func TestPrepareRemoveNewlinePathWorktree(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	target := filepath.Join(root, "feat", "line\nbreak")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/newline", target)
	targetPath := canonical(t, target)
	emptyRex(t)

	plan, err := PrepareRemove(context.Background(), cfg, "path:"+target)
	if err != nil {
		t.Fatalf("PrepareRemove(path:): %v", err)
	}
	if plan.Path != targetPath || plan.Branch != "feat/newline" {
		t.Fatalf("plan = %#v, want the newline worktree %q", plan, targetPath)
	}

	cfg.Providers["fake"] = config.Provider{Remove: []string{"sh", "-c", "true"}}
	cfg.Projects[0].Provider = "fake"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	plan, err = PrepareRemove(context.Background(), cfg, targetPath)
	if err != nil {
		t.Fatalf("PrepareRemove(literal path): %v", err)
	}

	var stdout, stderr strings.Builder
	if _, err := ExecuteRemove(context.Background(), cfg, plan, nil, &stdout, &stderr); err == nil ||
		!strings.Contains(err.Error(), "did not remove worktree") {
		t.Fatalf("err = %v, want the provider verification failure", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("preparing or executing removed the worktree: %v", err)
	}
}
