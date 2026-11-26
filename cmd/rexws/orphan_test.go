package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// orphanCLIFixture creates one tracked worktree and one state entry whose path
// was never created. Only the latter is provably stale.
func orphanCLIFixture(t *testing.T) (repo, current, stale, rexLog string) {
	t.Helper()
	repo = cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")

	current = filepath.Join(root, "feat", "current")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/current", current)
	currentPath, err := pathutil.Canonical(current)
	if err != nil {
		t.Fatal(err)
	}

	state := config.NewState()
	state.SetStatus(currentPath, config.StatusInProgress, time.Now())
	stale = filepath.Join(root, "feat", "missing")
	state.SetStatus(stale, config.StatusReady, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}

	rexLog = cleanupFakeRex(t, `{"sessions":[]}`)
	return repo, currentPath, stale, rexLog
}

func stateEntryExists(t *testing.T, path string) bool {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	state, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	_, ok := state.Workstreams[path]
	return ok
}

func runCLIWithInput(t *testing.T, input string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut lockedBuffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(input))
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func setCleanupTerminal(t *testing.T, interactive bool) {
	t.Helper()
	original := cleanupStdinIsTerminal
	cleanupStdinIsTerminal = func(*cobra.Command) bool { return interactive }
	t.Cleanup(func() { cleanupStdinIsTerminal = original })
}

func TestCleanupFixRequiresConfirmationForScripts(t *testing.T) {
	_, _, stale, _ := orphanCLIFixture(t)
	setCleanupTerminal(t, false)

	stdout, stderr, err := runCLI(t, "cleanup", "--fix", "--json")
	if err == nil || !strings.Contains(err.Error(), "--fix --yes") {
		t.Fatalf("cleanup --fix --json err = %v", err)
	}
	var report struct {
		Orphans struct {
			Fixed  bool `json:"fixed"`
			Totals struct {
				Scanned int `json:"scanned"`
				Fixable int `json:"fixable"`
				Removed int `json:"removed"`
			} `json:"totals"`
		} `json:"orphans"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, stdout)
	}
	if report.Orphans.Fixed || report.Orphans.Totals.Scanned != 1 ||
		report.Orphans.Totals.Fixable != 1 || report.Orphans.Totals.Removed != 0 {
		t.Errorf("orphans = %#v", report.Orphans)
	}
	if strings.Contains(stderr, "[y/N]") {
		t.Errorf("scripted run printed a prompt: %q", stderr)
	}
	if !stateEntryExists(t, stale) {
		t.Error("state entry was pruned without confirmation")
	}

	stdout, _, err = runCLI(t, "cleanup", "--fix")
	if err == nil || !strings.Contains(err.Error(), "--fix --yes") {
		t.Fatalf("cleanup --fix err = %v", err)
	}
	if !strings.Contains(stdout, "Orphans:") {
		t.Errorf("non-interactive refusal did not print the report: %q", stdout)
	}
	if !stateEntryExists(t, stale) {
		t.Error("state entry was pruned without confirmation")
	}

	// JSON is for scripts even when stdin happens to be a terminal.
	setCleanupTerminal(t, true)
	_, stderr, err = runCLI(t, "cleanup", "--fix", "--json")
	if err == nil || !strings.Contains(err.Error(), "--fix --yes") {
		t.Fatalf("cleanup --fix --json on a terminal err = %v", err)
	}
	if strings.Contains(stderr, "[y/N]") {
		t.Errorf("JSON run prompted on a terminal: %q", stderr)
	}
	if !stateEntryExists(t, stale) {
		t.Error("state entry was pruned by a JSON run without --yes")
	}
}

func TestCleanupFixYesPrunesStaleStateOnly(t *testing.T) {
	_, current, stale, rexLog := orphanCLIFixture(t)
	setCleanupTerminal(t, false)

	stdout, stderr, err := runCLI(t, "cleanup", "--json", "--fix", "--yes")
	if err != nil {
		t.Fatalf("cleanup --json --fix --yes: %v\nstderr: %s", err, stderr)
	}
	var report struct {
		Applied bool `json:"applied"`
		Orphans struct {
			Fixed  bool `json:"fixed"`
			Totals struct {
				Scanned int `json:"scanned"`
				Fixable int `json:"fixable"`
				Removed int `json:"removed"`
				Failed  int `json:"failed"`
				Kept    int `json:"kept"`
			} `json:"totals"`
		} `json:"orphans"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, stdout)
	}
	if report.Applied {
		t.Error("--fix applied worktree cleanup")
	}
	if !report.Orphans.Fixed || report.Orphans.Totals.Scanned != 1 ||
		report.Orphans.Totals.Fixable != 1 || report.Orphans.Totals.Removed != 1 ||
		report.Orphans.Totals.Failed != 0 || report.Orphans.Totals.Kept != 0 {
		t.Errorf("orphan totals = %#v", report.Orphans)
	}
	if strings.Contains(stderr, "[y/N]") {
		t.Errorf("--yes printed a prompt: %q", stderr)
	}
	if stateEntryExists(t, stale) {
		t.Error("stale state entry was not pruned")
	}
	if !stateEntryExists(t, current) {
		t.Error("current worktree state entry was pruned")
	}
	if strings.Contains(stderr, "LOSS") || strings.Contains(stderr, "removed") && strings.Contains(stderr, "worktree") {
		t.Errorf("--fix performed worktree cleanup: %q", stderr)
	}

	calls, err := os.ReadFile(rexLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(calls)); got != "session-ls" {
		t.Errorf("Rex calls = %q, want one session listing and nothing else", got)
	}
}

