package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

func tuiGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// tuiRemoveRepo creates a repository with a pinned main branch and a saved
// project config, so the TUI remove handlers run against real worktrees.
func tuiRemoveRepo(t *testing.T) (repo, root string, cfg config.Config) {
	t.Helper()
	t.Setenv(config.EnvDir, filepath.Join(t.TempDir(), "config"))
	repo = filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	tuiGit(t, repo, "init", "-q")
	tuiGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-q", "-m", "init")
	tuiGit(t, repo, "branch", "-M", "main")
	root = filepath.Join(t.TempDir(), "worktrees")
	cfg = config.Default()
	canonical, err := pathutil.Canonical(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.RegisterProject(canonical, "repo", "", root); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	return repo, root, cfg
}

func tuiWorktree(t *testing.T, repo, root, branch string) (target, targetPath string) {
	t.Helper()
	target = filepath.Join(root, branch)
	tuiGit(t, repo, "worktree", "add", "-q", "-b", branch, target)
	targetPath, err := pathutil.Canonical(target)
	if err != nil {
		t.Fatal(err)
	}
	return target, targetPath
}

func tuiCommit(t *testing.T, dir, file, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tuiGit(t, dir, "add", "--", file)
	tuiGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", message)
}

func tuiFakeRex(t *testing.T, sessions string) (logPath string) {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), "rex.log")
	t.Setenv("FAKE_REX_LOG", logPath)
	script := `#!/bin/sh
log="$FAKE_REX_LOG"
case "$*" in
  "session ls --json") printf '%s\n' '` + sessions + `' ;;
  "session kill "*) printf '%s\n' "$*" >> "$log" ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`
	bin := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvRexBin, bin)
	return logPath
}

