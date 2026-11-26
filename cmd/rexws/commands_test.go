package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// lockedBuffer is safe for the concurrent stdout/stderr copies os/exec starts.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut lockedBuffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func testRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q")
	cmd := exec.Command("git", "-C", repo,
		"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"commit", "--allow-empty", "-q", "-m", "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return repo
}

func testConfig(t *testing.T, repo, worktreeRoot, provider string) {
	t.Helper()
	t.Setenv(config.EnvDir, filepath.Join(t.TempDir(), "config"))
	cfg := config.Default()
	repoPath, err := pathutil.Canonical(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.RegisterProject(repoPath, "repo", provider, worktreeRoot); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestNewCreatesWorktree(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	stdout, stderr, err := runCLI(t, "new", "repo", "feat/auth", "--json")
	if err != nil {
		t.Fatalf("new: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Project  string `json:"project"`
		Branch   string `json:"branch"`
		Path     string `json:"path"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	want, err := pathutil.Canonical(filepath.Join(root, "feat", "auth"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Project != "repo" || result.Branch != "feat/auth" || result.Path != want || result.Provider != "git" {
		t.Errorf("result = %#v", result)
	}

	worktrees, err := git.ListWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	matches := git.WorktreesForBranch(worktrees, "feat/auth")
	if len(matches) != 1 {
		t.Fatalf("worktrees = %#v", matches)
	}
	if got, err := pathutil.Canonical(matches[0].Path); err != nil || got != want {
		t.Errorf("worktree path = %q, %v; want %q", got, err, want)
	}
}

func TestRmRemovesWorktreeAndClosesSession(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	target := filepath.Join(root, "feat", "rm")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/rm", target)
	targetPath, err := pathutil.Canonical(target)
	if err != nil {
		t.Fatal(err)
	}

	state := config.NewState()
	state.SetStatus(targetPath, config.StatusInProgress, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(t.TempDir(), "rex.log")
	t.Setenv("FAKE_REX_LOG", logPath)
	fake := filepath.Join(t.TempDir(), "rex")
	script := `#!/bin/sh
log="$FAKE_REX_LOG"
case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[{"session_id":"session:fake","label":"repo/feat/rm"}]}' ;;
  "session kill session:fake") printf '%s\n' killed >> "$log" ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REXWS_REX_BIN", fake)

	stdout, stderr, err := runCLI(t, "rm", targetPath, "--json")
	if err != nil {
		t.Fatalf("rm: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		ClosedSessionID string `json:"closed_session_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if result.ClosedSessionID != "session:fake" {
		t.Errorf("closed session = %q", result.ClosedSessionID)
	}

	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
	worktrees, err := git.ListWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if matches := git.WorktreesForBranch(worktrees, "feat/rm"); len(matches) != 0 {
		t.Errorf("worktree still registered: %#v", matches)
	}
	loaded, err := config.LoadState(config.Default().StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Workstreams[targetPath]; ok {
		t.Errorf("state entry was not removed: %#v", loaded.Workstreams)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "killed") {
		t.Errorf("Rex session was not killed: %q", calls)
	}
}

func TestRmRefusesMainWorktree(t *testing.T) {
	repo := testRepo(t)
	testConfig(t, repo, "", "")
	repoPath, err := pathutil.Canonical(repo)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runCLI(t, "rm", repoPath)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestRmRefusesDirtyWorktree(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	target := filepath.Join(root, "feat", "dirty")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/dirty", target)
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

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

	_, _, err := runCLI(t, "rm", target)
	if err == nil || !strings.Contains(err.Error(), "uncommitted or untracked") {
		t.Fatalf("err = %v, want a dirty-worktree refusal", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("worktree was removed despite being dirty: %v", statErr)
	}
}
