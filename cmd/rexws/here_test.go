package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/selector"
)

// gitOutput returns trimmed git output for a test repository.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// hereFixture is a registered repository with one branch worktree.
type hereFixture struct {
	repo       string
	root       string
	target     string
	targetPath string
}

func newHereFixture(t *testing.T) hereFixture {
	t.Helper()
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	target := filepath.Join(root, "feat", "auth")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/auth", target)
	return hereFixture{repo: repo, root: root, target: target, targetPath: mustCanonical(t, target)}
}

func decodeHere(t *testing.T, stdout string) hereJSON {
	t.Helper()
	var got hereJSON
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	return got
}

func TestHereResolvesWorktreeRootAndSubdirectory(t *testing.T) {
	f := newHereFixture(t)
	nested := filepath.Join(f.target, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "auth-link")
	if err := os.Symlink(f.target, link); err != nil {
		t.Fatal(err)
	}

	want := hereJSON{
		Project:             "repo",
		Branch:              "feat/auth",
		Path:                f.targetPath,
		SessionName:         "repo/feat/auth",
		WorkflowStatus:      config.StatusReady,
		WorkflowStatusLabel: "Ready",
		Selector:            "repo/feat/auth",
	}
	for _, tt := range []struct {
		name string
		dir  string
		pwd  string
	}{
		{"worktree root", f.target, ""},
		{"subdirectory", nested, ""},
		// PWD makes os.Getwd return the logical symlink path, exercising the
		// symlink branch rather than only the physical path.
		{"symlinked process directory", link, link},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(tt.dir)
			if tt.pwd != "" {
				t.Setenv("PWD", tt.pwd)
			}
			stdout, stderr, err := runCLI(t, "here", "--json")
			if err != nil {
				t.Fatalf("here: %v\nstderr: %s", err, stderr)
			}
			if got := decodeHere(t, stdout); got != want {
				t.Errorf("here = %#v, want %#v", got, want)
			}
		})
	}
}

func TestHereDirFlagAcceptsSymlinkTrailingSlashAndRelativePaths(t *testing.T) {
	f := newHereFixture(t)
	nested := filepath.Join(f.target, "internal")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "auth-link")
	if err := os.Symlink(f.target, link); err != nil {
		t.Fatal(err)
	}

	t.Chdir(t.TempDir())
	for _, tt := range []struct {
		name string
		dir  string
	}{
		{"absolute path", f.target},
		{"trailing slash", f.target + string(os.PathSeparator)},
		{"symlink", link},
		{"subdirectory", nested},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, "here", "--dir", tt.dir, "--json")
			if err != nil {
				t.Fatalf("here --dir %q: %v\nstderr: %s", tt.dir, err, stderr)
			}
			if got := decodeHere(t, stdout); got.Path != f.targetPath {
				t.Errorf("here --dir %q path = %q, want %q", tt.dir, got.Path, f.targetPath)
			}
		})
	}

	t.Run("relative to the process directory", func(t *testing.T) {
		t.Chdir(f.root)
		stdout, stderr, err := runCLI(t, "here", "--dir", filepath.Join("feat", "auth"), "--json")
		if err != nil {
			t.Fatalf("here --dir relative: %v\nstderr: %s", err, stderr)
		}
		if got := decodeHere(t, stdout); got.Path != f.targetPath {
			t.Errorf("path = %q, want %q", got.Path, f.targetPath)
		}
	})

	t.Run("home-relative", func(t *testing.T) {
		t.Setenv("HOME", f.root)
		stdout, stderr, err := runCLI(t, "here", "--dir", "~/feat/auth", "--json")
		if err != nil {
			t.Fatalf("here --dir ~/...: %v\nstderr: %s", err, stderr)
		}
		if got := decodeHere(t, stdout); got.Path != f.targetPath {
			t.Errorf("path = %q, want %q", got.Path, f.targetPath)
		}
	})
}