func saveTUIState(t *testing.T, cfg config.Config, path string) {
	t.Helper()
	state := config.NewState()
	state.SetStatus(path, config.StatusInProgress, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
}

func tuiStateHas(t *testing.T, cfg config.Config, path string) bool {
	t.Helper()
	loaded, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	_, ok := loaded.Workstreams[path]
	return ok
}

func tuiNotice(s State, substr string) (notice, bool) {
	for _, n := range s.notices {
		if strings.Contains(n.text, substr) {
			return n, true
		}
	}
	return notice{}, false
}

func TestPreflightRemoveUsesForce(t *testing.T) {
	repo, root, cfg := tuiRemoveRepo(t)
	target, targetPath := tuiWorktree(t, repo, root, "feat/pre")
	if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tuiFakeRex(t, `{"sessions":[]}`)

	msg := preflightRemoveCmd(context.Background(), cfg, RemoveRequest{Path: targetPath})()
	prepared, ok := msg.(removePreparedMsg)
	if !ok {
		t.Fatalf("msg = %#v", msg)
	}
	if prepared.err != nil {
		t.Fatalf("preflight: %v", prepared.err)
	}
	if !prepared.plan.Force || prepared.plan.Path != targetPath {
		t.Errorf("plan = %#v", prepared.plan)
	}
}

func TestCloseSessionVerifyRefusalSkipsKill(t *testing.T) {
	repo, root, cfg := tuiRemoveRepo(t)
	target, targetPath := tuiWorktree(t, repo, root, "feat/order")
	logPath := tuiFakeRex(t, `{"sessions":[{"session_id":"session:order","label":"repo/feat/order"}]}`)

	plan, err := ops.PrepareRemoveWithOptions(context.Background(), cfg, targetPath, ops.RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("PrepareRemoveWithOptions: %v", err)
	}
	if plan.SessionID != "session:order" {
		t.Fatalf("session = %q", plan.SessionID)
	}
	if err := os.WriteFile(filepath.Join(target, "late.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := closeSessionCmd(context.Background(), cfg, plan)()
	closed, ok := msg.(removeSessionClosedMsg)
	if !ok {
		t.Fatalf("msg = %#v", msg)
	}
	if closed.err == nil || !strings.Contains(closed.err.Error(), "changed after planning") {
		t.Fatalf("err = %v", closed.err)
	}
	if calls, err := os.ReadFile(logPath); err == nil && strings.Contains(string(calls), "session kill") {
		t.Errorf("session was killed before the worktree re-check: %q", calls)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("worktree was removed despite the re-check: %v", err)
	}
}

func TestRemoveExecDoneProviderFailureKeepsState(t *testing.T) {
	repo, root, cfg := tuiRemoveRepo(t)
	_, targetPath := tuiWorktree(t, repo, root, "feat/provider-fail")
	saveTUIState(t, cfg, targetPath)

	m := NewModel()
	defer m.Close()
	m.state.cfg = cfg
	m.state.haveConfig = true
	plan := ops.RemovePlan{Project: cfg.Projects[0], Path: targetPath, Branch: "feat/provider-fail"}
	m.handleRemoveExecDone(removeExecDoneMsg{plan: plan, err: errors.New("exit 1")})

	if _, ok := tuiNotice(m.state, "provider remove: exit 1"); !ok {
		t.Errorf("notices = %#v", m.state.notices)
	}
	if !tuiStateHas(t, cfg, targetPath) {
		t.Error("state entry was cleared after a provider failure")
	}
}

func TestRemoveExecDoneVerifyRemovedFailureKeepsState(t *testing.T) {
	repo, root, cfg := tuiRemoveRepo(t)
	_, targetPath := tuiWorktree(t, repo, root, "feat/still-there")
	saveTUIState(t, cfg, targetPath)

	m := NewModel()
	defer m.Close()
	m.state.cfg = cfg
	m.state.haveConfig = true
	plan := ops.RemovePlan{Project: cfg.Projects[0], Path: targetPath, Branch: "feat/still-there"}
	m.handleRemoveExecDone(removeExecDoneMsg{plan: plan})

	if _, ok := tuiNotice(m.state, "did not remove worktree"); !ok {
		t.Errorf("notices = %#v", m.state.notices)
	}
	if !tuiStateHas(t, cfg, targetPath) {
		t.Error("state entry was cleared while the worktree was still registered")
	}
}

func TestRemoveExecDoneSuccessReportsRetainedBranch(t *testing.T) {
	repo, root, cfg := tuiRemoveRepo(t)
	target, targetPath := tuiWorktree(t, repo, root, "feat/retained")
	tuiCommit(t, target, "work.txt", "work\n", "unmerged work")
	saveTUIState(t, cfg, targetPath)
	tuiGit(t, repo, "worktree", "remove", target)

	m := NewModel()
	defer m.Close()
	m.state.cfg = cfg
	m.state.haveConfig = true
	plan := ops.RemovePlan{Project: cfg.Projects[0], Path: targetPath, Branch: "feat/retained"}
	m.handleRemoveExecDone(removeExecDoneMsg{plan: plan})

	if tuiStateHas(t, cfg, targetPath) {
		t.Error("state entry was not cleared")
	}
	got, ok := tuiNotice(m.state, "Retained branch feat/retained")
	if !ok {
		t.Fatalf("notices = %#v", m.state.notices)
	}
	if !got.info || !strings.Contains(got.text, "unmerged") {
		t.Errorf("notice = %#v", got)
	}
}

func TestRemoveExecDoneWarnsLeftoverDirectory(t *testing.T) {
	repo, root, cfg := tuiRemoveRepo(t)
	target, targetPath := tuiWorktree(t, repo, root, "feat/leftover")
	saveTUIState(t, cfg, targetPath)
	tuiGit(t, repo, "worktree", "remove", target)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	defer m.Close()
	m.state.cfg = cfg
	m.state.haveConfig = true
	plan := ops.RemovePlan{Project: cfg.Projects[0], Path: targetPath, Branch: "feat/leftover"}
	m.handleRemoveExecDone(removeExecDoneMsg{plan: plan})

	got, ok := tuiNotice(m.state, "directory remains")
	if !ok {
		t.Fatalf("notices = %#v", m.state.notices)
	}
	if !got.warning {
		t.Errorf("leftover directory was not a warning notice: %#v", got)
	}
	if tuiStateHas(t, cfg, targetPath) {
		t.Error("state entry was not cleared for a deregistered worktree")
	}
}
