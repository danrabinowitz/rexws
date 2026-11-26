package ops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

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

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func canonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := pathutil.Canonical(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// testConfig registers repo under the given worktree root and points
// config/state at a temp directory.
func testConfig(t *testing.T, repo, worktreeRoot string) config.Config {
	t.Helper()
	t.Setenv(config.EnvDir, filepath.Join(t.TempDir(), "config"))
	cfg := config.Default()
	if _, _, err := cfg.RegisterProject(canonical(t, repo), "repo", "", worktreeRoot); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return cfg
}

func writeFakeRex(t *testing.T, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvRexBin, bin)
	return bin
}

func TestDiscoverReportsPerProjectErrors(t *testing.T) {
	repo := testRepo(t)
	cfg := config.Default()
	cfg.Projects = []config.Project{
		{Name: "good", Path: canonical(t, repo)},
		{Name: "bad", Path: filepath.Join(t.TempDir(), "missing")},
	}

	discovery := Discover(context.Background(), cfg)
	if len(discovery.Inputs) != 1 || discovery.Inputs[0].Project.Name != "good" {
		t.Fatalf("inputs = %#v", discovery.Inputs)
	}
	if len(discovery.Errors) != 1 || discovery.Errors[0].Project != "bad" {
		t.Fatalf("errors = %#v", discovery.Errors)
	}
	if err := discovery.Err(); err == nil || !strings.Contains(err.Error(), "project bad") {
		t.Fatalf("Err() = %v", err)
	}
}

func TestSetStatus(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	target := filepath.Join(root, "feat", "status")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/status", target)
	targetPath := canonical(t, target)

	change, err := SetStatus(context.Background(), cfg, target, config.StatusInProgress)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if !change.Changed || change.Path != targetPath || change.Status != config.StatusInProgress || change.Label != "In Progress" {
		t.Errorf("change = %#v", change)
	}

	state, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	stored := state.For(targetPath, cfg.DefaultStatusValue())
	if stored.Status != config.StatusInProgress {
		t.Errorf("stored status = %q", stored.Status)
	}

	same, err := SetStatus(context.Background(), cfg, targetPath, config.StatusInProgress)
	if err != nil {
		t.Fatalf("second SetStatus: %v", err)
	}
	if same.Changed {
		t.Error("setting the current status should not change state")
	}
	if !same.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Errorf("updated_at changed: %v != %v", same.UpdatedAt, stored.UpdatedAt)
	}

	if _, err := SetStatus(context.Background(), cfg, filepath.Join(t.TempDir(), "elsewhere"), config.StatusDone); err == nil {
		t.Error("expected an error for a path outside the registered projects")
	}
}

func TestCreateWorktree(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	var stdout, stderr strings.Builder
	result, err := CreateWorktree(context.Background(), cfg, "repo", "feat/auth", nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("CreateWorktree: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	want := canonical(t, filepath.Join(root, "feat", "auth"))
	if result.Project != "repo" || result.Branch != "feat/auth" || result.Path != want || result.Provider != "git" {
		t.Errorf("result = %#v, want path %q", result, want)
	}

	plan, err := PrepareCreate(context.Background(), cfg, "repo", "feat/other")
	if err != nil {
		t.Fatalf("PrepareCreate: %v", err)
	}
	if plan.TargetPath != filepath.Join(root, "feat", "other") {
		t.Errorf("target = %q", plan.TargetPath)
	}
	cmd, err := plan.Cmd()
	if err != nil || cmd.Dir != "" {
		t.Errorf("Cmd = %#v, %v", cmd, err)
	}
	if len(cmd.Args) == 0 || cmd.Args[0] != "git" {
		t.Errorf("command = %#v", cmd.Args)
	}
}

func TestPrepareRemovePrechecks(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	if _, err := PrepareRemove(context.Background(), cfg, repo); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("main worktree err = %v, want a refusal", err)
	}

	dirtyTarget := filepath.Join(root, "feat", "dirty")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/dirty", dirtyTarget)
	if err := os.WriteFile(filepath.Join(dirtyTarget, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRemove(context.Background(), cfg, dirtyTarget); err == nil || !strings.Contains(err.Error(), "uncommitted or untracked") {
		t.Fatalf("dirty worktree err = %v, want a dirty refusal", err)
	}

	cleanTarget := filepath.Join(root, "feat", "clean")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/clean", cleanTarget)

	writeFakeRex(t, "exit 1\n")
	if _, err := PrepareRemove(context.Background(), cfg, cleanTarget); err == nil || !strings.Contains(err.Error(), "check Rex sessions") {
		t.Fatalf("rex-unavailable err = %v", err)
	}

	writeFakeRex(t, `case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[]}' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)
	plan, err := PrepareRemove(context.Background(), cfg, cleanTarget)
	if err != nil {
		t.Fatalf("PrepareRemove: %v", err)
	}
	if plan.Path != canonical(t, cleanTarget) || plan.Branch != "feat/clean" || plan.ProviderName != "git" || plan.SessionID != "" {
		t.Errorf("plan = %#v", plan)
	}
	if len(plan.Command) == 0 {
		t.Error("remove command was not rendered")
	}
}

func TestRemoveWorkstream(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	target := filepath.Join(root, "feat", "rm")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/rm", target)
	targetPath := canonical(t, target)

	state := config.NewState()
	state.SetStatus(targetPath, config.StatusInProgress, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(t.TempDir(), "rex.log")
	t.Setenv("FAKE_REX_LOG", logPath)
	writeFakeRex(t, `log="$FAKE_REX_LOG"
case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[{"session_id":"session:fake","label":"repo/feat/rm"}]}' ;;
  "session kill session:fake") printf '%s\n' killed >> "$log" ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)

	var stdout, stderr strings.Builder
	result, err := RemoveWorkstream(context.Background(), cfg, targetPath, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RemoveWorkstream: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if result.ClosedSessionID != "session:fake" || result.Path != targetPath || result.Provider != "git" {
		t.Errorf("result = %#v", result)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
	loaded, err := config.LoadState(cfg.StatusIDs())
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

func TestSwitchWorkstream(t *testing.T) {
	cfg := config.Default()
	logPath := filepath.Join(t.TempDir(), "rex.log")
	t.Setenv("FAKE_REX_LOG", logPath)
	writeFakeRex(t, `log="$FAKE_REX_LOG"
case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[{"session_id":"session:fake","label":"repo/main"}]}' ;;
  *"session.select"*) printf '%s\n' "$*" >> "$log" ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)

	result, err := SwitchWorkstreamWithClient(context.Background(), cfg, "client:fake", "repo/main", "/tmp", "")
	if err != nil {
		t.Fatalf("SwitchWorkstreamWithClient: %v", err)
	}
	if result.SessionID != "session:fake" || result.Created {
		t.Errorf("result = %#v", result)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "session.select session_id=session:fake") {
		t.Errorf("select call = %q", calls)
	}
}
