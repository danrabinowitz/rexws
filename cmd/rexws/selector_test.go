package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/pathutil"
	"github.com/danrabinowitz/rexws/internal/selector"
)

func mustCanonical(t *testing.T, path string) string {
	t.Helper()
	canonical, err := pathutil.Canonical(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestSwitchResolvesBranchActiveAndSubstring(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	target := filepath.Join(root, "feat", "auth")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/auth", target)

	fakeRexNew(t,
		`{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`,
		`{"sessions":[{"session_id":"session:auth","label":"repo/feat/auth"}]}`,
	)

	for _, input := range []string{"branch:feat/auth", "feat/auth"} {
		stdout, stderr, err := runCLI(t, "switch", input, "--client", "client:app", "--json")
		if err != nil {
			t.Fatalf("switch %q: %v\nstderr: %s", input, err, stderr)
		}
		var result struct {
			SessionName string `json:"session_name"`
			SessionID   string `json:"session_id"`
			Created     bool   `json:"created"`
		}
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("parse JSON %q: %v", stdout, err)
		}
		if result.SessionName != "repo/feat/auth" || result.SessionID != "session:auth" || result.Created {
			t.Errorf("switch %q = %#v", input, result)
		}
	}

	nested := filepath.Join(target, "internal")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	stdout, stderr, err := runCLI(t, "switch", "active", "--client", "client:app", "--json")
	if err != nil {
		t.Fatalf("switch active: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		SessionName string `json:"session_name"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if result.SessionName != "repo/feat/auth" {
		t.Errorf("switch active session = %q", result.SessionName)
	}
}

func TestStatusResolvesSelectors(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	alpha := filepath.Join(root, "feat", "alpha")
	beta := filepath.Join(root, "feat", "beta")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/alpha", alpha)
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/beta", beta)
	alphaPath := mustCanonical(t, alpha)
	betaPath := mustCanonical(t, beta)

	for _, tt := range []struct {
		input string
		want  string
	}{
		{"branch:feat/alpha", alphaPath},
		{"path:" + beta, betaPath},
		{"name:repo/feat/alpha", alphaPath},
		{betaPath, betaPath},
		{"BETA", betaPath},
	} {
		stdout, stderr, err := runCLI(t, "status", tt.input, "in_progress", "--json")
		if err != nil {
			t.Fatalf("status %q: %v\nstderr: %s", tt.input, err, stderr)
		}
		var change struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(stdout), &change); err != nil {
			t.Fatalf("parse JSON %q: %v", stdout, err)
		}
		if change.Path != tt.want {
			t.Errorf("status %q path = %s, want %s", tt.input, change.Path, tt.want)
		}
	}

	_, _, err := runCLI(t, "status", "feat", "in_progress")
	if !errors.Is(err, selector.ErrAmbiguous) {
		t.Fatalf("ambiguous status err = %v, want ErrAmbiguous", err)
	}
	for _, want := range []string{"repo/feat/alpha", "repo/feat/beta"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error does not list %q: %v", want, err)
		}
	}

	_, _, err = runCLI(t, "status", "missing", "in_progress")
	if !errors.Is(err, selector.ErrNoMatch) {
		t.Fatalf("no-match status err = %v, want ErrNoMatch", err)
	}
	if got := exitCode(err); got != 2 {
		t.Errorf("no-match exit code = %d, want 2", got)
	}

	_, _, err = runCLI(t, "status", "active", "in_progress")
	if !errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Fatalf("active status err = %v, want ErrNoActiveWorkstream", err)
	}
	if got := exitCode(err); got != 4 {
		t.Errorf("no-active exit code = %d, want 4", got)
	}
}

func TestRmResolvesSelector(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	target := filepath.Join(root, "feat", "remove")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/remove", target)
	targetPath := mustCanonical(t, target)

	fake := filepath.Join(t.TempDir(), "rex")
	script := `#!/bin/sh
case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[{"session_id":"session:remove","label":"repo/feat/remove"}]}' ;;
  "session kill session:remove") printf '%s\n' killed ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REXWS_REX_BIN", fake)

	stdout, stderr, err := runCLI(t, "rm", "branch:feat/remove", "--json")
	if err != nil {
		t.Fatalf("rm: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Path            string `json:"path"`
		ClosedSessionID string `json:"closed_session_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if result.Path != targetPath {
		t.Errorf("removed path = %q", result.Path)
	}
	if result.ClosedSessionID != "session:remove" {
		t.Errorf("closed session = %q", result.ClosedSessionID)
	}
	if !strings.Contains(stderr, "Removing repo/feat/remove ("+targetPath+")") {
		t.Errorf("stderr does not echo the resolved target: %q", stderr)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
}

