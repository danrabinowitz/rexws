package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// newlineWorktree creates a real worktree whose directory name contains a
// newline. `git worktree list --porcelain -z` reports that path verbatim, so
// discovery and every selector treat it like any other worktree.
func newlineWorktree(t *testing.T, repo, root, branch string) string {
	t.Helper()
	path := filepath.Join(root, "feat", "new\nline")
	gitRun(t, repo, "worktree", "add", "-q", "-b", branch, path)
	return path
}

// unreadableWorktree reports an extra worktree at path, which must not exist,
// so its path cannot be canonicalized. A healthy Git repository cannot produce
// this state: Git marks a worktree whose directory is missing prunable, and
// discovery ignores prunable worktrees instead of warning about them. The
// discovery seam keeps the skipped-worktree guards testable now that the -z
// parser made newline paths addressable.
func unreadableWorktree(t *testing.T, projectPath, path, branch string) {
	t.Helper()
	project := mustCanonical(t, projectPath)
	original := ops.WorktreeLister
	ops.WorktreeLister = func(ctx context.Context, repoPath string) ([]git.Worktree, error) {
		worktrees, err := original(ctx, repoPath)
		if err != nil {
			return nil, err
		}
		canonical, err := pathutil.Canonical(repoPath)
		if err == nil && canonical == project {
			worktrees = append(worktrees, git.Worktree{
				Path:   path,
				Head:   "3333333333444444",
				Branch: branch,
			})
		}
		return worktrees, nil
	}
	t.Cleanup(func() { ops.WorktreeLister = original })
}

func skippedWorktreeFixture(t *testing.T) (repo, readable string) {
	t.Helper()
	repo = testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	readable = filepath.Join(root, "feat", "readable")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/readable", readable)
	unreadableWorktree(t, repo, filepath.Join(t.TempDir(), "gone"), "feat/skipped")
	return repo, readable
}

func newlineWorktreeFixture(t *testing.T) (repo, path string) {
	t.Helper()
	repo = testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	path = newlineWorktree(t, repo, root, "feat/newline")
	return repo, path
}

func requireSkippedWarning(t *testing.T, stderr string) {
	t.Helper()
	if !strings.Contains(stderr, "warning:") ||
		!strings.Contains(stderr, "project repo") ||
		!strings.Contains(stderr, "unreadable") {
		t.Fatalf("stderr does not name the skipped worktree: %q", stderr)
	}
}

func requireNoSkippedWarning(t *testing.T, stderr string) {
	t.Helper()
	if strings.Contains(stderr, "unreadable") || strings.Contains(stderr, "skipped") {
		t.Fatalf("stderr reports a skipped worktree: %q", stderr)
	}
}

func TestListShowsNewlinePathWorktree(t *testing.T) {
	_, path := newlineWorktreeFixture(t)
	rmFakeRex(t)

	stdout, stderr, err := runCLI(t, "list", "--json")
	if err != nil {
		t.Fatalf("list: %v\nstderr: %s", err, stderr)
	}
	requireNoSkippedWarning(t, stderr)

	var payload ListPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	canonical := mustCanonical(t, path)
	found := false
	for _, ws := range payload.Workstreams {
		if ws.Path == canonical && ws.Branch == "feat/newline" {
			found = true
		}
	}
	if !found {
		t.Errorf("list JSON does not contain the newline-path worktree %q: %#v", canonical, payload.Workstreams)
	}
	for _, warning := range payload.Warnings {
		if strings.Contains(warning, "unreadable") {
			t.Errorf("newline-path worktree was reported skipped: %s", warning)
		}
	}
}

func TestNewlinePathWorktreeIsAddressable(t *testing.T) {
	_, path := newlineWorktreeFixture(t)
	canonical := mustCanonical(t, path)

	selectors := []struct {
		name     string
		selector string
	}{
		{"path", "path:" + path},
		{"identity", "repo/feat/newline"},
		{"branch", "branch:feat/newline"},
	}
	for _, tt := range selectors {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, "status", tt.selector, "in_progress", "--json")
			if err != nil {
				t.Fatalf("status %q: %v\nstderr: %s", tt.selector, err, stderr)
			}
			requireNoSkippedWarning(t, stderr)

			var payload StatusPayload
			if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
				t.Fatalf("parse JSON %q: %v", stdout, err)
			}
			if payload.Path != canonical {
				t.Errorf("path = %q, want %q", payload.Path, canonical)
			}
		})
	}
}

