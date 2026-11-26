package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newlineWorktree creates a real worktree whose directory name contains a
// newline. `git worktree list --porcelain` does not represent that path as one
// line, so the existing parser records a path that does not exist. Git does
// not mark the real worktree prunable, so this is a genuine skipped candidate.
func newlineWorktree(t *testing.T, repo, root, branch string) string {
	t.Helper()
	path := filepath.Join(root, "feat", "skipped\nline")
	gitRun(t, repo, "worktree", "add", "-q", "-b", branch, path)
	return path
}

func skippedWorktreeFixture(t *testing.T) (repo, readable string) {
	t.Helper()
	repo = testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	readable = filepath.Join(root, "feat", "readable")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/readable", readable)
	newlineWorktree(t, repo, root, "feat/skipped")
	return repo, readable
}

func requireSkippedWarning(t *testing.T, stderr string) {
	t.Helper()
	if !strings.Contains(stderr, "warning:") ||
		!strings.Contains(stderr, "project repo") ||
		!strings.Contains(stderr, "unreadable") {
		t.Fatalf("stderr does not name the skipped worktree: %q", stderr)
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
	newlineWorktree(t, repo, root, "feat/orphan")

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
	newlineWorktree(t, repo, root, "feat/skipped")

	_, stderr, err := runCLI(t, "rm", "repo/feat/skipped")
	if err == nil || !strings.Contains(err.Error(), "has no worktree for branch") {
		t.Fatalf("err = %v\nstderr: %s", err, stderr)
	}
	requireSkippedWarning(t, stderr)
}

func TestRmNoMatchWarnsForRelativePathSelector(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	newlineWorktree(t, repo, root, "feat/skipped")
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