func TestCleanupFixInteractivePrompt(t *testing.T) {
	_, _, stale, _ := orphanCLIFixture(t)
	setCleanupTerminal(t, true)

	stdout, stderr, err := runCLIWithInput(t, "n\n", "cleanup", "--fix")
	if err != nil {
		t.Fatalf("declined fix: %v\nstderr: %s", err, stderr)
	}
	for _, want := range []string{
		"Stale state.json entries to prune:",
		stale,
		"Remove 1 stale state.json entry? [y/N]",
		"Nothing was changed.",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q: %q", want, stderr)
		}
	}
	if !strings.Contains(stdout, "Orphans:") || !strings.Contains(stdout, "Rerun with --fix") {
		t.Errorf("declined report = %q", stdout)
	}
	if !stateEntryExists(t, stale) {
		t.Error("declined fix pruned the state entry")
	}

	stdout, stderr, err = runCLIWithInput(t, "yes\n", "cleanup", "--fix")
	if err != nil {
		t.Fatalf("accepted fix: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "Remove 1 stale state.json entry? [y/N]") {
		t.Errorf("accept prompt = %q", stderr)
	}
	if !strings.Contains(stdout, "State fix: 1 removed, 0 failed, 0 retained.") {
		t.Errorf("accepted report = %q", stdout)
	}
	if stateEntryExists(t, stale) {
		t.Error("accepted fix did not prune the state entry")
	}
}

func TestCleanupFixFlagValidation(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"cleanup", "--fix", "--apply"}, "run --apply separately"},
		{[]string{"cleanup", "--fix", "--force"}, "does not apply"},
		{[]string{"cleanup", "--yes"}, "requires --fix"},
	}
	for _, test := range tests {
		_, _, err := runCLI(t, test.args...)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%v err = %v, want %q", test.args, err, test.want)
		}
	}
}