func TestHereDirFlagAcceptsFilesInsideWorktrees(t *testing.T) {
	f := newHereFixture(t)
	inside := filepath.Join(f.target, "README.md")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLI(t, "here", "--dir", inside, "--json")
	if err != nil {
		t.Fatalf("here --dir file inside: %v\nstderr: %s", err, stderr)
	}
	if got := decodeHere(t, stdout); got.Path != f.targetPath {
		t.Errorf("path = %q, want the containing worktree %q", got.Path, f.targetPath)
	}

	outside := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = runCLI(t, "here", "--dir", outside)
	if !errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Fatalf("file outside err = %v, want ErrNoActiveWorkstream", err)
	}
	if got := exitCode(err); got != 4 {
		t.Errorf("file outside exit code = %d, want 4", got)
	}
}

func TestHereOutsideEveryWorktreeFails(t *testing.T) {
	newHereFixture(t)
	outside := t.TempDir()
	t.Chdir(outside)

	for _, args := range [][]string{
		{"here"},
		{"here", "--dir", outside},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, err := runCLI(t, args...)
			if !errors.Is(err, selector.ErrNoActiveWorkstream) {
				t.Fatalf("%v err = %v, want ErrNoActiveWorkstream", args, err)
			}
			if stdout != "" {
				t.Errorf("%v stdout = %q, want no output on failure", args, stdout)
			}
			if got := exitCode(err); got != 4 {
				t.Errorf("%v exit code = %d, want 4", args, got)
			}
			if !strings.Contains(err.Error(), "not inside a registered workstream") {
				t.Errorf("%v err = %v, want the not-inside message", args, err)
			}
		})
	}
}

func TestHereNonexistentDirFails(t *testing.T) {
	newHereFixture(t)
	missing := filepath.Join(t.TempDir(), "missing")

	_, _, err := runCLI(t, "here", "--dir", missing)
	if err == nil {
		t.Fatal("here --dir missing succeeded")
	}
	if errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Errorf("err = %v, want a bad-input error rather than a workstream miss", err)
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), `--dir "`+missing+`"`) {
		t.Errorf("err = %v, want the flag and path named", err)
	}
}

func TestHereEmptyDirFlagFails(t *testing.T) {
	newHereFixture(t)
	_, _, err := runCLI(t, "here", "--dir", "")
	if err == nil || !strings.Contains(err.Error(), "--dir") {
		t.Fatalf("err = %v, want a --dir value error", err)
	}
}

func TestHereHumanOutput(t *testing.T) {
	f := newHereFixture(t)
	t.Chdir(f.target)

	stdout, stderr, err := runCLI(t, "here")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	for _, want := range []string{
		"project   repo\n",
		"branch    feat/auth\n",
		"path      " + f.targetPath + "\n",
		"session   repo/feat/auth\n",
		"status    Ready\n",
		"selector  repo/feat/auth\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want line %q", stdout, want)
		}
	}
}

func TestHereReportsWorkflowStatusFromState(t *testing.T) {
	f := newHereFixture(t)
	state := config.NewState()
	state.SetStatus(f.targetPath, config.StatusInProgress, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}

	// Rex is not the source of the workflow status, and here must not call it.
	logPath := filepath.Join(t.TempDir(), "rex.log")
	t.Setenv("FAKE_REX_LOG", logPath)
	fake := filepath.Join(t.TempDir(), "rex")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_REX_LOG"
exit 1
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvRexBin, fake)

	t.Chdir(f.target)
	stdout, stderr, err := runCLI(t, "here", "--json")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	got := decodeHere(t, stdout)
	if got.WorkflowStatus != config.StatusInProgress || got.WorkflowStatusLabel != "In Progress" {
		t.Errorf("workflow status = (%q, %q), want (in_progress, In Progress)", got.WorkflowStatus, got.WorkflowStatusLabel)
	}
	if calls, err := os.ReadFile(logPath); err == nil {
		t.Errorf("here invoked Rex: %s", calls)
	} else if !os.IsNotExist(err) {
		t.Fatalf("read Rex log: %v", err)
	}
}