func TestListTableKeepsNewlinePathOnOneLine(t *testing.T) {
	newlineWorktreeFixture(t)
	rmFakeRex(t)

	stdout, stderr, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list: %v\nstderr: %s", err, stderr)
	}
	requireNoSkippedWarning(t, stderr)

	// Header plus the main worktree and the newline-path worktree.
	if lines := strings.Count(stdout, "\n"); lines != 3 {
		t.Errorf("list printed %d lines, want 3:\n%s", lines, stdout)
	}
	if !strings.Contains(stdout, "new?line") || strings.Contains(stdout, "new\nline") {
		t.Errorf("path was not sanitized onto its row:\n%s", stdout)
	}
}

func TestHereResolvesNewlinePathWorktree(t *testing.T) {
	_, path := newlineWorktreeFixture(t)
	nested := filepath.Join(path, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	stdout, stderr, err := runCLI(t, "here", "--json")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	requireNoSkippedWarning(t, stderr)

	var payload HerePayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if payload.Path != mustCanonical(t, path) || payload.Branch != "feat/newline" {
		t.Errorf("here = %#v, want the newline worktree", payload)
	}

	stdout, stderr, err = runCLI(t, "here")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	if lines := strings.Count(stdout, "\n"); lines != 6 {
		t.Errorf("here printed %d lines, want 6:\n%s", lines, stdout)
	}
	var pathLine string
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if strings.HasPrefix(line, "path") {
			pathLine = line
		}
	}
	if !strings.Contains(pathLine, "new?line") || strings.Contains(stdout, "new\nline") {
		t.Errorf("here did not keep the path on its labelled line:\n%s", stdout)
	}
}

// TestHereTableSanitizesDetachedNewlineSelector covers the path-form selector:
// a detached worktree makes ops.ContextSelector return path:<newline path>, so
// the selector line must be sanitized even though --json keeps it exact.
func TestHereTableSanitizesDetachedNewlineSelector(t *testing.T) {
	_, path := newlineWorktreeFixture(t)
	gitRun(t, path, "checkout", "-q", "--detach", "HEAD")
	canonical := mustCanonical(t, path)
	t.Chdir(path)

	stdout, stderr, err := runCLI(t, "here", "--json")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	var payload HerePayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if payload.Selector != "path:"+canonical || !strings.Contains(payload.Selector, "\n") {
		t.Errorf("selector = %q, want the exact path form for %q", payload.Selector, canonical)
	}

	stdout, stderr, err = runCLI(t, "here")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	if lines := strings.Count(stdout, "\n"); lines != 6 {
		t.Errorf("here printed %d lines, want 6:\n%s", lines, stdout)
	}
	var selectorLine string
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if strings.HasPrefix(line, "selector") {
			selectorLine = line
		}
	}
	if !strings.Contains(selectorLine, "new?line") || strings.Contains(stdout, "new\nline") {
		t.Errorf("here did not keep the selector on its labelled line:\n%s", stdout)
	}
}

func TestListSurfacesUnreadableWorktreeWarning(t *testing.T) {
	skippedWorktreeFixture(t)
	rmFakeRex(t)

	stdout, stderr, err := runCLI(t, "list", "--json")
	if err != nil {
		t.Fatalf("list: %v\nstderr: %s", err, stderr)
	}
	requireSkippedWarning(t, stderr)

	var payload ListPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	found := false
	for _, warning := range payload.Warnings {
		if strings.Contains(warning, "project repo") && strings.Contains(warning, "unreadable") {
			found = true
		}
	}
	if !found {
		t.Errorf("list JSON warnings do not include the skipped worktree: %#v", payload.Warnings)
	}
}

func TestSwitchSurfacesUnreadableWorktreeWarning(t *testing.T) {
	skippedWorktreeFixture(t)
	fakeRexNew(t,
		`{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`,
		`{"sessions":[{"session_id":"session:readable","label":"repo/feat/readable"}]}`,
	)

	stdout, stderr, err := runCLI(t, "switch", "feat", "--client", "client:app", "--json")
	if err != nil {
		t.Fatalf("switch: %v\nstderr: %s", err, stderr)
	}
	requireSkippedWarning(t, stderr)

	var payload SwitchPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if payload.SessionName != "repo/feat/readable" {
		t.Errorf("session_name = %q", payload.SessionName)
	}
}