// An authorized --fix with nothing to prune is still a fix pass, not a
// preview: it reports fixed:true.
func TestCleanupFixWithNothingToPruneMarksFixed(t *testing.T) {
	repo := cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	cleanupFakeRex(t, `{"sessions":[]}`)
	setCleanupTerminal(t, false)

	var preview struct {
		Orphans struct {
			Fixed bool `json:"fixed"`
		} `json:"orphans"`
	}
	stdout, _, err := runCLI(t, "cleanup", "--json")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if err := json.Unmarshal([]byte(stdout), &preview); err != nil {
		t.Fatalf("preview JSON: %v", err)
	}
	if preview.Orphans.Fixed {
		t.Error("preview marked the orphan section fixed")
	}

	var report struct {
		Orphans struct {
			Fixed    bool `json:"fixed"`
			Complete bool `json:"complete"`
			Totals   struct {
				Scanned int `json:"scanned"`
				Fixable int `json:"fixable"`
				Removed int `json:"removed"`
				Failed  int `json:"failed"`
				Kept    int `json:"kept"`
			} `json:"totals"`
		} `json:"orphans"`
	}
	stdout, stderr, err := runCLI(t, "cleanup", "--json", "--fix", "--yes")
	if err != nil {
		t.Fatalf("cleanup --json --fix --yes: %v\nstderr: %s", err, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("fix JSON: %v", err)
	}
	if !report.Orphans.Fixed || !report.Orphans.Complete || report.Orphans.Totals.Scanned != 0 ||
		report.Orphans.Totals.Fixable != 0 || report.Orphans.Totals.Removed != 0 ||
		report.Orphans.Totals.Failed != 0 || report.Orphans.Totals.Kept != 0 {
		t.Errorf("zero-candidate orphans = %#v", report.Orphans)
	}
	if strings.Contains(stderr, "[y/N]") {
		t.Errorf("zero-candidate fix prompted: %q", stderr)
	}
}

// A zero-candidate fix still marks non-stale orphan items kept, so the report
// distinguishes "pruned nothing" from "never examined".
func TestCleanupFixZeroCandidatesKeepsNonStaleOrphans(t *testing.T) {
	repo := cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	cleanupFakeRex(t, `{"sessions":[{"session_id":"session:ghost","label":"repo/feat/ghost"}]}`)
	setCleanupTerminal(t, false)

	var report struct {
		Orphans struct {
			Fixed    bool `json:"fixed"`
			Complete bool `json:"complete"`
			Items    []struct {
				Kind   string `json:"kind"`
				Label  string `json:"label"`
				Result string `json:"result"`
			} `json:"items"`
			Totals struct {
				Scanned int `json:"scanned"`
				Fixable int `json:"fixable"`
				Removed int `json:"removed"`
				Failed  int `json:"failed"`
				Kept    int `json:"kept"`
			} `json:"totals"`
		} `json:"orphans"`
	}
	stdout, stderr, err := runCLI(t, "cleanup", "--json", "--fix", "--yes")
	if err != nil {
		t.Fatalf("cleanup --json --fix --yes: %v\nstderr: %s", err, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("fix JSON: %v\n%s", err, stdout)
	}
	if !report.Orphans.Fixed || !report.Orphans.Complete {
		t.Errorf("fixed=%v complete=%v", report.Orphans.Fixed, report.Orphans.Complete)
	}
	if report.Orphans.Totals.Scanned != 1 || report.Orphans.Totals.Fixable != 0 ||
		report.Orphans.Totals.Removed != 0 || report.Orphans.Totals.Failed != 0 ||
		report.Orphans.Totals.Kept != 1 {
		t.Errorf("totals = %#v", report.Orphans.Totals)
	}
	if len(report.Orphans.Items) != 1 || report.Orphans.Items[0].Kind != "unmatched-session" ||
		report.Orphans.Items[0].Label != "repo/feat/ghost" || report.Orphans.Items[0].Result != "kept" {
		t.Errorf("items = %#v", report.Orphans.Items)
	}
	if strings.Contains(stderr, "[y/N]") {
		t.Errorf("zero-candidate fix prompted: %q", stderr)
	}
}

// A state.json read failure is a warning in a preview and an error only for an
// explicit fix. JSON stays pure in both cases.
func TestCleanupStateErrorIsPreviewWarningAndFixError(t *testing.T) {
	repo := cleanupRepo(t)
	testConfig(t, repo, "", "")
	cleanupFakeRex(t, `{"sessions":[]}`)
	statePath, err := config.StatePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLI(t, "cleanup", "--json")
	if err != nil {
		t.Fatalf("preview with invalid state should warn, not fail: %v", err)
	}
	var preview struct {
		Errors   []string `json:"errors"`
		Warnings []string `json:"warnings"`
		Orphans  struct {
			Fixed    bool `json:"fixed"`
			Complete bool `json:"complete"`
			Totals   struct {
				Fixable int `json:"fixable"`
			} `json:"totals"`
		} `json:"orphans"`
	}
	if err := json.Unmarshal([]byte(stdout), &preview); err != nil {
		t.Fatalf("preview JSON: %v\n%s", err, stdout)
	}
	if len(preview.Errors) != 0 {
		t.Errorf("preview errors = %#v", preview.Errors)
	}
	if !strings.Contains(strings.Join(preview.Warnings, " "), "state.json") {
		t.Errorf("preview warnings = %#v", preview.Warnings)
	}
	if preview.Orphans.Fixed || preview.Orphans.Complete || preview.Orphans.Totals.Fixable != 0 {
		t.Errorf("preview orphans = %#v", preview.Orphans)
	}
	if !strings.Contains(stderr, "state.json") {
		t.Errorf("preview stderr = %q", stderr)
	}

	stdout, _, err = runCLI(t, "cleanup", "--json", "--fix", "--yes")
	if err == nil {
		t.Fatal("fix with invalid state should fail")
	}
	var fixed struct {
		Errors  []string `json:"errors"`
		Orphans struct {
			Fixed    bool `json:"fixed"`
			Complete bool `json:"complete"`
		} `json:"orphans"`
	}
	if err := json.Unmarshal([]byte(stdout), &fixed); err != nil {
		t.Fatalf("fix stdout is not pure JSON: %v\n%s", err, stdout)
	}
	if !strings.Contains(strings.Join(fixed.Errors, " "), "state.json") {
		t.Errorf("fix errors = %#v", fixed.Errors)
	}
	if fixed.Orphans.Fixed || fixed.Orphans.Complete {
		t.Errorf("failed fix reported fixed=%v complete=%v", fixed.Orphans.Fixed, fixed.Orphans.Complete)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("fix changed invalid state.json:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCleanupStdinTerminalPredicate(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if cleanupFDIsTerminal(null.Fd()) {
		t.Errorf("%s was detected as a terminal", os.DevNull)
	}

	if zero, err := os.Open("/dev/zero"); err == nil {
		defer zero.Close()
		if cleanupFDIsTerminal(zero.Fd()) {
			t.Error("/dev/zero was detected as a terminal")
		}
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if cleanupFDIsTerminal(reader.Fd()) {
		t.Error("a pipe was detected as a terminal")
	}

	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("y\n"))
	if cleanupStdinIsTerminal(cmd) {
		t.Error("a bytes reader was detected as a terminal")
	}
}

func TestCleanupOrphanOutputSanitizesControls(t *testing.T) {
	repo := cleanupRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	testConfig(t, repo, root, "")
	state := config.NewState()
	badPath := filepath.Join(t.TempDir(), "bad\x1b[31mpath")
	state.SetStatus(badPath, config.StatusReady, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
	cleanupFakeRex(t, `{"sessions":[{"session_id":"session:bad","label":"repo/feat/\u001b[31mghost"}]}`)

	stdout, stderr, err := runCLI(t, "cleanup")
	if err != nil {
		t.Fatalf("cleanup: %v\nstderr: %s", err, stderr)
	}
	if strings.Contains(stdout, "\x1b") || strings.Contains(stderr, "\x1b") {
		t.Errorf("preview let an escape sequence through: stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, "stale-state") || !strings.Contains(stdout, "unmatched-session") {
		t.Errorf("preview did not report both categories: %q", stdout)
	}

	setCleanupTerminal(t, true)
	stdout, stderr, err = runCLIWithInput(t, "n\n", "cleanup", "--fix")
	if err != nil {
		t.Fatalf("prompted cleanup: %v\nstderr: %s", err, stderr)
	}
	if strings.Contains(stdout, "\x1b") || strings.Contains(stderr, "\x1b") {
		t.Errorf("prompt let an escape sequence through: stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "Stale state.json entries to prune:") {
		t.Errorf("prompt did not list the candidate: %q", stderr)
	}
}