func TestHereSelectorRoundTripsThroughOtherCommands(t *testing.T) {
	f := newHereFixture(t)
	fakeRexNew(t,
		`{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`,
		`{"sessions":[{"session_id":"session:auth","label":"repo/feat/auth"}]}`,
	)
	t.Chdir(f.target)

	stdout, stderr, err := runCLI(t, "here", "--json")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	got := decodeHere(t, stdout)
	if got.Selector != "repo/feat/auth" || got.SessionName != "repo/feat/auth" {
		t.Fatalf("selector = %q, session_name = %q, want the unique exact label", got.Selector, got.SessionName)
	}

	stdout, stderr, err = runCLI(t, "status", got.Selector, "blocked", "--json")
	if err != nil {
		t.Fatalf("status %q: %v\nstderr: %s", got.Selector, err, stderr)
	}
	var change struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(stdout), &change); err != nil {
		t.Fatalf("parse status JSON %q: %v", stdout, err)
	}
	if change.Path != f.targetPath {
		t.Errorf("status path = %q, want %q", change.Path, f.targetPath)
	}

	stdout, stderr, err = runCLI(t, "switch", got.Selector, "--client", "client:app", "--json")
	if err != nil {
		t.Fatalf("switch %q: %v\nstderr: %s", got.Selector, err, stderr)
	}
	var switched struct {
		SessionName string `json:"session_name"`
		SessionID   string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &switched); err != nil {
		t.Fatalf("parse switch JSON %q: %v", stdout, err)
	}
	if switched.SessionName != "repo/feat/auth" || switched.SessionID != "session:auth" {
		t.Errorf("switch = %#v", switched)
	}
}

func TestHereResolvesMainCheckout(t *testing.T) {
	f := newHereFixture(t)
	branch := gitOutput(t, f.repo, "rev-parse", "--abbrev-ref", "HEAD")
	repoPath := mustCanonical(t, f.repo)

	t.Chdir(f.repo)
	stdout, stderr, err := runCLI(t, "here", "--json")
	if err != nil {
		t.Fatalf("here in main checkout: %v\nstderr: %s", err, stderr)
	}
	got := decodeHere(t, stdout)
	if got.Path != repoPath || got.Branch != branch || got.SessionName != "repo/"+branch {
		t.Errorf("here = %#v, want main checkout %s on %s", got, repoPath, branch)
	}
	if got.Selector != "repo/"+branch {
		t.Errorf("selector = %q, want the unique label", got.Selector)
	}
}

func TestHereDuplicateAttachedLabelUsesPathForm(t *testing.T) {
	f := newHereFixture(t)
	dup := filepath.Join(f.root, "feat", "auth-dup")
	gitRun(t, f.repo, "worktree", "add", "-q", "--force", dup, "feat/auth")
	dupPath := mustCanonical(t, dup)

	for _, tt := range []struct {
		name string
		dir  string
		want string
	}{
		{"first checkout", f.target, f.targetPath},
		{"duplicate checkout", dup, dupPath},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, "here", "--dir", tt.dir, "--json")
			if err != nil {
				t.Fatalf("here --dir %q: %v\nstderr: %s", tt.dir, err, stderr)
			}
			got := decodeHere(t, stdout)
			if got.SessionName != "repo/feat/auth" {
				t.Errorf("session_name = %q, want the shared label", got.SessionName)
			}
			if got.Selector != "path:"+tt.want {
				t.Fatalf("selector = %q, want the path form", got.Selector)
			}

			// The emitted selector must resolve to this worktree, not the
			// other one that shares its label.
			stdout, stderr, err = runCLI(t, "status", got.Selector, "blocked", "--json")
			if err != nil {
				t.Fatalf("status %q: %v\nstderr: %s", got.Selector, err, stderr)
			}
			var change struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(stdout), &change); err != nil {
				t.Fatalf("parse status JSON %q: %v", stdout, err)
			}
			if change.Path != tt.want {
				t.Errorf("status path = %q, want %q", change.Path, tt.want)
			}
		})
	}
}