func TestRmEchoesResolvedTargetInTextMode(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	target := filepath.Join(root, "feat", "echo")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/echo", target)
	targetPath := mustCanonical(t, target)

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

	stdout, stderr, err := runCLI(t, "rm", "branch:feat/echo")
	if err != nil {
		t.Fatalf("rm: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Removing repo/feat/echo ("+targetPath+")") {
		t.Errorf("stdout does not echo the resolved target: %q", stdout)
	}
	if !strings.Contains(stdout, "Removed worktree "+targetPath) {
		t.Errorf("stdout does not report the removal: %q", stdout)
	}
}

func TestSwitchSelectorErrors(t *testing.T) {
	repo := testRepo(t)
	testConfig(t, repo, "", "")

	if _, _, err := runCLI(t, "switch", "does-not-exist"); !errors.Is(err, selector.ErrNoMatch) {
		t.Fatalf("unknown selector err = %v, want ErrNoMatch", err)
	}
	if _, _, err := runCLI(t, "switch", "path:/does/not/exist"); !errors.Is(err, selector.ErrNoMatch) {
		t.Fatalf("unknown path err = %v, want ErrNoMatch", err)
	}
	_, _, err := runCLI(t, "switch", "repo/missing")
	if err == nil || !strings.Contains(err.Error(), "rexws new repo missing") {
		t.Fatalf("missing worktree err = %v, want a rexws new hint", err)
	}
	if got := exitCode(err); got != 2 {
		t.Errorf("missing worktree exit code = %d, want 2", got)
	}
	if _, _, err := runCLI(t, "switch", "nope/main"); err == nil || !strings.Contains(err.Error(), "no registered project") {
		t.Fatalf("unknown project err = %v, want the registered-project message", err)
	}
}

func TestStatusAndRmKeepProjectHints(t *testing.T) {
	repo := testRepo(t)
	testConfig(t, repo, "", "")

	for _, args := range [][]string{
		{"status", "repo/missing", "in_progress"},
		{"rm", "repo/missing"},
	} {
		_, _, err := runCLI(t, args...)
		if !errors.Is(err, selector.ErrNoMatch) || !strings.Contains(err.Error(), "rexws new repo missing") {
			t.Errorf("%v err = %v, want a rexws new hint", args, err)
		}
		if got := exitCode(err); got != 2 {
			t.Errorf("%v exit code = %d, want 2", args, got)
		}
	}
}

func TestResolveErrorPreservesCause(t *testing.T) {
	cfg := config.Default()
	cfg.Projects = []config.Project{{Name: "repo", Path: "/tmp/repo"}}
	cause := fmt.Errorf("%w; some projects could not be read: %w",
		&selector.NoMatchError{Selector: "repo/missing"},
		ops.ProjectError{Project: "bad", Err: errors.New("boom")})
	err := resolveError(cfg, "repo/missing", cause)

	if !errors.Is(err, selector.ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	var noMatch *selector.NoMatchError
	if !errors.As(err, &noMatch) {
		t.Fatalf("err = %v, want the typed no-match cause", err)
	}
	var projectErr ops.ProjectError
	if !errors.As(err, &projectErr) || projectErr.Project != "bad" {
		t.Fatalf("err = %v, want the discovery error in the chain", err)
	}
}

func TestRmEchoPrecedesProvider(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	t.Setenv(config.EnvDir, filepath.Join(t.TempDir(), "config"))
	cfg := config.Default()
	cfg.Providers["marker"] = config.Provider{Remove: []string{"sh", "-c", "echo provider-ran; git -C '{{.RepoPath}}' worktree remove '{{.WorktreePath}}'"}}
	if _, _, err := cfg.RegisterProject(mustCanonical(t, repo), "repo", "marker", root); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "feat", "order")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/order", target)
	targetPath := mustCanonical(t, target)

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

	stdout, stderr, err := runCLI(t, "rm", "branch:feat/order")
	if err != nil {
		t.Fatalf("rm: %v\nstderr: %s", err, stderr)
	}
	echo := strings.Index(stdout, "Removing repo/feat/order ("+targetPath+")")
	provider := strings.Index(stdout, "provider-ran")
	if echo < 0 || provider < 0 {
		t.Fatalf("stdout = %q, want both the echo and provider output", stdout)
	}
	if echo > provider {
		t.Errorf("rm echo came after the provider ran:\n%s", stdout)
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"other", errors.New("boom"), 1},
		{"no match", &selector.NoMatchError{Selector: "x"}, 2},
		{"wrapped no match", fmt.Errorf("context: %w", &selector.NoMatchError{Selector: "x"}), 2},
		{"ambiguous", &selector.AmbiguousError{Selector: "x"}, 3},
		{"wrapped ambiguous", fmt.Errorf("context: %w", selector.ErrAmbiguous), 3},
		{"no active", &selector.NoActiveWorkstreamError{Dir: "/tmp"}, 4},
		{"hint", hintError{message: "create it"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.err); got != tt.want {
				t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
