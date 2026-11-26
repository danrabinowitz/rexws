package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// runCLIInput is runCLI with stdin set, so non-interactive and prompt paths can
// both be exercised deterministically.
func runCLIInput(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut lockedBuffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func rmFakeRex(t *testing.T) {
	t.Helper()
	fake := filepath.Join(t.TempDir(), "rex")
	script := `#!/bin/sh
case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[]}' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REXWS_REX_BIN", fake)
}

func rmCommitIn(t *testing.T, dir, file, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "--", file)
	gitRun(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", message)
}

func rmFixture(t *testing.T, branch string) (repo, target, targetPath string) {
	t.Helper()
	repo = testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	target = filepath.Join(root, branch)
	gitRun(t, repo, "worktree", "add", "-q", "-b", branch, target)
	targetPath, err := pathutil.Canonical(target)
	if err != nil {
		t.Fatal(err)
	}
	rmFakeRex(t)
	return repo, target, targetPath
}

func TestRmForceJSONRequiresYesAndKeepsStdoutPure(t *testing.T) {
	_, target, targetPath := rmFixture(t, "feat/json-force")
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLIInput(t, "", "rm", targetPath, "--force", "--json")
	if err == nil || !strings.Contains(err.Error(), "without confirmation") {
		t.Fatalf("err = %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("refused --json run wrote stdout: %q", stdout)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("worktree was removed despite the refusal: %v", statErr)
	}

	stdout, stderr, err = runCLIInput(t, "", "rm", targetPath, "--force", "--json", "--yes")
	if err != nil {
		t.Fatalf("rm --force --json --yes: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Path string `json:"path"`
		Loss *struct {
			DirtyFiles []string `json:"dirty_files"`
		} `json:"loss"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if result.Path != targetPath || result.Loss == nil || len(result.Loss.DirtyFiles) != 1 {
		t.Errorf("result = %#v", result)
	}
	if !strings.Contains(stderr, "LOSS") || !strings.Contains(stderr, "untracked.txt") || !strings.Contains(stderr, "ACTIONS") {
		t.Errorf("stderr did not describe the loss:\n%s", stderr)
	}
	if strings.Contains(stdout, "LOSS") {
		t.Errorf("stdout was not pure JSON:\n%s", stdout)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Errorf("worktree still exists: %v", statErr)
	}
}

func TestRmForceRequiresYesWhenNonInteractive(t *testing.T) {
	_, target, targetPath := rmFixture(t, "feat/noninteractive")
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runCLIInput(t, "", "rm", targetPath, "--force"); err == nil || !strings.Contains(err.Error(), "without confirmation") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("worktree was removed despite the refusal: %v", statErr)
	}

	stdout, stderr, err := runCLIInput(t, "", "rm", targetPath, "--force", "--yes")
	if err != nil {
		t.Fatalf("rm --force --yes: %v\nstderr: %s", err, stderr)
	}
	for _, want := range []string{"LOSS", "uncommitted, untracked, or ignored", "untracked.txt", "ACTIONS", "run provider", "retain branch"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stdout, "Removed worktree "+targetPath) {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRmForceInteractivePrompt(t *testing.T) {
	_, target, targetPath := rmFixture(t, "feat/prompt")
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := removeStdinIsTerminal
	removeStdinIsTerminal = func(*cobra.Command) bool { return true }
	t.Cleanup(func() { removeStdinIsTerminal = previous })

	stdout, stderr, err := runCLIInput(t, "n\n", "rm", targetPath, "--force")
	if err != nil {
		t.Fatalf("rm with n: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "[y/N]") || !strings.Contains(stderr, "Nothing was changed.") {
		t.Errorf("decline output = %q", stderr)
	}
	if !strings.Contains(stderr, "LOSS") {
		t.Errorf("loss was not printed before the prompt:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("decline wrote stdout: %q", stdout)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("worktree was removed after declining: %v", statErr)
	}

	_, stderr, err = runCLIInput(t, "y\n", "rm", targetPath, "--force")
	if err != nil {
		t.Fatalf("rm with y: %v\nstderr: %s", err, stderr)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Errorf("worktree still exists after confirming: %v", statErr)
	}
}

func TestRmDeleteBranchRefusesUnmerged(t *testing.T) {
	repo, target, targetPath := rmFixture(t, "feat/cli-unmerged")
	rmCommitIn(t, target, "work.txt", "work\n", "unmerged work")

	_, _, err := runCLI(t, "rm", targetPath, "--delete-branch")
	if err == nil {
		t.Fatal("expected an unmerged --delete-branch refusal")
	}
	for _, want := range []string{"unmerged", "--delete-branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("worktree was removed despite the refusal: %v", statErr)
	}
	if _, err := git.BranchSHA(context.Background(), repo, "feat/cli-unmerged"); err != nil {
		t.Errorf("branch was deleted despite the refusal: %v", err)
	}
}

func TestRmDeleteBranchMerged(t *testing.T) {
	repo, target, targetPath := rmFixture(t, "feat/cli-merged")

	stdout, stderr, err := runCLI(t, "rm", targetPath, "--delete-branch", "--yes", "--json")
	if err != nil {
		t.Fatalf("rm --delete-branch: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		DeletedBranch    string `json:"deleted_branch"`
		RetainedBranches []struct {
			Branch string `json:"branch"`
		} `json:"retained_branches"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if result.DeletedBranch != "feat/cli-merged" || len(result.RetainedBranches) != 0 {
		t.Errorf("result = %#v", result)
	}
	if _, err := git.BranchSHA(context.Background(), repo, "feat/cli-merged"); err == nil {
		t.Error("merged branch was not deleted")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Errorf("worktree still exists: %v", statErr)
	}
}

func TestRmReportsRetainedUnmergedBranch(t *testing.T) {
	repo, target, targetPath := rmFixture(t, "feat/cli-retained")
	rmCommitIn(t, target, "work.txt", "work\n", "unmerged work")

	stdout, stderr, err := runCLI(t, "rm", targetPath, "--json")
	if err != nil {
		t.Fatalf("rm: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		RetainedBranches []struct {
			Branch  string `json:"branch"`
			State   string `json:"state"`
			Commits []struct {
				SHA     string `json:"sha"`
				Subject string `json:"subject"`
			} `json:"commits"`
		} `json:"retained_branches"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if len(result.RetainedBranches) != 1 {
		t.Fatalf("result = %#v", result)
	}
	retained := result.RetainedBranches[0]
	if retained.Branch != "feat/cli-retained" || retained.State != "unmerged" || len(retained.Commits) != 1 {
		t.Errorf("retained = %#v", retained)
	}
	if _, err := git.BranchSHA(context.Background(), repo, "feat/cli-retained"); err != nil {
		t.Errorf("branch was not retained: %v", err)
	}
}

func TestRmForceRequiresForceForYes(t *testing.T) {
	_, _, targetPath := rmFixture(t, "feat/yes-only")
	if _, _, err := runCLI(t, "rm", targetPath, "--yes"); err == nil || !strings.Contains(err.Error(), "--yes requires --force") {
		t.Fatalf("err = %v", err)
	}
}

func rmGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestRmForceDoesNotPromptForRetainedUnmergedBranch(t *testing.T) {
	_, target, targetPath := rmFixture(t, "feat/retained-force")
	rmCommitIn(t, target, "work.txt", "work\n", "unmerged work")

	// The worktree is clean and the branch is retained, so force discards
	// nothing and must not prompt even with no --yes and a non-terminal stdin.
	stdout, stderr, err := runCLIInput(t, "", "rm", targetPath, "--force")
	if err != nil {
		t.Fatalf("rm --force: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "LOSS") || !strings.Contains(stderr, "nothing would be lost") {
		t.Errorf("stderr did not separate the retained branch from loss:\n%s", stderr)
	}
	if !strings.Contains(stderr, "RETAINED") || !strings.Contains(stderr, "unmerged") {
		t.Errorf("stderr did not report the retained branch:\n%s", stderr)
	}
	if strings.Contains(stderr, "[y/N]") {
		t.Errorf("retained-only force prompted:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Retained branch feat/retained-force: unmerged") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRmForceDetachedListsUniqueCommits(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	target := filepath.Join(root, "detached")
	gitRun(t, repo, "worktree", "add", "-q", "--detach", target, "HEAD")
	targetPath, err := pathutil.Canonical(target)
	if err != nil {
		t.Fatal(err)
	}
	rmCommitIn(t, target, "unique.txt", "unique\n", "detached work")
	rmFakeRex(t)

	if _, _, err := runCLI(t, "rm", targetPath); err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("plain detached rm = %v", err)
	}

	stdout, stderr, err := runCLI(t, "rm", targetPath, "--force", "--yes", "--json")
	if err != nil {
		t.Fatalf("rm detached --force: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Loss struct {
			Detached        bool `json:"detached"`
			DetachedCommits []struct {
				Subject string `json:"subject"`
			} `json:"detached_commits"`
		} `json:"loss"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if !result.Loss.Detached || len(result.Loss.DetachedCommits) != 1 || result.Loss.DetachedCommits[0].Subject != "detached work" {
		t.Errorf("loss = %#v", result.Loss)
	}
	if !strings.Contains(stderr, "detached HEAD") || !strings.Contains(stderr, "detached work") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRmPartialDeleteBranchFailureEmitsJSON(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	base := rmGitOutput(t, repo, "rev-parse", "HEAD")
	rmCommitIn(t, repo, "main.txt", "main\n", "advance main")
	target := filepath.Join(root, "feat", "partial")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/partial", target, "main")
	targetPath, err := pathutil.Canonical(target)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("git -C %q worktree remove %q && git -C %q reset --hard %s", repo, targetPath, repo, base)
	cfg.Providers["fake"] = config.Provider{Remove: []string{"sh", "-c", script}}
	cfg.Projects[0].Provider = "fake"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	rmFakeRex(t)

	stdout, stderr, err := runCLI(t, "rm", targetPath, "--delete-branch", "--json")
	if err == nil {
		t.Fatal("expected the branch proof to fail after the provider moved the default branch")
	}
	var result struct {
		Path             string `json:"path"`
		Error            string `json:"error"`
		RetainedBranches []struct {
			Branch string `json:"branch"`
			State  string `json:"state"`
		} `json:"retained_branches"`
	}
	if jsonErr := json.Unmarshal([]byte(stdout), &result); jsonErr != nil {
		t.Fatalf("stdout was not a JSON document on partial failure: %q\n%v\nstderr: %s", stdout, jsonErr, stderr)
	}
	if result.Path != targetPath || result.Error == "" {
		t.Errorf("result = %#v", result)
	}
	if len(result.RetainedBranches) != 1 || result.RetainedBranches[0].State != "unmerged" {
		t.Errorf("retained = %#v", result.RetainedBranches)
	}
	if _, err := git.BranchSHA(context.Background(), repo, "feat/partial"); err != nil {
		t.Errorf("branch was deleted without a fresh proof: %v", err)
	}
}

func TestRmForceJSONSplitsDiscardedAndRetained(t *testing.T) {
	_, target, targetPath := rmFixture(t, "feat/split")
	rmCommitIn(t, target, "work.txt", "work\n", "unmerged work")
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLI(t, "rm", targetPath, "--force", "--json", "--yes")
	if err != nil {
		t.Fatalf("rm --force --json --yes: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Loss struct {
			DirtyFiles []string `json:"dirty_files"`
			Unpushed   []struct {
				Subject string `json:"subject"`
			} `json:"unpushed_commits"`
		} `json:"loss"`
		Retained struct {
			State    string `json:"state"`
			Unpushed []struct {
				Subject string `json:"subject"`
			} `json:"unpushed_commits"`
		} `json:"retained"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if len(result.Loss.DirtyFiles) != 1 || len(result.Loss.Unpushed) != 0 {
		t.Errorf("loss = %#v; unpushed commits belong under retained", result.Loss)
	}
	if result.Retained.State != "unmerged" || len(result.Retained.Unpushed) != 1 || result.Retained.Unpushed[0].Subject != "unmerged work" {
		t.Errorf("retained = %#v", result.Retained)
	}
}