func TestHereDetachedWorktreeUsesPathForm(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	target := filepath.Join(root, "detached")
	gitRun(t, repo, "worktree", "add", "-q", "--detach", target, "HEAD")
	targetPath := mustCanonical(t, target)
	short := gitOutput(t, target, "rev-parse", "--short=7", "HEAD")

	t.Chdir(target)
	stdout, stderr, err := runCLI(t, "here", "--json")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	got := decodeHere(t, stdout)
	if !got.Detached {
		t.Error("detached = false, want true")
	}
	if got.SessionName != "repo/"+short {
		t.Errorf("session_name = %q, want repo/%s", got.SessionName, short)
	}
	if got.Selector != "path:"+targetPath {
		t.Fatalf("selector = %q, want path:%s even when the label is unique", got.Selector, targetPath)
	}

	stdout, stderr, err = runCLI(t, "status", got.Selector, "blocked", "--json")
	if err != nil {
		t.Fatalf("status %q: %v\nstderr: %s", got.Selector, err, stderr)
	}
	var change struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(stdout), &change); err != nil {
		t.Fatalf("parse status JSON %q: %v", stdout, err)
	}
	if change.Path != targetPath {
		t.Errorf("status path = %q, want %q", change.Path, targetPath)
	}

	stdout, _, err = runCLI(t, "here")
	if err != nil {
		t.Fatalf("here: %v", err)
	}
	if !strings.Contains(stdout, "selector  path:"+targetPath+"\n") {
		t.Errorf("stdout = %q, want the path selector line", stdout)
	}
}

func TestHereBranchNamedLikeHeadAndDetachedWorktrees(t *testing.T) {
	f := newHereFixture(t)
	short := gitOutput(t, f.repo, "rev-parse", "--short=7", "HEAD")
	gitRun(t, f.repo, "branch", short)
	attached := filepath.Join(f.root, "collision-branch")
	detached := filepath.Join(f.root, "collision-detached")
	gitRun(t, f.repo, "worktree", "add", "-q", attached, short)
	gitRun(t, f.repo, "worktree", "add", "-q", "--detach", detached, "HEAD")

	for _, tt := range []struct {
		name       string
		dir        string
		wantPath   string
		wantDetach bool
	}{
		{"branch sharing the commit label", attached, mustCanonical(t, attached), false},
		{"detached at the same commit", detached, mustCanonical(t, detached), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, "here", "--dir", tt.dir, "--json")
			if err != nil {
				t.Fatalf("here --dir %q: %v\nstderr: %s", tt.dir, err, stderr)
			}
			got := decodeHere(t, stdout)
			if got.SessionName != "repo/"+short {
				t.Errorf("session_name = %q, want the shared repo/%s", got.SessionName, short)
			}
			if got.Detached != tt.wantDetach {
				t.Errorf("detached = %v, want %v", got.Detached, tt.wantDetach)
			}
			if got.Selector != "path:"+tt.wantPath {
				t.Fatalf("selector = %q, want the path form", got.Selector)
			}

			stdout, stderr, err = runCLI(t, "status", got.Selector, "blocked", "--json")
			if err != nil {
				t.Fatalf("status %q: %v\nstderr: %s", got.Selector, err, stderr)
			}
			var change struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(stdout), &change); err != nil {
				t.Fatalf("parse status JSON %q: %v", stdout, err)
			}
			if change.Path != tt.wantPath {
				t.Errorf("status path = %q, want %q", change.Path, tt.wantPath)
			}
		})
	}
}

func TestHereWithNoProjectsSuggestsProjectAdd(t *testing.T) {
	t.Setenv(config.EnvDir, filepath.Join(t.TempDir(), "config"))
	t.Chdir(t.TempDir())

	_, _, err := runCLI(t, "here")
	if !errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Fatalf("err = %v, want ErrNoActiveWorkstream", err)
	}
	if got := exitCode(err); got != 4 {
		t.Errorf("exit code = %d, want 4", got)
	}
	if !strings.Contains(err.Error(), "rexws project add <path>") {
		t.Errorf("err = %v, want the project-add hint", err)
	}
}
