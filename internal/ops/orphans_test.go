package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
)

// orphanScenario has one representative of every orphan category plus four
// state entries that must be preserved by --fix.
type orphanScenario struct {
	*cleanupFixture
	currentPath    string
	stalePath      string
	plainPath      string
	gonePath       string
	otherRepoPath  string
	nestedPath     string
	ambiguousLabel string
	rexLog         string
}

func newOrphanScenario(t *testing.T) *orphanScenario {
	t.Helper()
	f := newCleanupFixture(t)
	scenario := &orphanScenario{cleanupFixture: f}

	state := config.NewState()

	current := f.worktree("feat/current")
	scenario.currentPath = canonical(t, current)
	state.SetStatus(scenario.currentPath, config.StatusInProgress, time.Now())

	// A state entry whose path was never created.
	scenario.stalePath = filepath.Join(f.root, "feat", "missing")
	state.SetStatus(scenario.stalePath, config.StatusDone, time.Now())

	// A state entry whose path exists but is not a Git worktree.
	scenario.plainPath = filepath.Join(f.root, "plain")
	if err := os.MkdirAll(scenario.plainPath, 0o755); err != nil {
		t.Fatal(err)
	}
	scenario.plainPath = canonical(t, scenario.plainPath)
	state.SetStatus(scenario.plainPath, config.StatusReady, time.Now())

	// A prunable worktree is still a Git worktree registration; cleanup
	// --apply owns it, so --fix must retain the state entry.
	gone := f.worktree("feat/gone")
	scenario.gonePath = canonical(t, gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	state.SetStatus(scenario.gonePath, config.StatusInProgress, time.Now())

	// A worktree of an unregistered repository is still a current Git
	// worktree, so its state entry is not stale.
	otherRepo := testRepo(t)
	scenario.otherRepoPath = canonical(t, otherRepo)
	state.SetStatus(scenario.otherRepoPath, config.StatusBlocked, time.Now())

	// A worktree inside the project checkout but outside its worktree_root.
	nested := filepath.Join(f.repo, ".worktrees", "manual")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.repo, "worktree", "add", "-q", "-b", "feat/manual", nested, "main")
	scenario.nestedPath = canonical(t, nested)

	// Two detached worktrees at the same commit share one session label.
	first := filepath.Join(f.root, "detached-a")
	second := filepath.Join(f.root, "detached-b")
	f.detach(first, "main")
	f.detach(second, "main")
	scenario.ambiguousLabel = "repo/" + headSHA(t, f.repo)[:7]

	if err := state.Save(); err != nil {
		t.Fatal(err)
	}

	scenario.rexLog = writeCleanupRex(t, `{"sessions":[
	  {"session_id":"session:matched","label":"repo/feat/current"},
	  {"session_id":"session:ghost","label":"repo/feat/ghost"},
	  {"session_id":"session:dup-a","label":"repo/feat/current"},
	  {"session_id":"session:dup-b","label":"repo/feat/current"},
	  {"session_id":"session:amb","label":"`+scenario.ambiguousLabel+`"},
	  {"session_id":"session:manual","label":"repo/feat/manual"}
	]}`)
	return scenario
}

func orphansOfKind(report CleanupReport, kind CleanupOrphanKind) []CleanupOrphan {
	var out []CleanupOrphan
	for _, item := range report.Orphans.Items {
		if item.Kind == kind {
			out = append(out, item)
		}
	}
	return out
}

func orphanByPath(t *testing.T, report CleanupReport, kind CleanupOrphanKind, path string) (CleanupOrphan, bool) {
	t.Helper()
	for _, item := range orphansOfKind(report, kind) {
		if cleanupComparablePath(item.Path) == cleanupComparablePath(path) {
			return item, true
		}
	}
	return CleanupOrphan{}, false
}