func TestStatusSurfacesUnreadableWorktreeWarning(t *testing.T) {
	_, readable := skippedWorktreeFixture(t)

	stdout, stderr, err := runCLI(t, "status", "feat", "in_progress", "--json")
	if err != nil {
		t.Fatalf("status: %v\nstderr: %s", err, stderr)
	}
	requireSkippedWarning(t, stderr)

	var payload StatusPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if payload.Path != mustCanonical(t, readable) {
		t.Errorf("path = %q, want %q", payload.Path, mustCanonical(t, readable))
	}
}

func TestHereSurfacesUnreadableWorktreeWarning(t *testing.T) {
	_, readable := skippedWorktreeFixture(t)
	t.Chdir(readable)

	stdout, stderr, err := runCLI(t, "here", "--json")
	if err != nil {
		t.Fatalf("here: %v\nstderr: %s", err, stderr)
	}
	requireSkippedWarning(t, stderr)

	var payload HerePayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if payload.Branch != "feat/readable" {
		t.Errorf("branch = %q", payload.Branch)
	}
}

func TestRmBlocksSkippedWorktreeUnlessPathSelector(t *testing.T) {
	_, readable := skippedWorktreeFixture(t)
	rmFakeRex(t)

	stdout, stderr, err := runCLI(t, "rm", "feat")
	if err == nil || !strings.Contains(err.Error(), "worktrees that could not be read may match") {
		t.Fatalf("err = %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(err.Error(), "path:<path>") {
		t.Errorf("refusal does not suggest the escape hatch: %v", err)
	}
	requireSkippedWarning(t, stderr)
	if _, statErr := os.Stat(readable); statErr != nil {
		t.Errorf("readable worktree was removed despite the refusal: %v", statErr)
	}

	stdout, stderr, err = runCLI(t, "rm", "path:"+readable)
	if err != nil {
		t.Fatalf("rm path:: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	requireSkippedWarning(t, stderr)
	if _, statErr := os.Stat(readable); !os.IsNotExist(statErr) {
		t.Errorf("readable worktree still exists: %v", statErr)
	}
}

func TestRmNoMatchIncludesSkippedWorktreeWarning(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	unreadableWorktree(t, repo, filepath.Join(t.TempDir(), "gone"), "feat/orphan")

	_, stderr, err := runCLI(t, "rm", "orphan")
	if err == nil || !strings.Contains(err.Error(), "no workstream matches") {
		t.Fatalf("err = %v\nstderr: %s", err, stderr)
	}
	requireSkippedWarning(t, stderr)
}

func TestRmNoMatchWarnsForProjectBranchSelector(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	unreadableWorktree(t, repo, filepath.Join(t.TempDir(), "gone"), "feat/skipped")

	_, stderr, err := runCLI(t, "rm", "repo/feat/skipped")
	if err == nil || !strings.Contains(err.Error(), "has no worktree for branch") {
		t.Fatalf("err = %v\nstderr: %s", err, stderr)
	}
	requireSkippedWarning(t, stderr)
}

func TestRmNoMatchWarnsForRelativePathSelector(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	testConfig(t, repo, root, "")
	unreadableWorktree(t, repo, filepath.Join(root, "feat", "skipped"), "feat/skipped")
	t.Chdir(root)

	_, stderr, err := runCLI(t, "rm", filepath.Join("feat", "skipped"))
	if err == nil || !strings.Contains(err.Error(), "no registered project") {
		t.Fatalf("err = %v\nstderr: %s", err, stderr)
	}
	requireSkippedWarning(t, stderr)
}

func TestFormatCLIErrorDropsEscapeSequences(t *testing.T) {
	err := errorString("project repo: worktree /tmp/\x1b[31mevil\x1b[0m is unreadable")
	got := formatCLIError(err)
	if strings.ContainsRune(got, 0x1b) || strings.Contains(got, "[31m") {
		t.Fatalf("escape sequence survived: %q", got)
	}
	if !strings.Contains(got, "evil") {
		t.Errorf("sanitized error lost the path text: %q", got)
	}

	collapsed := formatCLIError(errorString("line one\nline two\tindented"))
	if strings.ContainsAny(collapsed, "\n\t") {
		t.Errorf("control characters survived: %q", collapsed)
	}
}

// errorString is a one-value error for the sanitization test.
type errorString string

func (e errorString) Error() string { return string(e) }
