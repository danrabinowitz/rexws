package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

func cleanupRepo(t *testing.T) string {
	t.Helper()
	repo := testRepo(t)
	gitRun(t, repo, "branch", "-M", "main")
	gitRun(t, repo, "config", "user.name", "Test")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	return repo
}

func cleanupCommit(t *testing.T, worktree, file, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, worktree, "add", file)
	gitRun(t, worktree, "commit", "-q", "-m", message)
}

func cleanupFakeRex(t *testing.T, sessions string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "rex.log")
	script := `#!/bin/sh
log="$FAKE_REX_LOG"
case "$*" in
  "session ls --json") printf 'session-ls\n' >> "$log"; printf '%s\n' '` + sessions + `' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`
	bin := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_REX_LOG", logPath)
	t.Setenv(config.EnvRexBin, bin)
	return logPath
}

func TestCleanupCLIPreviewIsReportOnly(t *testing.T) {
	repo := cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	target := filepath.Join(root, "feat", "done")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/done", target)
	cleanupCommit(t, target, "done.txt", "done\n", "done work")
	gitRun(t, repo, "merge", "-q", "--ff-only", "feat/done")
	cleanupFakeRex(t, `{"sessions":[]}`)

	stdout, stderr, err := runCLI(t, "cleanup", "--json")
	if err != nil {
		t.Fatalf("cleanup --json: %v\nstderr: %s", err, stderr)
	}
	var report struct {
		SchemaVersion int  `json:"schema_version"`
		Applied       bool `json:"applied"`
		Projects      []struct {
			Items []struct {
				Branch         string `json:"branch"`
				Classification string `json:"classification"`
				Action         string `json:"action"`
				Removable      bool   `json:"removable"`
			} `json:"items"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if report.SchemaVersion != 1 || report.Applied {
		t.Errorf("report = %#v", report)
	}
	var found bool
	for _, project := range report.Projects {
		for _, item := range project.Items {
			if item.Branch == "feat/done" {
				found = true
				if item.Classification != "merged" || item.Action != "remove-worktree-and-branch" || !item.Removable {
					t.Errorf("item = %#v", item)
				}
			}
		}
	}
	if !found {
		t.Fatalf("feat/done not in report: %s", stdout)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("preview removed the worktree: %v", err)
	}

	human, _, err := runCLI(t, "cleanup")
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if !strings.Contains(human, "Preview only: nothing was changed.") {
		t.Errorf("human preview = %q", human)
	}
	if !strings.Contains(human, "merged") || !strings.Contains(human, "feat/done") {
		t.Errorf("human preview is missing the merged item: %q", human)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("preview removed the worktree: %v", err)
	}
}

func TestCleanupCLIForcePrintsLossBeforeActing(t *testing.T) {
	repo := cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	dirty := filepath.Join(root, "feat", "dirty")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/dirty", dirty)
	cleanupCommit(t, dirty, "dirty.txt", "dirty\n", "dirty work")
	gitRun(t, repo, "merge", "-q", "--ff-only", "feat/dirty")
	if err := os.WriteFile(filepath.Join(dirty, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	unmerged := filepath.Join(root, "feat", "unmerged")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/unmerged", unmerged)
	cleanupCommit(t, unmerged, "unmerged.txt", "unmerged\n", "unmerged work")

	cleanupFakeRex(t, `{"sessions":[]}`)

	stdout, stderr, err := runCLI(t, "cleanup", "--apply", "--force")
	if err != nil {
		t.Fatalf("cleanup --apply --force: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "LOSS") {
		t.Errorf("stderr does not name the loss: %q", stderr)
	}
	if !strings.Contains(stderr, "untracked.txt") {
		t.Errorf("stderr does not name the dirty file: %q", stderr)
	}
	if !strings.Contains(stderr, "commits not in main") || !strings.Contains(stderr, "unmerged work") {
		t.Errorf("stderr does not name the unmerged commits: %q", stderr)
	}
	if !strings.Contains(stdout, "2 removed") || strings.Contains(stdout, "nothing was changed") {
		t.Errorf("apply output = %q", stdout)
	}
	for _, path := range []string{dirty, unmerged} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("worktree %s still exists: %v", path, err)
		}
	}
}

func TestCleanupCLIProjectFilter(t *testing.T) {
	repo := cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	other := cleanupRepo(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	otherPath, err := pathutil.Canonical(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.RegisterProject(otherPath, "other", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	cleanupFakeRex(t, `{"sessions":[]}`)

	stdout, stderr, err := runCLI(t, "cleanup", "--json")
	if err != nil {
		t.Fatalf("cleanup: %v\nstderr: %s", err, stderr)
	}
	var all struct {
		Projects []struct {
			Project string `json:"project"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(stdout), &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Projects) != 2 || all.Projects[0].Project != "other" || all.Projects[1].Project != "repo" {
		t.Errorf("all projects = %#v", all.Projects)
	}

	stdout, stderr, err = runCLI(t, "cleanup", "--json", "--project", "repo")
	if err != nil {
		t.Fatalf("cleanup --project: %v\nstderr: %s", err, stderr)
	}
	var filtered struct {
		Projects []struct {
			Project string `json:"project"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(stdout), &filtered); err != nil {
		t.Fatal(err)
	}
	if len(filtered.Projects) != 1 || filtered.Projects[0].Project != "repo" {
		t.Errorf("filtered projects = %#v", filtered.Projects)
	}

	if _, _, err := runCLI(t, "cleanup", "--project", "missing"); err == nil ||
		!strings.Contains(err.Error(), "unknown project") {
		t.Errorf("unknown project err = %v", err)
	}
}

func TestCleanupCLIPartialProjectFailureKeepsJSONPure(t *testing.T) {
	repo := cleanupRepo(t)
	testConfig(t, repo, "", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Projects = append(cfg.Projects, config.Project{
		Name: "missing",
		Path: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	cleanupFakeRex(t, `{"sessions":[]}`)

	stdout, _, err := runCLI(t, "cleanup", "--json")
	if err == nil {
		t.Fatal("expected a nonzero result for the unreadable project")
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exitCode = %d, want 1", got)
	}
	var document struct {
		Projects []struct {
			Project string `json:"project"`
		} `json:"projects"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, stdout)
	}
	if len(document.Errors) == 0 || !strings.Contains(strings.Join(document.Errors, " "), "missing") {
		t.Errorf("errors = %#v", document.Errors)
	}
	if len(document.Projects) != 1 || document.Projects[0].Project != "repo" {
		t.Errorf("projects = %#v", document.Projects)
	}
}

func TestCleanupCLIJSONApplyForceKeepsStdoutPure(t *testing.T) {
	repo := cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	dirty := filepath.Join(root, "feat", "dirty")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/dirty", dirty)
	cleanupCommit(t, dirty, "dirty.txt", "dirty\n", "dirty work")
	gitRun(t, repo, "merge", "-q", "--ff-only", "feat/dirty")
	if err := os.WriteFile(filepath.Join(dirty, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupFakeRex(t, `{"sessions":[]}`)

	stdout, stderr, err := runCLI(t, "cleanup", "--json", "--apply", "--force")
	if err != nil {
		t.Fatalf("cleanup --json --apply --force: %v\nstderr: %s", err, stderr)
	}
	var report struct {
		Applied bool `json:"applied"`
		Totals  struct {
			Removed int `json:"removed"`
			Failed  int `json:"failed"`
		} `json:"totals"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, stdout)
	}
	if !report.Applied || report.Totals.Removed != 1 || report.Totals.Failed != 0 {
		t.Errorf("report = %#v", report)
	}
	if !strings.Contains(stderr, "LOSS") || !strings.Contains(stderr, "untracked.txt") {
		t.Errorf("stderr does not name the planned loss: %q", stderr)
	}
	if _, err := os.Stat(dirty); !os.IsNotExist(err) {
		t.Errorf("forced worktree still exists: %v", err)
	}
}

func TestCleanupCLIForcePreviewPrintsLossWithoutActing(t *testing.T) {
	repo := cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	dirty := filepath.Join(root, "feat", "dirty")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/dirty", dirty)
	cleanupCommit(t, dirty, "dirty.txt", "dirty\n", "dirty work")
	gitRun(t, repo, "merge", "-q", "--ff-only", "feat/dirty")
	if err := os.WriteFile(filepath.Join(dirty, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupFakeRex(t, `{"sessions":[]}`)

	stdout, stderr, err := runCLI(t, "cleanup", "--force")
	if err != nil {
		t.Fatalf("cleanup --force preview: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "LOSS") || !strings.Contains(stderr, "untracked.txt") {
		t.Errorf("force preview does not name the loss: %q", stderr)
	}
	if !strings.Contains(stdout, "Preview only: nothing was changed.") {
		t.Errorf("stdout = %q", stdout)
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Errorf("force preview removed the worktree: %v", err)
	}
}

func TestCleanupCLIApplyRefusesWhenRexIsUnavailable(t *testing.T) {
	repo := cleanupRepo(t)
	testConfig(t, repo, "", "")
	bin := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvRexBin, bin)

	stdout, stderr, err := runCLI(t, "cleanup")
	if err != nil {
		t.Fatalf("preview: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "Rex unavailable") || !strings.Contains(stdout, "Preview only: nothing was changed") {
		t.Errorf("stdout=%q stderr=%q", stdout, stderr)
	}

	if _, _, err := runCLI(t, "cleanup", "--apply"); err == nil ||
		!strings.Contains(err.Error(), "cannot verify live Rex sessions") {
		t.Errorf("apply err = %v", err)
	}
}