func orphanByLabel(t *testing.T, report CleanupReport, kind CleanupOrphanKind, label string) CleanupOrphan {
	t.Helper()
	for _, item := range orphansOfKind(report, kind) {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("no %s orphan for label %q", kind, label)
	return CleanupOrphan{}
}

func stateBytes(t *testing.T) string {
	t.Helper()
	path, err := config.StatePath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCleanupOrphanReportNamesEveryCategory(t *testing.T) {
	scenario := newOrphanScenario(t)
	before := stateBytes(t)

	report, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{})
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	if after := stateBytes(t); after != before {
		t.Errorf("preview changed state.json:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if report.Orphans.Fixed {
		t.Error("preview marked the orphan section fixed")
	}
	if !report.Orphans.Complete {
		t.Error("preview did not complete the stale-state proof")
	}

	wantCounts := map[string]int{
		string(CleanupOrphanUnmatchedSession):     1,
		string(CleanupOrphanStaleState):           2,
		string(CleanupOrphanUnregisteredWorktree): 1,
		string(CleanupOrphanSessionLabelConflict): 2,
	}
	for kind, want := range wantCounts {
		if got := report.Orphans.Totals.ByKind[kind]; got != want {
			t.Errorf("%s count = %d, want %d", kind, got, want)
		}
	}
	if report.Orphans.Totals.Scanned != 6 {
		t.Errorf("orphans scanned = %d, want 6", report.Orphans.Totals.Scanned)
	}
	if report.Orphans.Totals.Fixable != 1 {
		t.Errorf("orphans fixable = %d, want 1", report.Orphans.Totals.Fixable)
	}

	ghost := orphanByLabel(t, report, CleanupOrphanUnmatchedSession, "repo/feat/ghost")
	if ghost.Project != "repo" || len(ghost.SessionIDs) != 1 || ghost.SessionIDs[0] != "session:ghost" {
		t.Errorf("unmatched ghost = %#v", ghost)
	}
	for _, item := range orphansOfKind(report, CleanupOrphanUnmatchedSession) {
		if item.Label == "repo/feat/manual" {
			t.Errorf("session for an unregistered worktree was reported unmatched: %#v", item)
		}
	}

	stale, ok := orphanByPath(t, report, CleanupOrphanStaleState, scenario.stalePath)
	if !ok || !stale.Fixable || stale.Project != "repo" {
		t.Errorf("stale missing item = %#v, found %v", stale, ok)
	}
	if stale.SessionIDs == nil || len(stale.SessionIDs) != 0 {
		t.Errorf("stale item session_ids = %#v, want an empty array", stale.SessionIDs)
	}

	plain, ok := orphanByPath(t, report, CleanupOrphanStaleState, scenario.plainPath)
	if !ok || plain.Fixable || !strings.Contains(plain.Detail, "still exists") {
		t.Errorf("existing non-worktree stale item = %#v, found %v", plain, ok)
	}

	for _, path := range []string{scenario.currentPath, scenario.gonePath, scenario.otherRepoPath} {
		if item, ok := orphanByPath(t, report, CleanupOrphanStaleState, path); ok {
			t.Errorf("state entry for current worktree %s was reported stale: %#v", path, item)
		}
	}

	nested, ok := orphanByPath(t, report, CleanupOrphanUnregisteredWorktree, scenario.nestedPath)
	if !ok || nested.Project != "repo" || !strings.Contains(nested.Detail, "outside its configured worktree_root") {
		t.Errorf("unregistered worktree = %#v, found %v", nested, ok)
	}

	duplicate := orphanByLabel(t, report, CleanupOrphanSessionLabelConflict, "repo/feat/current")
	if len(duplicate.SessionIDs) != 3 || cleanupComparablePath(duplicate.Path) != cleanupComparablePath(scenario.currentPath) {
		t.Errorf("duplicate label item = %#v", duplicate)
	}
	for _, id := range []string{"session:matched", "session:dup-a", "session:dup-b"} {
		if !strings.Contains(duplicate.Detail, id) {
			t.Errorf("duplicate detail does not name %s: %q", id, duplicate.Detail)
		}
	}

	ambiguous := orphanByLabel(t, report, CleanupOrphanSessionLabelConflict, scenario.ambiguousLabel)
	if ambiguous.Path != "" || len(ambiguous.SessionIDs) != 1 || !strings.Contains(ambiguous.Detail, "matches 2 worktrees") {
		t.Errorf("ambiguous label item = %#v", ambiguous)
	}
}

func TestCleanupFixPrunesOnlyProvablyStaleState(t *testing.T) {
	scenario := newOrphanScenario(t)
	plan, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	beforeState := stateBytes(t)
	beforeRefs := gitOutput(t, scenario.repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")
	beforeWorktrees := gitOutput(t, scenario.repo, "worktree", "list", "--porcelain")

	report, err := ApplyCleanupFix(context.Background(), scenario.cfg, plan)
	if err != nil {
		t.Fatalf("ApplyCleanupFix: %v", err)
	}
	if !report.Orphans.Fixed {
		t.Error("fix pass did not mark the orphan section fixed")
	}
	if !report.Orphans.Complete {
		t.Error("fix pass did not report a complete stale-state proof")
	}
	if !reflect.DeepEqual(report.Totals, plan.Totals) {
		t.Errorf("orphan fix changed worktree totals: %#v != %#v", report.Totals, plan.Totals)
	}
	if report.Orphans.Totals.Removed != 1 || report.Orphans.Totals.Failed != 0 || report.Orphans.Totals.Kept != 5 {
		t.Errorf("orphan fix totals = %#v", report.Orphans.Totals)
	}

	stale, ok := orphanByPath(t, report, CleanupOrphanStaleState, scenario.stalePath)
	if !ok || stale.Result != CleanupResultRemoved || stale.Error != "" {
		t.Errorf("stale item result = %#v, found %v", stale, ok)
	}
	plain, ok := orphanByPath(t, report, CleanupOrphanStaleState, scenario.plainPath)
	if !ok || plain.Result != CleanupResultKept {
		t.Errorf("plain state item result = %#v, found %v", plain, ok)
	}
	ghost := orphanByLabel(t, report, CleanupOrphanUnmatchedSession, "repo/feat/ghost")
	if ghost.Result != CleanupResultKept {
		t.Errorf("unmatched session was not retained: %#v", ghost)
	}
	nested, ok := orphanByPath(t, report, CleanupOrphanUnregisteredWorktree, scenario.nestedPath)
	if !ok || nested.Result != CleanupResultKept {
		t.Errorf("unregistered worktree was not retained: %#v", nested)
	}

	loaded, err := config.LoadState(scenario.cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Workstreams[scenario.stalePath]; ok {
		t.Errorf("stale state entry %s was not pruned", scenario.stalePath)
	}
	for _, path := range []string{scenario.currentPath, scenario.plainPath, scenario.gonePath, scenario.otherRepoPath} {
		if _, ok := loaded.Workstreams[path]; !ok {
			t.Errorf("state entry for %s was pruned but is not provably stale", path)
		}
	}

	afterState := stateBytes(t)
	if afterState == beforeState {
		t.Error("fix did not change state.json")
	}
	if strings.Contains(afterState, scenario.stalePath) {
		t.Errorf("pruned path remains in state.json: %s", afterState)
	}
	for _, path := range []string{scenario.currentPath, scenario.plainPath, scenario.gonePath, scenario.otherRepoPath} {
		if !strings.Contains(afterState, path) {
			t.Errorf("state entry for retained path %s disappeared: %s", path, afterState)
		}
	}

	if after := gitOutput(t, scenario.repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"); after != beforeRefs {
		t.Errorf("fix changed refs:\nbefore:\n%s\nafter:\n%s", beforeRefs, after)
	}
	if after := gitOutput(t, scenario.repo, "worktree", "list", "--porcelain"); after != beforeWorktrees {
		t.Errorf("fix changed worktrees:\nbefore:\n%s\nafter:\n%s", beforeWorktrees, after)
	}

	calls, err := os.ReadFile(scenario.rexLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "kill") || strings.Contains(string(calls), "unexpected") {
		t.Errorf("fix touched Rex beyond listing sessions: %q", calls)
	}
}

func TestCleanupFixKeepsStateWhenPathReappears(t *testing.T) {
	scenario := newOrphanScenario(t)
	plan, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scenario.stalePath, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := ApplyCleanupFix(context.Background(), scenario.cfg, plan)
	if err != nil {
		t.Fatalf("ApplyCleanupFix: %v", err)
	}
	stale, ok := orphanByPath(t, report, CleanupOrphanStaleState, scenario.stalePath)
	if !ok || stale.Result != CleanupResultFailed || !strings.Contains(stale.Error, "exists again") {
		t.Errorf("reappeared path item = %#v, found %v", stale, ok)
	}
	if report.Orphans.Totals.Removed != 0 || report.Orphans.Totals.Failed != 1 {
		t.Errorf("totals = %#v", report.Orphans.Totals)
	}
	loaded, err := config.LoadState(scenario.cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Workstreams[scenario.stalePath]; !ok {
		t.Error("state entry was pruned after its path reappeared")
	}
	if err := report.Err(); err == nil || !strings.Contains(err.Error(), "orphan fix") {
		t.Errorf("Err() = %v, want an orphan fix failure", err)
	}
}

func TestCleanupFixRefusesWhenProjectBecomesUnreadable(t *testing.T) {
	scenario := newOrphanScenario(t)
	plan, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := stateBytes(t)

	cfg := scenario.cfg
	cfg.Projects = append(append([]config.Project{}, cfg.Projects...), config.Project{
		Name: "missing",
		Path: filepath.Join(t.TempDir(), "missing"),
	})
	report, err := ApplyCleanupFix(context.Background(), cfg, plan)
	if err == nil || !strings.Contains(err.Error(), "cannot re-list project missing") {
		t.Fatalf("ApplyCleanupFix err = %v", err)
	}
	if report.Orphans.Fixed {
		t.Error("failed re-list marked the orphan section fixed")
	}
	if after := stateBytes(t); after != before {
		t.Errorf("fix refused but changed state.json:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCleanupStaleStateIsSkippedWhenProjectUnreadable(t *testing.T) {
	scenario := newOrphanScenario(t)
	cfg := scenario.cfg
	cfg.Projects = append(append([]config.Project{}, cfg.Projects...), config.Project{
		Name: "missing",
		Path: filepath.Join(t.TempDir(), "missing"),
	})

	report, err := PlanCleanup(context.Background(), cfg, CleanupOptions{})
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	if len(report.Errors) == 0 {
		t.Error("unreadable project was not reported")
	}
	if count := report.Orphans.Totals.ByKind[string(CleanupOrphanStaleState)]; count != 0 {
		t.Errorf("stale state items = %d, want 0 while a project is unreadable", count)
	}
	if report.Orphans.Complete {
		t.Error("incomplete stale-state proof was reported complete")
	}
	foundWarning := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "stale state.json entries were not checked") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("warnings = %#v, want an explanation for the skipped state scan", report.Warnings)
	}
}

func TestCleanupOrphanProjectFilter(t *testing.T) {
	scenario := newOrphanScenario(t)

	other := testRepo(t)
	gitRun(t, other, "branch", "-M", "main")
	otherRoot := filepath.Join(t.TempDir(), "other-worktrees")
	if _, _, err := scenario.cfg.RegisterProject(canonical(t, other), "other", "", otherRoot); err != nil {
		t.Fatal(err)
	}
	if err := scenario.cfg.Save(); err != nil {
		t.Fatal(err)
	}
	otherStale := filepath.Join(otherRoot, "feat", "missing")
	state, err := config.LoadState(scenario.cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	state.SetStatus(otherStale, config.StatusReady, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
	writeCleanupRex(t, `{"sessions":[
	  {"session_id":"session:repo-ghost","label":"repo/feat/ghost"},
	  {"session_id":"session:other-ghost","label":"other/ghost"}
	]}`)

	report, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{Project: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range report.Orphans.Items {
		if item.Project == "other" || item.Label == "other/ghost" || item.Path == otherStale {
			t.Errorf("filtered report contains an other-project orphan: %#v", item)
		}
	}
	if _, ok := orphanByPath(t, report, CleanupOrphanStaleState, otherStale); ok {
		t.Error("filtered report contains the other project's stale state")
	}

	fixed, err := ApplyCleanupFix(context.Background(), scenario.cfg, report)
	if err != nil {
		t.Fatalf("ApplyCleanupFix: %v", err)
	}
	if fixed.Orphans.Totals.Removed != 1 {
		t.Errorf("filtered fix removed %d entries, want 1", fixed.Orphans.Totals.Removed)
	}
	loaded, err := config.LoadState(scenario.cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Workstreams[scenario.stalePath]; ok {
		t.Error("repo stale entry was not pruned")
	}
	if _, ok := loaded.Workstreams[otherStale]; !ok {
		t.Error("other project's state entry was pruned by a filtered run")
	}
}

func TestCleanupReportJSONIncludesOrphans(t *testing.T) {
	scenario := newOrphanScenario(t)
	first, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := json.MarshalIndent(first, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(second, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("two orphan reports differ:\n%s\n---\n%s", a, b)
	}

	var document struct {
		SchemaVersion int                          `json:"schema_version"`
		Projects      []map[string]json.RawMessage `json:"projects"`
		Orphans       struct {
			Fixed    bool                         `json:"fixed"`
			Complete bool                         `json:"complete"`
			Items    []map[string]json.RawMessage `json:"items"`
			Totals   map[string]json.RawMessage   `json:"totals"`
		} `json:"orphans"`
		Totals map[string]json.RawMessage `json:"totals"`
	}
	if err := json.Unmarshal(a, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 {
		t.Errorf("schema_version = %d", document.SchemaVersion)
	}
	if !document.Orphans.Complete {
		t.Error("orphan report did not include a complete stale-state proof")
	}
	if len(document.Projects) != 1 {
		t.Errorf("projects = %d", len(document.Projects))
	}
	for _, key := range []string{`"classification"`, `"branch_state"`, `"loss"`} {
		if !strings.Contains(string(a), key) {
			t.Errorf("cleanup report lost existing key %s", key)
		}
	}
	if !strings.Contains(string(document.Totals["by_classification"]), `"prunable"`) {
		t.Errorf("cleanup totals lost by_classification: %s", document.Totals["by_classification"])
	}

	itemKeys := []string{
		"kind", "project", "label", "path", "session_ids",
		"detail", "fixable", "result", "error",
	}
	kinds := make(map[string]bool)
	sawFixable, sawNotFixable, sawArray := false, false, false
	for _, item := range document.Orphans.Items {
		for _, key := range itemKeys {
			if _, ok := item[key]; !ok {
				t.Errorf("orphan item is missing JSON key %q: %s", key, string(a))
			}
		}
		var kind string
		if err := json.Unmarshal(item["kind"], &kind); err != nil {
			t.Fatal(err)
		}
		kinds[kind] = true
		var fixable bool
		if err := json.Unmarshal(item["fixable"], &fixable); err != nil {
			t.Fatal(err)
		}
		if fixable {
			sawFixable = true
		} else {
			sawNotFixable = true
		}
		var ids []string
		if err := json.Unmarshal(item["session_ids"], &ids); err != nil {
			t.Fatalf("session_ids is not an array: %s", item["session_ids"])
		}
		if ids != nil {
			sawArray = true
		}
	}
	for _, kind := range []string{
		string(CleanupOrphanUnmatchedSession),
		string(CleanupOrphanStaleState),
		string(CleanupOrphanUnregisteredWorktree),
		string(CleanupOrphanSessionLabelConflict),
	} {
		if !kinds[kind] {
			t.Errorf("report is missing orphan kind %q", kind)
		}
	}
	if !sawFixable || !sawNotFixable {
		t.Errorf("orphan fixable coverage: fixable=%v notFixable=%v", sawFixable, sawNotFixable)
	}
	if !sawArray {
		t.Error("orphan session_ids is not always an array")
	}

	for _, key := range []string{"scanned", "fixable", "removed", "failed", "kept", "by_kind"} {
		if _, ok := document.Orphans.Totals[key]; !ok {
			t.Errorf("orphan totals is missing %q", key)
		}
	}
	byKind := string(document.Orphans.Totals["by_kind"])
	for _, kind := range []string{
		string(CleanupOrphanUnmatchedSession),
		string(CleanupOrphanStaleState),
		string(CleanupOrphanUnregisteredWorktree),
		string(CleanupOrphanSessionLabelConflict),
	} {
		if !strings.Contains(byKind, `"`+kind+`"`) {
			t.Errorf("orphan by_kind is missing %q: %s", kind, byKind)
		}
	}
}

// A single registered project still needs explicit narrowing: comparing the
// selected scan count with the configured project count made --project a no-op.
func TestCleanupOrphanProjectFilterWithSingleRegisteredProject(t *testing.T) {
	f := newCleanupFixture(t)
	state := config.NewState()
	repoStale := filepath.Join(f.root, "feat", "missing")
	outsideStale := filepath.Join(t.TempDir(), "outside", "missing")
	state.SetStatus(repoStale, config.StatusReady, time.Now())
	state.SetStatus(outsideStale, config.StatusReady, time.Now())
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
	writeCleanupRex(t, `{"sessions":[
	  {"session_id":"session:repo-ghost","label":"repo/feat/ghost"},
	  {"session_id":"session:other-ghost","label":"otherproject/ghost"}
	]}`)

	report, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Project: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := orphanByPath(t, report, CleanupOrphanStaleState, repoStale); !ok {
		t.Error("filtered report is missing the repo stale state")
	}
	if _, ok := orphanByPath(t, report, CleanupOrphanStaleState, outsideStale); ok {
		t.Error("filtered report contains a stale entry outside the selected project")
	}
	for _, item := range orphansOfKind(report, CleanupOrphanUnmatchedSession) {
		if item.Label == "otherproject/ghost" {
			t.Errorf("filtered report contains an unmanaged label: %#v", item)
		}
	}

	fixed, err := ApplyCleanupFix(context.Background(), f.cfg, report)
	if err != nil {
		t.Fatalf("ApplyCleanupFix: %v", err)
	}
	if fixed.Orphans.Totals.Removed != 1 {
		t.Errorf("filtered fix removed %d entries, want 1", fixed.Orphans.Totals.Removed)
	}
	loaded, err := config.LoadState(f.cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Workstreams[repoStale]; ok {
		t.Error("repo stale entry was not pruned")
	}
	if _, ok := loaded.Workstreams[outsideStale]; !ok {
		t.Error("out-of-scope state entry was pruned by a filtered run")
	}
}

// A project whose default branch cannot be resolved is still readable enough
// for session matching and unregistered-worktree reports.
func TestCleanupOrphanScanKeepsWorktreesWhenClassificationFails(t *testing.T) {
	repo := testRepo(t)
	gitRun(t, repo, "branch", "-M", "trunk")
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	live := filepath.Join(root, "feat", "live")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/live", live, "trunk")

	nested := filepath.Join(repo, ".worktrees", "manual")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/manual", nested, "trunk")
	nestedPath := canonical(t, nested)

	writeCleanupRex(t, `{"sessions":[
	  {"session_id":"session:live","label":"repo/feat/live"},
	  {"session_id":"session:ghost","label":"repo/feat/ghost"}
	]}`)

	report, err := PlanCleanup(context.Background(), cfg, CleanupOptions{})
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	if len(report.Errors) == 0 {
		t.Error("expected the unresolvable default branch to be reported")
	}
	for _, item := range orphansOfKind(report, CleanupOrphanUnmatchedSession) {
		if item.Label == "repo/feat/live" {
			t.Errorf("session for a listed worktree was reported unmatched: %#v", item)
		}
	}
	ghost := orphanByLabel(t, report, CleanupOrphanUnmatchedSession, "repo/feat/ghost")
	if len(ghost.SessionIDs) != 1 || ghost.SessionIDs[0] != "session:ghost" {
		t.Errorf("unmatched ghost = %#v", ghost)
	}
	if _, ok := orphanByPath(t, report, CleanupOrphanUnregisteredWorktree, nestedPath); !ok {
		t.Error("nested worktree was hidden by the classification failure")
	}
}

func TestCleanupOrphanAmbiguousLabelWithoutSession(t *testing.T) {
	f := newCleanupFixture(t)
	f.detach(filepath.Join(f.root, "detached-a"), "main")
	f.detach(filepath.Join(f.root, "detached-b"), "main")
	label := "repo/" + headSHA(t, f.repo)[:7]
	writeCleanupRex(t, `{"sessions":[]}`)

	report, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conflicts := orphansOfKind(report, CleanupOrphanSessionLabelConflict)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %#v, want one ambiguous label", conflicts)
	}
	item := conflicts[0]
	if item.Label != label || item.SessionIDs == nil || len(item.SessionIDs) != 0 {
		t.Errorf("ambiguous label item = %#v", item)
	}
	if !strings.Contains(item.Detail, "no live Rex session") || !strings.Contains(item.Detail, "matches 2 worktrees") {
		t.Errorf("ambiguous label detail = %q", item.Detail)
	}
	if len(orphansOfKind(report, CleanupOrphanUnmatchedSession)) != 0 {
		t.Error("an ambiguous label with no sessions was also reported unmatched")
	}
}

// Preview fixability is computed over every project ApplyCleanupFix re-lists,
// so an unrelated unreadable project does not advertise a fix it will refuse.
func TestCleanupOrphanFilterWithUnrelatedUnreadableProject(t *testing.T) {
	scenario := newOrphanScenario(t)
	cfg := scenario.cfg
	cfg.Projects = append(append([]config.Project{}, cfg.Projects...), config.Project{
		Name: "unreadable",
		Path: filepath.Join(t.TempDir(), "missing"),
	})

	report, err := PlanCleanup(context.Background(), cfg, CleanupOptions{Project: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Orphans.Totals.Fixable != 0 {
		t.Errorf("fixable = %d, want 0 while a registered project is unreadable", report.Orphans.Totals.Fixable)
	}
	if report.Orphans.Complete {
		t.Error("incomplete stale-state proof was reported complete")
	}
	if count := report.Orphans.Totals.ByKind[string(CleanupOrphanStaleState)]; count != 0 {
		t.Errorf("stale state items = %d, want 0", count)
	}
	foundWarning := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "stale state.json entries were not checked") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("warnings = %#v, want an explanation for the skipped state scan", report.Warnings)
	}
	// Session triage only needs the selected project, which is readable.
	if _, ok := orphanByLabelOK(report, CleanupOrphanUnmatchedSession, "repo/feat/ghost"); !ok {
		t.Error("unreadable unrelated project suppressed the selected project's session triage")
	}

	before := stateBytes(t)
	fixed, err := ApplyCleanupFix(context.Background(), cfg, report)
	if err != nil {
		t.Fatalf("zero-candidate fix: %v", err)
	}
	if !fixed.Orphans.Fixed || fixed.Orphans.Totals.Removed != 0 {
		t.Errorf("zero-candidate fix = %#v", fixed.Orphans)
	}
	if after := stateBytes(t); after != before {
		t.Errorf("zero-candidate fix changed state.json:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// One unreadable selected project must not disable session triage for healthy
// selected projects. Only labels belonging to the failed project are skipped.
func TestCleanupOrphanSessionTriageSurvivesUnreadableProject(t *testing.T) {
	f := newCleanupFixture(t)
	f.detach(filepath.Join(f.root, "detached-a"), "main")
	f.detach(filepath.Join(f.root, "detached-b"), "main")
	ambiguousLabel := "repo/" + headSHA(t, f.repo)[:7]

	badPath := filepath.Join(t.TempDir(), "missing")
	if _, _, err := f.cfg.RegisterProject(badPath, "bad", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.cfg.Save(); err != nil {
		t.Fatal(err)
	}
	writeCleanupRex(t, `{"sessions":[
	  {"session_id":"session:repo-ghost","label":"repo/feat/ghost"},
	  {"session_id":"session:bad-ghost","label":"bad/ghost"}
	]}`)

	report, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) == 0 {
		t.Error("unreadable project was not reported")
	}
	if report.Orphans.Complete {
		t.Error("incomplete stale-state proof was reported complete")
	}

	unmatched := orphansOfKind(report, CleanupOrphanUnmatchedSession)
	if len(unmatched) != 1 || unmatched[0].Label != "repo/feat/ghost" {
		t.Errorf("unmatched = %#v, want only the healthy project's session", unmatched)
	}
	if _, ok := orphanByLabelOK(report, CleanupOrphanUnmatchedSession, "bad/ghost"); ok {
		t.Error("session for the unreadable project was reported unmatched")
	}
	conflicts := orphansOfKind(report, CleanupOrphanSessionLabelConflict)
	if len(conflicts) != 1 || conflicts[0].Label != ambiguousLabel {
		t.Errorf("conflicts = %#v, want the healthy project's detached-label conflict", conflicts)
	}
	foundWarning := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "Rex sessions for project(s) bad") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("warnings = %#v, want the skipped project named", report.Warnings)
	}
}

func orphanByLabelOK(report CleanupReport, kind CleanupOrphanKind, label string) (CleanupOrphan, bool) {
	for _, item := range orphansOfKind(report, kind) {
		if item.Label == label {
			return item, true
		}
	}
	return CleanupOrphan{}, false
}

func TestCleanupOrphansSkipUnmanagedLabels(t *testing.T) {
	f := newCleanupFixture(t)
	writeCleanupRex(t, `{"sessions":[
	  {"session_id":"session:hand","label":"handmade"},
	  {"session_id":"session:other","label":"other/ghost"},
	  {"session_id":"session:repo","label":"repo/feat/ghost"}
	]}`)

	report, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	unmatched := orphansOfKind(report, CleanupOrphanUnmatchedSession)
	if len(unmatched) != 1 || unmatched[0].Label != "repo/feat/ghost" {
		t.Errorf("unmatched = %#v, want only the registered project's label", unmatched)
	}
}

func TestCleanupOrphansZeroProjectsSkipSessionTriage(t *testing.T) {
	t.Setenv(config.EnvDir, filepath.Join(t.TempDir(), "config"))
	writeCleanupRex(t, `{"sessions":[{"session_id":"session:hand","label":"handmade"}]}`)

	report, err := PlanCleanup(context.Background(), config.Default(), CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orphans.Items) != 0 {
		t.Errorf("zero-project report has orphans: %#v", report.Orphans.Items)
	}
	if report.Orphans.Complete {
		t.Error("zero-project report claimed a complete stale-state proof")
	}
	foundWarning := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "no projects are registered") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("warnings = %#v, want a zero-project explanation", report.Warnings)
	}
}

func TestCleanupOrphanUnregisteredWorktreeRequiresWorktreeRoot(t *testing.T) {
	repo := testRepo(t)
	gitRun(t, repo, "branch", "-M", "main")
	cfg := testConfig(t, repo, "")
	nested := filepath.Join(repo, ".worktrees", "manual")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/manual", nested, "main")
	writeCleanupRex(t, `{"sessions":[]}`)

	report, err := PlanCleanup(context.Background(), cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if items := orphansOfKind(report, CleanupOrphanUnregisteredWorktree); len(items) != 0 {
		t.Errorf("unregistered worktrees without a configured root = %#v", items)
	}
}

// cleanupComparablePath must resolve the deepest existing ancestor, not just
// the immediate parent, so a deleted nested path under a symlinked temp
// directory (/var -> /private/var on macOS) still matches its configured root.
func TestCleanupComparablePathResolvesMissingAncestors(t *testing.T) {
	if got := cleanupComparablePath(""); got != "" {
		t.Errorf("cleanupComparablePath(\"\") = %q", got)
	}

	base := t.TempDir()
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}

	got := cleanupComparablePath(filepath.Join(base, "missing", "deeper", "leaf"))
	want := filepath.Join(resolvedBase, "missing", "deeper", "leaf")
	if got != want {
		t.Errorf("missing chain = %q, want %q", got, want)
	}

	got = cleanupComparablePath(filepath.Join(base, "does-not-exist"))
	want = filepath.Join(resolvedBase, "does-not-exist")
	if got != want {
		t.Errorf("missing root = %q, want %q", got, want)
	}

	target := filepath.Join(base, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	got = cleanupComparablePath(filepath.Join(link, "missing", "deeper", "leaf"))
	want = filepath.Join(resolvedTarget, "missing", "deeper", "leaf")
	if got != want {
		t.Errorf("symlinked missing chain = %q, want %q", got, want)
	}
}
