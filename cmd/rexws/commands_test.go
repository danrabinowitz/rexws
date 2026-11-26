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
	// Pin the default branch so tests do not depend on init.defaultBranch.
	gitRun(t, repo, "branch", "-M", "main")
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

// fakeRexNew writes a rex stub for the new --switch flow. clientList and
// sessionList are returned verbatim; every call is logged so tests can assert
// the creation and select arguments.
func fakeRexNew(t *testing.T, clientList, sessionList string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "rex.log")
	t.Setenv("FAKE_REX_LOG", logPath)
	t.Setenv(config.EnvRexClient, "")
	script := `#!/bin/sh
log="$FAKE_REX_LOG"
case "$*" in
  "client ls --json") printf 'client-ls\n' >> "$log"; printf '%s\n' '` + clientList + `' ;;
  "session ls --json") printf 'session-ls\n' >> "$log"; printf '%s\n' '` + sessionList + `' ;;
  "session new "*) printf 'new:%s\n' "$*" >> "$log"; printf '%s\n' '{"session_id":"session:new"}' ;;
  *"session.select"*) printf 'select:%s\n' "$*" >> "$log" ;;
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

func readCalls(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read calls: %v", err)
	}
	return string(data)
}

func TestNewSwitchCreatesSession(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	logPath := fakeRexNew(t,
		`{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`,
		`{"sessions":[]}`,
	)

	stdout, stderr, err := runCLI(t, "new", "repo", "feat/switch", "--switch", "--json")
	if err != nil {
		t.Fatalf("new --switch: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Project        string `json:"project"`
		Branch         string `json:"branch"`
		Path           string `json:"path"`
		Provider       string `json:"provider"`
		SessionID      string `json:"session_id"`
		SessionCreated *bool  `json:"session_created"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	wantPath, err := pathutil.Canonical(filepath.Join(root, "feat", "switch"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Project != "repo" || result.Branch != "feat/switch" || result.Path != wantPath || result.Provider != "git" {
		t.Errorf("result = %#v", result)
	}
	if result.SessionID != "session:new" || result.SessionCreated == nil || !*result.SessionCreated {
		t.Errorf("session fields = (%q, %v)", result.SessionID, result.SessionCreated)
	}

	calls := readCalls(t, logPath)
	if !strings.Contains(calls, "client-ls") {
		t.Errorf("app client was not resolved before creating: %q", calls)
	}
	if !strings.Contains(calls, "new:session new repo/feat/switch -c "+wantPath+" --json") {
		t.Errorf("session creation args = %q", calls)
	}
	if !strings.Contains(calls, "select:-C client:app do session.select session_id=session:new") {
		t.Errorf("session select args = %q", calls)
	}
}

func TestNewSwitchSelectsExistingSession(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	logPath := fakeRexNew(t,
		`{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`,
		`{"sessions":[{"session_id":"session:existing","label":"repo/feat/existing"}]}`,
	)

	stdout, stderr, err := runCLI(t, "new", "repo", "feat/existing", "--switch", "--json")
	if err != nil {
		t.Fatalf("new --switch: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		SessionID      string `json:"session_id"`
		SessionCreated *bool  `json:"session_created"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if result.SessionID != "session:existing" || result.SessionCreated == nil || *result.SessionCreated {
		t.Errorf("session fields = (%q, %v)", result.SessionID, result.SessionCreated)
	}
	calls := readCalls(t, logPath)
	if strings.Contains(calls, "new:") {
		t.Errorf("existing session was recreated: %q", calls)
	}
	if !strings.Contains(calls, "select:-C client:app do session.select session_id=session:existing") {
		t.Errorf("select args = %q", calls)
	}
}

func TestNewSwitchUsesExplicitClient(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	logPath := fakeRexNew(t,
		`{"clients":[{"client_id":"client:other","info":{"kind":"app"}}]}`,
		`{"sessions":[]}`,
	)

	_, stderr, err := runCLI(t, "new", "repo", "feat/explicit", "--switch", "--client", "client:explicit", "--json")
	if err != nil {
		t.Fatalf("new --switch --client: %v\nstderr: %s", err, stderr)
	}
	calls := readCalls(t, logPath)
	if strings.Contains(calls, "client-ls") {
		t.Errorf("explicit client still resolved the first app: %q", calls)
	}
	if !strings.Contains(calls, "select:-C client:explicit do session.select session_id=session:new") {
		t.Errorf("select args = %q", calls)
	}
}

func TestNewSwitchRequiresAppClientBeforeCreating(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	fakeRexNew(t, `{"clients":[]}`, `{"sessions":[]}`)

	_, _, err := runCLI(t, "new", "repo", "feat/noapp", "--switch")
	if err == nil || !strings.Contains(err.Error(), "needs a Rex app client") {
		t.Fatalf("err = %v, want an app-client error", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "feat", "noapp")); !os.IsNotExist(statErr) {
		t.Errorf("worktree was created before the app-client check: %v", statErr)
	}
}

func TestNewClientRequiresSwitch(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	_, _, err := runCLI(t, "new", "repo", "feat/client", "--client", "client:app")
	if err == nil || !strings.Contains(err.Error(), "--client requires --switch") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "feat", "client")); !os.IsNotExist(statErr) {
		t.Errorf("worktree was created despite the flag error: %v", statErr)
	}
}

func TestNewSwitchHumanOutput(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	fakeRexNew(t, `{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`, `{"sessions":[]}`)

	stdout, stderr, err := runCLI(t, "new", "repo", "feat/human", "--switch")
	if err != nil {
		t.Fatalf("new --switch: %v\nstderr: %s", err, stderr)
	}
	wantPath, err := pathutil.Canonical(filepath.Join(root, "feat", "human"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Created worktree feat/human at "+wantPath) {
		t.Errorf("stdout = %q, want the worktree line", stdout)
	}
	if !strings.Contains(stdout, "Created and switched to Rex session session:new") {
		t.Errorf("stdout = %q, want the created-session line", stdout)
	}
}

func TestNewSwitchHumanOutputExistingSession(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	fakeRexNew(t,
		`{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`,
		`{"sessions":[{"session_id":"session:existing","label":"repo/feat/again"}]}`,
	)

	stdout, stderr, err := runCLI(t, "new", "repo", "feat/again", "--switch")
	if err != nil {
		t.Fatalf("new --switch: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Switched to Rex session session:existing") {
		t.Errorf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, "Created and switched") {
		t.Errorf("stdout claims a new session: %q", stdout)
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
	// Without --switch the JSON shape must not gain the session fields.
	if strings.Contains(stdout, "session_id") || strings.Contains(stdout, "session_created") {
		t.Errorf("default new JSON changed: %q", stdout)
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

func TestSwitchSuggestsNewWhenWorktreeMissing(t *testing.T) {
	repo := testRepo(t)
	testConfig(t, repo, "", "")
	_, _, err := runCLI(t, "switch", "repo/missing")
	if err == nil || !strings.Contains(err.Error(), "rexws new repo missing") {
		t.Fatalf("err = %v, want a rexws new hint", err)
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
