package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
)

type cleanupFixture struct {
	t    *testing.T
	repo string
	root string
	cfg  config.Config
}

// cleanupScenario is the shared fixture for classification and apply tests.
// Every classification rule has one representative item.
type cleanupScenario struct {
	*cleanupFixture
	paths  map[string]string // fixture label -> canonical or Git-reported path
	rexLog string
}

func newCleanupFixture(t *testing.T) *cleanupFixture {
	t.Helper()
	repo := testRepo(t)
	gitRun(t, repo, "branch", "-M", "main")
	gitRun(t, repo, "config", "user.name", "Test")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	writeCleanupRex(t, `{"sessions":[]}`)
	return &cleanupFixture{t: t, repo: repo, root: root, cfg: cfg}
}

// writeCleanupRex installs a fake rex that logs every call and returns the
// given session list.
func writeCleanupRex(t *testing.T, sessions string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "rex.log")
	t.Setenv("FAKE_REX_LOG", logPath)
	writeFakeRex(t, `log="$FAKE_REX_LOG"
case "$*" in
  "session ls --json") printf 'session-ls\n' >> "$log"; printf '%s\n' '`+sessions+`' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)
	return logPath
}

func (f *cleanupFixture) worktree(branch string) string {
	f.t.Helper()
	path := filepath.Join(f.root, filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	gitRun(f.t, f.repo, "worktree", "add", "-q", "-b", branch, path)
	return path
}

func (f *cleanupFixture) detach(path, ref string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	gitRun(f.t, f.repo, "worktree", "add", "-q", "--detach", path, ref)
}

func (f *cleanupFixture) commit(worktree, file, content, message string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, file), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	gitRun(f.t, worktree, "add", file)
	gitRun(f.t, worktree, "commit", "-q", "-m", message)
}

func (f *cleanupFixture) merge(branch string) {
	f.t.Helper()
	gitRun(f.t, f.repo, "merge", "-q", "--ff-only", branch)
}

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD in %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

func newCleanupScenario(t *testing.T) *cleanupScenario {
	t.Helper()
	f := newCleanupFixture(t)
	scenario := &cleanupScenario{cleanupFixture: f, paths: make(map[string]string)}

	merged := f.worktree("feat/merged")
	f.commit(merged, "merged.txt", "merged\n", "merged work")
	f.merge("feat/merged")
	scenario.paths["merged"] = canonical(t, merged)

	patch := f.worktree("feat/patch")
	f.commit(patch, "patch.txt", "patch\n", "patch work")
	patchSHA := headSHA(t, patch)
	f.commit(f.repo, "main-before-patch.txt", "main\n", "advance main before cherry-pick")
	gitRun(t, f.repo, "cherry-pick", patchSHA)
	scenario.paths["patch"] = canonical(t, patch)

	unmerged := f.worktree("feat/unmerged")
	f.commit(unmerged, "unmerged.txt", "unmerged\n", "unmerged work")
	scenario.paths["unmerged"] = canonical(t, unmerged)

	dirty := f.worktree("feat/dirty")
	f.commit(dirty, "dirty.txt", "dirty\n", "dirty work")
	f.merge("feat/dirty")
	if err := os.WriteFile(filepath.Join(dirty, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	scenario.paths["dirty"] = canonical(t, dirty)

	locked := f.worktree("feat/locked")
	f.commit(locked, "locked.txt", "locked\n", "locked work")
	f.merge("feat/locked")
	gitRun(t, f.repo, "worktree", "lock", locked)
	scenario.paths["locked"] = canonical(t, locked)

	detached := filepath.Join(f.root, "detached")
	f.detach(detached, "main")
	scenario.paths["detached"] = canonical(t, detached)

	develop := f.worktree("develop")
	scenario.paths["develop"] = canonical(t, develop)

	active := f.worktree("feat/active")
	scenario.paths["active"] = canonical(t, active)

	gone := f.worktree("feat/gone")
	scenario.paths["gone"] = canonical(t, gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	gitRun(t, f.repo, "branch", "feat/branch-merged", "main")

	branchUnmerged := f.worktree("feat/branch-unmerged")
	f.commit(branchUnmerged, "branch-unmerged.txt", "x\n", "branch-unmerged work")
	gitRun(t, f.repo, "worktree", "remove", branchUnmerged)

	branchPatch := f.worktree("feat/branch-patch")
	f.commit(branchPatch, "branch-patch.txt", "x\n", "branch-patch work")
	branchPatchSHA := headSHA(t, branchPatch)
	f.commit(f.repo, "main-before-branch-patch.txt", "main\n", "advance main before branch cherry-pick")
	gitRun(t, f.repo, "cherry-pick", branchPatchSHA)
	gitRun(t, f.repo, "worktree", "remove", branchPatch)

	scenario.rexLog = writeCleanupRex(t, `{"sessions":[{"session_id":"session:active","label":"repo/feat/active"}]}`)
	scenario.paths["main"] = canonical(t, f.repo)
	return scenario
}

func cleanupItemByBranch(t *testing.T, report CleanupReport, branch string) CleanupItem {
	t.Helper()
	for _, project := range report.Projects {
		for _, item := range project.Items {
			if item.Branch == branch {
				return item
			}
		}
	}
	t.Fatalf("no cleanup item for branch %q", branch)
	return CleanupItem{}
}

func cleanupItemByPath(t *testing.T, report CleanupReport, path string) CleanupItem {
	t.Helper()
	for _, project := range report.Projects {
		for _, item := range project.Items {
			if item.Path == path {
				return item
			}
		}
	}
	t.Fatalf("no cleanup item for path %q", path)
	return CleanupItem{}
}

func TestCleanupClassifiesEveryRule(t *testing.T) {
	scenario := newCleanupScenario(t)
	report, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{})
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}

	want := map[string]CleanupClassification{
		"main":                 CleanupProtected,
		"feat/merged":          CleanupMerged,
		"feat/patch":           CleanupPatchEquivalent,
		"feat/unmerged":        CleanupUnmerged,
		"feat/dirty":           CleanupDirty,
		"feat/locked":          CleanupLocked,
		"develop":              CleanupProtected,
		"feat/active":          CleanupActive,
		"feat/gone":            CleanupPrunable,
		"feat/branch-merged":   CleanupMerged,
		"feat/branch-unmerged": CleanupUnmerged,
		"feat/branch-patch":    CleanupPatchEquivalent,
	}
	for branch, classification := range want {
		item := cleanupItemByBranch(t, report, branch)
		if item.Classification != classification {
			t.Errorf("%s: classification = %q, want %q (detail %q)", branch, item.Classification, classification, item.Detail)
		}
	}
	detached := cleanupItemByPath(t, report, scenario.paths["detached"])
	if !detached.Detached || detached.Classification != CleanupDetached {
		t.Errorf("detached item = %#v", detached)
	}

	safe := map[string]bool{
		"feat/merged":          true,
		"feat/patch":           true,
		"feat/gone":            true,
		"feat/branch-merged":   true,
		"feat/branch-patch":    true,
		"feat/unmerged":        false,
		"feat/dirty":           false,
		"feat/locked":          false,
		"feat/active":          false,
		"feat/branch-unmerged": false,
	}
	for branch, wantSafe := range safe {
		item := cleanupItemByBranch(t, report, branch)
		if item.Safe != wantSafe {
			t.Errorf("%s: safe = %v, want %v", branch, item.Safe, wantSafe)
		}
		if item.Removable != wantSafe {
			t.Errorf("%s: removable = %v, want %v without --force", branch, item.Removable, wantSafe)
		}
	}

	if item := cleanupItemByBranch(t, report, "feat/merged"); item.Action != CleanupActionRemoveWorktreeAndBranch {
		t.Errorf("merged action = %q", item.Action)
	}
	if item := cleanupItemByBranch(t, report, "feat/patch"); item.Action != CleanupActionRemoveWorktreeAndBranch {
		t.Errorf("patch action = %q", item.Action)
	}
	if item := cleanupItemByBranch(t, report, "feat/gone"); item.Action != CleanupActionPrune {
		t.Errorf("prunable action = %q", item.Action)
	}
	if item := cleanupItemByBranch(t, report, "feat/branch-merged"); item.Action != CleanupActionDeleteBranch {
		t.Errorf("branch merged action = %q", item.Action)
	}
	if item := cleanupItemByBranch(t, report, "feat/branch-patch"); item.Action != CleanupActionDeleteBranch {
		t.Errorf("branch patch action = %q", item.Action)
	}

	dirty := cleanupItemByBranch(t, report, "feat/dirty")
	if !dirty.Dirty || !dirty.RequiresForce || dirty.BranchState != CleanupBranchMerged {
		t.Errorf("dirty item = %#v", dirty)
	}
	if dirty.Loss == nil || len(dirty.Loss.DirtyFiles) != 1 || !strings.Contains(dirty.Loss.DirtyFiles[0], "untracked.txt") {
		t.Errorf("dirty loss = %#v", dirty.Loss)
	}
	unmerged := cleanupItemByBranch(t, report, "feat/unmerged")
	if !unmerged.RequiresForce || unmerged.Loss == nil || len(unmerged.Loss.Commits) != 1 {
		t.Errorf("unmerged item = %#v, loss = %#v", unmerged, unmerged.Loss)
	}
	if !strings.Contains(unmerged.Loss.Commits[0].Subject, "unmerged work") {
		t.Errorf("unmerged loss subject = %q", unmerged.Loss.Commits[0].Subject)
	}
	branchUnmerged := cleanupItemByBranch(t, report, "feat/branch-unmerged")
	if !branchUnmerged.RequiresForce || branchUnmerged.Loss == nil || len(branchUnmerged.Loss.Commits) != 1 {
		t.Errorf("branch unmerged item = %#v", branchUnmerged)
	}
	locked := cleanupItemByBranch(t, report, "feat/locked")
	if !locked.Locked || locked.RequiresForce || locked.Removable {
		t.Errorf("locked item = %#v", locked)
	}
	active := cleanupItemByBranch(t, report, "feat/active")
	if !active.ActiveSession || active.SessionID != "session:active" || active.Removable {
		t.Errorf("active item = %#v", active)
	}

	if report.Totals.Scanned != 13 {
		t.Errorf("scanned = %d, want 13", report.Totals.Scanned)
	}
	counts := report.Totals.ByClassification
	if counts["protected"] != 2 || counts["merged"] != 2 || counts["patch-equivalent"] != 2 ||
		counts["unmerged"] != 2 || counts["dirty"] != 1 || counts["locked"] != 1 ||
		counts["detached"] != 1 || counts["active"] != 1 || counts["prunable"] != 1 {
		t.Errorf("classification counts = %#v", counts)
	}
	if report.Totals.Safe != 5 || report.Totals.Removable != 5 || report.Totals.Kept != 8 {
		t.Errorf("totals = %#v", report.Totals)
	}

	// --force widens the removable set to dirty and unmerged only. Protected,
	// locked, detached, and active items stay impossible to remove.
	forced, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{Force: true})
	if err != nil {
		t.Fatalf("PlanCleanup force: %v", err)
	}
	for _, branch := range []string{"feat/dirty", "feat/unmerged", "feat/branch-unmerged"} {
		item := cleanupItemByBranch(t, forced, branch)
		if !item.Removable || item.Action == CleanupActionNone {
			t.Errorf("%s with --force = %#v", branch, item)
		}
	}
	for _, branch := range []string{"feat/locked", "feat/active", "develop", "main"} {
		item := cleanupItemByBranch(t, forced, branch)
		if item.Removable || item.Action != CleanupActionNone {
			t.Errorf("%s with --force was removable: %#v", branch, item)
		}
	}
	detachedForced := cleanupItemByPath(t, forced, scenario.paths["detached"])
	if detachedForced.Removable || detachedForced.Action != CleanupActionNone {
		t.Errorf("detached with --force was removable: %#v", detachedForced)
	}
	if forced.Totals.Removable != 8 {
		t.Errorf("forced removable = %d, want 8", forced.Totals.Removable)
	}
}

func TestCleanupPreviewMutatesNothing(t *testing.T) {
	scenario := newCleanupScenario(t)
	state := config.NewState()
	for _, path := range []string{
		scenario.paths["merged"], scenario.paths["patch"], scenario.paths["unmerged"],
		scenario.paths["dirty"], scenario.paths["locked"], scenario.paths["develop"],
		scenario.paths["active"],
	} {
		state.SetStatus(path, config.StatusInProgress, time.Now())
	}
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}

	before := snapshotCleanupRepo(t, scenario)

	if _, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{}); err != nil {
		t.Fatalf("preview: %v", err)
	}
	if _, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{Force: true}); err != nil {
		t.Fatalf("force preview: %v", err)
	}
	after := snapshotCleanupRepo(t, scenario)

	if before.refs != after.refs {
		t.Errorf("preview changed refs:\nbefore:\n%s\nafter:\n%s", before.refs, after.refs)
	}
	if before.worktrees != after.worktrees {
		t.Errorf("preview changed worktrees:\nbefore:\n%s\nafter:\n%s", before.worktrees, after.worktrees)
	}
	if before.state != after.state {
		t.Errorf("preview changed state.json:\nbefore:\n%s\nafter:\n%s", before.state, after.state)
	}
	if len(before.statuses) != len(after.statuses) {
		t.Errorf("preview changed statuses: %#v != %#v", before.statuses, after.statuses)
	}
	for path, status := range before.statuses {
		if after.statuses[path] != status {
			t.Errorf("preview changed status for %s: %q != %q", path, status, after.statuses[path])
		}
	}
	calls, err := os.ReadFile(scenario.rexLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(calls)); got != "session-ls\nsession-ls" {
		t.Errorf("preview Rex calls = %q, want two session listings", got)
	}
}

type cleanupSnapshot struct {
	refs      string
	worktrees string
	statuses  map[string]string
	state     string
}

func snapshotCleanupRepo(t *testing.T, scenario *cleanupScenario) cleanupSnapshot {
	t.Helper()
	snapshot := cleanupSnapshot{
		refs:      gitOutput(t, scenario.repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"),
		worktrees: gitOutput(t, scenario.repo, "worktree", "list", "--porcelain"),
		statuses:  make(map[string]string),
	}
	worktrees, err := git.ListWorktrees(context.Background(), scenario.repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, worktree := range worktrees {
		if _, err := os.Stat(worktree.Path); err != nil {
			snapshot.statuses[worktree.Path] = "<missing>"
			continue
		}
		snapshot.statuses[worktree.Path] = gitOutput(t, worktree.Path, "status", "--porcelain")
	}
	statePath, err := config.StatePath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	snapshot.state = string(data)
	return snapshot
}

func TestCleanupApplyRemovesSafeSetOnly(t *testing.T) {
	scenario := newCleanupScenario(t)
	state := config.NewState()
	for _, path := range []string{
		scenario.paths["merged"], scenario.paths["patch"], scenario.paths["unmerged"],
		scenario.paths["dirty"], scenario.paths["locked"], scenario.paths["develop"],
		scenario.paths["active"],
	} {
		state.SetStatus(path, config.StatusInProgress, time.Now())
	}
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}

	logPath := writeCleanupRex(t, `{"sessions":[{"session_id":"session:active","label":"repo/feat/active"}]}`)

	plan, err := PlanCleanup(context.Background(), scenario.cfg, CleanupOptions{Apply: true})
	if err != nil {
		t.Fatalf("plan apply: %v", err)
	}
	report, err := ApplyCleanup(context.Background(), scenario.cfg, plan)
	if err != nil {
		t.Fatalf("ApplyCleanup: %v", err)
	}
	if !report.Applied {
		t.Error("report is not marked applied")
	}
	if report.Totals.Removed != 5 || report.Totals.Failed != 0 || report.Totals.Kept != 8 {
		t.Errorf("totals = %#v", report.Totals)
	}

	for _, label := range []string{"merged", "patch"} {
		path := scenario.paths[label]
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s worktree still exists: %v", label, err)
		}
		if hasBranch(t, scenario.repo, "feat/"+label) {
			t.Errorf("%s branch still exists", label)
		}
	}
	if _, err := os.Stat(scenario.paths["gone"]); !os.IsNotExist(err) {
		t.Errorf("prunable worktree directory exists: %v", err)
	}
	if !hasBranch(t, scenario.repo, "feat/gone") {
		t.Error("pruning the worktree should leave its branch for a later run")
	}
	if hasBranch(t, scenario.repo, "feat/branch-merged") {
		t.Error("branch-only merged branch still exists")
	}
	if hasBranch(t, scenario.repo, "feat/branch-patch") {
		t.Error("branch-only patch-equivalent branch still exists")
	}
	for _, label := range []string{"unmerged", "dirty", "locked", "develop", "active", "detached"} {
		if _, err := os.Stat(scenario.paths[label]); err != nil {
			t.Errorf("%s worktree was removed: %v", label, err)
		}
	}
	if !hasBranch(t, scenario.repo, "feat/unmerged") || !hasBranch(t, scenario.repo, "develop") {
		t.Error("an unmerged or protected branch was deleted")
	}
	if !worktreeLocked(t, scenario.repo, scenario.paths["locked"]) {
		t.Error("locked worktree lost its lock")
	}
	if _, err := os.Stat(filepath.Join(scenario.paths["dirty"], "untracked.txt")); err != nil {
		t.Errorf("dirty worktree lost its untracked file: %v", err)
	}

	loaded, err := config.LoadState(scenario.cfg.StatusIDs())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{scenario.paths["merged"], scenario.paths["patch"]} {
		if _, ok := loaded.Workstreams[path]; ok {
			t.Errorf("state entry for removed worktree %s remains", path)
		}
	}
	for _, path := range []string{scenario.paths["dirty"], scenario.paths["active"], scenario.paths["develop"]} {
		if _, ok := loaded.Workstreams[path]; !ok {
			t.Errorf("state entry for kept worktree %s was removed", path)
		}
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "kill") || strings.Contains(string(calls), "unexpected") {
		t.Errorf("cleanup touched Rex beyond listing sessions: %q", calls)
	}
}

func TestCleanupForceRemovesOnlyDirtyAndUnmergedAfterNamingLoss(t *testing.T) {
	f := newCleanupFixture(t)
	dirty := f.worktree("feat/dirty")
	f.commit(dirty, "dirty.txt", "dirty\n", "dirty work")
	f.merge("feat/dirty")
	if err := os.WriteFile(filepath.Join(dirty, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	unmerged := f.worktree("feat/unmerged")
	f.commit(unmerged, "unmerged.txt", "unmerged\n", "unmerged work")

	active := f.worktree("feat/active")
	f.commit(active, "active.txt", "active\n", "active work")

	locked := f.worktree("feat/locked")
	f.commit(locked, "locked.txt", "locked\n", "locked work")
	gitRun(t, f.repo, "worktree", "lock", locked)

	develop := f.worktree("develop")
	f.commit(develop, "develop.txt", "develop\n", "develop work")

	branchOnly := f.worktree("feat/branch-unmerged")
	f.commit(branchOnly, "branch.txt", "branch\n", "branch-only work")
	gitRun(t, f.repo, "worktree", "remove", branchOnly)

	dirtyPath := canonical(t, dirty)
	unmergedPath := canonical(t, unmerged)
	activePath := canonical(t, active)
	lockedPath := canonical(t, locked)
	developPath := canonical(t, develop)

	writeCleanupRex(t, `{"sessions":[{"session_id":"session:active","label":"repo/feat/active"}]}`)

	plain, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true})
	if err != nil {
		t.Fatalf("plan without force: %v", err)
	}
	for _, branch := range []string{"feat/dirty", "feat/unmerged", "feat/branch-unmerged"} {
		item := cleanupItemByBranch(t, plain, branch)
		if item.Removable || item.Action != CleanupActionNone || !item.RequiresForce || item.Loss == nil {
			t.Errorf("%s without --force = %#v", branch, item)
		}
	}
	for _, branch := range []string{"feat/active", "feat/locked", "develop"} {
		item := cleanupItemByBranch(t, plain, branch)
		if item.Removable || item.RequiresForce || item.Loss != nil {
			t.Errorf("%s without --force = %#v", branch, item)
		}
	}
	if plain.Totals.Removed != 0 {
		t.Errorf("preview/apply plan removed without force: %#v", plain.Totals)
	}

	forced, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true, Force: true})
	if err != nil {
		t.Fatalf("plan with force: %v", err)
	}
	report, err := ApplyCleanup(context.Background(), f.cfg, forced)
	if err != nil {
		t.Fatalf("ApplyCleanup force: %v", err)
	}
	if report.Totals.Removed != 3 || report.Totals.Failed != 0 {
		t.Errorf("forced totals = %#v", report.Totals)
	}
	for _, path := range []string{dirtyPath, unmergedPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("forced worktree %s still exists: %v", path, err)
		}
	}
	if hasBranch(t, f.repo, "feat/dirty") || hasBranch(t, f.repo, "feat/unmerged") || hasBranch(t, f.repo, "feat/branch-unmerged") {
		t.Error("a force-removed branch still exists")
	}
	for _, path := range []string{activePath, lockedPath, developPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("blocked worktree %s was removed: %v", path, err)
		}
	}
	if !hasBranch(t, f.repo, "feat/active") || !hasBranch(t, f.repo, "develop") {
		t.Error("a blocked branch was deleted")
	}
}

func TestCleanupApplyRefusesWithoutRex(t *testing.T) {
	f := newCleanupFixture(t)
	merged := f.worktree("feat/merged")
	f.commit(merged, "merged.txt", "merged\n", "merged work")
	f.merge("feat/merged")

	writeFakeRex(t, "exit 1\n")

	preview, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{})
	if err != nil {
		t.Fatalf("preview without Rex: %v", err)
	}
	if len(preview.Warnings) == 0 || !strings.Contains(preview.Warnings[0], "Rex unavailable") {
		t.Errorf("warnings = %#v", preview.Warnings)
	}
	if _, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true}); err == nil ||
		!strings.Contains(err.Error(), "cannot verify live Rex sessions") {
		t.Fatalf("apply without Rex err = %v", err)
	}
	if _, err := os.Stat(canonical(t, merged)); err != nil {
		t.Errorf("apply plan mutated a worktree without Rex: %v", err)
	}
}

func TestCleanupProjectFilterAndCrossRepo(t *testing.T) {
	f := newCleanupFixture(t)
	other := testRepo(t)
	gitRun(t, other, "branch", "-M", "main")
	if _, _, err := f.cfg.RegisterProject(canonical(t, other), "other", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.cfg.Save(); err != nil {
		t.Fatal(err)
	}

	all, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{})
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(all.Projects) != 2 || all.Projects[0].Project != "other" || all.Projects[1].Project != "repo" {
		t.Fatalf("projects = %#v", all.Projects)
	}
	only, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Project: "repo"})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(only.Projects) != 1 || only.Projects[0].Project != "repo" {
		t.Errorf("filtered projects = %#v", only.Projects)
	}
	if _, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Project: "missing"}); err == nil ||
		!strings.Contains(err.Error(), "unknown project") {
		t.Errorf("unknown project err = %v", err)
	}
}

func TestCleanupReportJSONIsStable(t *testing.T) {
	scenario := newCleanupScenario(t)
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
		t.Errorf("two plans differ:\n%s\n---\n%s", a, b)
	}
	var document struct {
		SchemaVersion int                        `json:"schema_version"`
		Projects      []map[string]any           `json:"projects"`
		Totals        map[string]json.RawMessage `json:"totals"`
	}
	if err := json.Unmarshal(a, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 {
		t.Errorf("schema_version = %d", document.SchemaVersion)
	}
	if len(document.Projects) != 1 {
		t.Errorf("projects = %#v", document.Projects)
	}
	byClassification := string(document.Totals["by_classification"])
	for _, key := range []string{"prunable", "merged", "patch-equivalent", "unmerged", "dirty", "protected", "locked", "detached", "active", "unknown"} {
		if !strings.Contains(byClassification, `"`+key+`"`) {
			t.Errorf("by_classification is missing %q: %s", key, byClassification)
		}
	}
	projectsJSON := string(a)
	for _, key := range []string{`"classification"`, `"branch_state"`, `"requires_force"`, `"action"`, `"detail"`, `"loss"`} {
		if !strings.Contains(projectsJSON, key) {
			t.Errorf("report JSON is missing %s", key)
		}
	}

	var raw struct {
		Projects []struct {
			Items []map[string]json.RawMessage `json:"items"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(a, &raw); err != nil {
		t.Fatal(err)
	}
	itemKeys := []string{
		"project", "kind", "path", "branch", "head", "classification", "branch_state",
		"safe", "removable", "requires_force", "dirty", "locked", "detached",
		"active_session", "session_id", "action", "result", "detail", "loss", "error",
	}
	lossKeys := []string{"dirty_files", "commits"}
	sawNullLoss, sawObjectLoss, sawDetached, sawBranchOnly := false, false, false, false
	for _, project := range raw.Projects {
		for _, item := range project.Items {
			for _, key := range itemKeys {
				if _, ok := item[key]; !ok {
					t.Errorf("item is missing JSON key %q: %s", key, string(a))
				}
			}
			var branch, path string
			var detached bool
			if err := json.Unmarshal(item["branch"], &branch); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(item["path"], &path); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(item["detached"], &detached); err != nil {
				t.Fatal(err)
			}
			if branch == "" && detached {
				sawDetached = true
			}
			if path == "" {
				sawBranchOnly = true
			}
			switch string(item["loss"]) {
			case "null":
				sawNullLoss = true
			default:
				sawObjectLoss = true
				var loss map[string]json.RawMessage
				if err := json.Unmarshal(item["loss"], &loss); err != nil {
					t.Fatalf("parse loss: %v", err)
				}
				for _, key := range lossKeys {
					if _, ok := loss[key]; !ok {
						t.Errorf("loss is missing JSON key %q: %s", key, string(item["loss"]))
					}
				}
			}
		}
	}
	if !sawNullLoss || !sawObjectLoss || !sawDetached || !sawBranchOnly {
		t.Errorf("key-set coverage: nullLoss=%v objectLoss=%v detached=%v branchOnly=%v",
			sawNullLoss, sawObjectLoss, sawDetached, sawBranchOnly)
	}
}

func TestCleanupIgnoredOnlyWorktreeRequiresForce(t *testing.T) {
	f := newCleanupFixture(t)
	f.commit(f.repo, ".gitignore", "*.secret\n", "ignore secrets")
	path := f.worktree("feat/ignored")
	path = canonical(t, path)
	if err := os.WriteFile(filepath.Join(path, "only.secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The ordinary dirty check does not see ignored files; cleanup must.
	if dirty, err := git.IsDirty(context.Background(), path); err != nil || dirty {
		t.Fatalf("ignored file made IsDirty say dirty=%v err=%v", dirty, err)
	}

	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, plan, "feat/ignored")
	if item.Classification != CleanupDirty || !item.Dirty || item.BranchState != CleanupBranchMerged {
		t.Errorf("ignored-only item = %#v", item)
	}
	if item.Removable || item.Action != CleanupActionNone || !item.RequiresForce {
		t.Errorf("ignored-only item without force = %#v", item)
	}
	if item.Loss == nil || !lossContains(item.Loss.DirtyFiles, "only.secret") {
		t.Errorf("ignored-only loss = %#v", item.Loss)
	}

	kept, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatalf("apply without force: %v", err)
	}
	if kept.Totals.Removed != 0 {
		t.Errorf("apply without force removed %d items", kept.Totals.Removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("ignored-only worktree was removed without force: %v", err)
	}

	forced, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	forcedItem := cleanupItemByBranch(t, forced, "feat/ignored")
	if !forcedItem.Removable || forcedItem.Action != CleanupActionRemoveWorktreeAndBranch {
		t.Errorf("ignored-only item with force = %#v", forcedItem)
	}
	report, err := ApplyCleanup(context.Background(), f.cfg, forced)
	if err != nil {
		t.Fatalf("apply with force: %v", err)
	}
	if report.Totals.Removed != 1 || report.Totals.Failed != 0 {
		t.Errorf("forced totals = %#v", report.Totals)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("ignored-only worktree still exists: %v", err)
	}
	if hasBranch(t, f.repo, "feat/ignored") {
		t.Error("ignored-only branch still exists")
	}
}

func TestCleanupBranchOnlyActiveSessionIsNeverRemoved(t *testing.T) {
	f := newCleanupFixture(t)
	worktree := f.worktree("feat/branch-active")
	f.commit(worktree, "work.txt", "work\n", "work")
	gitRun(t, f.repo, "worktree", "remove", worktree)

	writeCleanupRex(t, `{"sessions":[
	  {"session_id":"session:one","label":"repo/feat/branch-active"},
	  {"session_id":"session:two","label":"repo/feat/branch-active"}
	]}`)

	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, plan, "feat/branch-active")
	if item.Kind != "branch" || item.Classification != CleanupActive || !item.ActiveSession {
		t.Errorf("branch-only active item = %#v", item)
	}
	if item.SessionID != "session:one" {
		t.Errorf("session id = %q, want the first duplicate label", item.SessionID)
	}
	if !strings.Contains(item.Detail, "session:one, session:two") {
		t.Errorf("detail does not name both duplicate sessions: %q", item.Detail)
	}
	if item.Removable || item.Action != CleanupActionNone {
		t.Errorf("active branch-only item is removable: %#v", item)
	}

	report, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Removed != 0 {
		t.Errorf("active branch-only item was removed: %#v", report.Totals)
	}
	if !hasBranch(t, f.repo, "feat/branch-active") {
		t.Error("active branch-only branch was deleted")
	}
}

func TestCleanupUnknownAndProofFailureAreBlocked(t *testing.T) {
	f := newCleanupFixture(t)
	project := config.Project{Name: "repo", Path: f.repo}

	// A real, clean directory with no branch and no detached flag is unknown.
	unknown := planCleanupWorktree(context.Background(), project,
		git.Worktree{Path: f.repo, Head: "deadbeef"},
		"/not-the-main-worktree", "main", nil, CleanupOptions{Force: true})
	if unknown.Classification != CleanupUnknown || unknown.Safe || unknown.Removable || unknown.Action != CleanupActionNone {
		t.Errorf("unknown worktree = %#v", unknown)
	}

	// A branch that cannot be read during the proof is unknown, never
	// force-removable, even with an empty loss.
	missing := planCleanupBranch(context.Background(), project, "does-not-exist", "main", nil, CleanupOptions{Force: true})
	if missing.Classification != CleanupUnknown || missing.BranchState != "" || missing.Safe || missing.Removable || missing.Action != CleanupActionNone {
		t.Errorf("unreadable branch = %#v", missing)
	}
	if !strings.Contains(missing.Detail, "branch check failed") {
		t.Errorf("detail does not keep the proof error: %q", missing.Detail)
	}

	// The same proof failure on a real worktree blocks it too.
	path := canonical(t, f.worktree("feat/real"))
	failed := planCleanupWorktree(context.Background(), project,
		git.Worktree{Path: path, Branch: "does-not-exist", Head: "deadbeef"},
		"/not-the-main-worktree", "main", nil, CleanupOptions{Force: true})
	if failed.Classification != CleanupUnknown || failed.Removable || failed.Action != CleanupActionNone {
		t.Errorf("worktree with a failed proof = %#v", failed)
	}
	if !strings.Contains(failed.Detail, "branch check failed") {
		t.Errorf("worktree detail does not keep the proof error: %q", failed.Detail)
	}
}

func TestCleanupRejectsEvilMergeAndRevertedPatch(t *testing.T) {
	f := newCleanupFixture(t)
	addEvilMergeBranch(t, f)
	addRevertedPatchBranch(t, f)
	writeCleanupRex(t, `{"sessions":[]}`)

	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"feat/evil", "feat/revert"} {
		item := cleanupItemByBranch(t, plan, branch)
		if item.Classification != CleanupUnmerged || item.BranchState != CleanupBranchUnmerged {
			t.Errorf("%s = %#v, want unmerged", branch, item)
		}
		if item.Safe || item.Removable || item.Action != CleanupActionNone {
			t.Errorf("%s is removable without force: %#v", branch, item)
		}
	}
	report, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Removed != 0 {
		t.Errorf("evil merge or reverted patch was removed without force: %#v", report.Totals)
	}
	for _, branch := range []string{"feat/evil", "feat/revert"} {
		if !hasBranch(t, f.repo, branch) {
			t.Errorf("%s was deleted without force", branch)
		}
	}
}

func addEvilMergeBranch(t *testing.T, f *cleanupFixture) {
	t.Helper()
	f.commit(f.repo, "one.txt", "one\n", "one")
	first := headSHA(t, f.repo)
	f.commit(f.repo, "two.txt", "two\n", "two")
	second := headSHA(t, f.repo)

	content := f.worktree("content")
	f.commit(content, "evil.txt", "unique conflict resolution\n", "content")
	tree := strings.TrimSpace(gitOutput(t, content, "rev-parse", "HEAD^{tree}"))
	evil := strings.TrimSpace(gitOutput(t, f.repo, "commit-tree", tree, "-p", first, "-p", second, "-m", "evil merge"))
	gitRun(t, f.repo, "branch", "feat/evil", evil)
	gitRun(t, f.repo, "worktree", "remove", content)
	gitRun(t, f.repo, "branch", "-D", "content")
}

func addRevertedPatchBranch(t *testing.T, f *cleanupFixture) {
	t.Helper()
	feature := f.worktree("feat/revert")
	f.commit(feature, "feature.txt", "feature\n", "feature")
	sha := headSHA(t, feature)
	f.commit(f.repo, "main-before-revert.txt", "main\n", "advance main")
	gitRun(t, f.repo, "cherry-pick", sha)
	gitRun(t, f.repo, "revert", "--no-edit", "HEAD")
	gitRun(t, f.repo, "worktree", "remove", feature)
}

func lossContains(files []string, needle string) bool {
	for _, file := range files {
		if strings.Contains(file, needle) {
			return true
		}
	}
	return false
}

func TestCleanupApplyRejectsDirtiedWorktreeAfterPlan(t *testing.T) {
	f := newCleanupFixture(t)
	target := f.worktree("feat/merged")
	f.commit(target, "merged.txt", "merged\n", "merged work")
	f.merge("feat/merged")
	target = canonical(t, target)

	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "late.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, report, "feat/merged")
	if item.Result != CleanupResultFailed || !strings.Contains(item.Error, "changed after planning") {
		t.Errorf("item = %#v", item)
	}
	if report.Totals.Removed != 0 || report.Totals.Failed != 1 {
		t.Errorf("totals = %#v", report.Totals)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("worktree was removed despite the late change: %v", err)
	}
	if !hasBranch(t, f.repo, "feat/merged") {
		t.Error("branch was deleted despite the late change")
	}
}

func TestCleanupApplyRejectsBranchMovedAfterPlan(t *testing.T) {
	f := newCleanupFixture(t)
	gitRun(t, f.repo, "branch", "feat/branch-merged", "main")
	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.TrimSpace(gitOutput(t, f.repo, "commit-tree", "main^{tree}", "-p", "main", "-m", "moved"))
	gitRun(t, f.repo, "update-ref", "refs/heads/feat/branch-merged", moved)

	report, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, report, "feat/branch-merged")
	if item.Result != CleanupResultFailed || !strings.Contains(item.Error, "moved after planning") {
		t.Errorf("item = %#v", item)
	}
	if !hasBranch(t, f.repo, "feat/branch-merged") {
		t.Error("moved branch was deleted")
	}
}

func TestCleanupApplyRejectsNewRexSessionAfterPlan(t *testing.T) {
	f := newCleanupFixture(t)
	target := f.worktree("feat/merged")
	f.commit(target, "merged.txt", "merged\n", "merged work")
	f.merge("feat/merged")
	target = canonical(t, target)

	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	writeCleanupRex(t, `{"sessions":[{"session_id":"session:late","label":"repo/feat/merged"}]}`)

	report, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, report, "feat/merged")
	if item.Result != CleanupResultFailed || !strings.Contains(item.Error, "started after planning") {
		t.Errorf("item = %#v", item)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("worktree was removed despite the new session: %v", err)
	}
	if !hasBranch(t, f.repo, "feat/merged") {
		t.Error("branch was deleted despite the new session")
	}
}

func TestCleanupApplyRejectsLostMergeProofAfterPlan(t *testing.T) {
	f := newCleanupFixture(t)
	target := f.worktree("feat/merged")
	f.commit(target, "merged.txt", "merged\n", "merged work")
	f.merge("feat/merged")

	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	// Move the default branch back so the branch is no longer an ancestor.
	gitRun(t, f.repo, "reset", "--hard", "HEAD~1")

	report, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, report, "feat/merged")
	if item.Result != CleanupResultFailed || !strings.Contains(item.Error, "no longer provably merged") {
		t.Errorf("item = %#v", item)
	}
	if !hasBranch(t, f.repo, "feat/merged") {
		t.Error("branch was deleted after losing its merge proof")
	}
}

func TestCleanupApplyRefusesWhenRexDisappearsAfterPreview(t *testing.T) {
	f := newCleanupFixture(t)
	target := f.worktree("feat/merged")
	f.commit(target, "merged.txt", "merged\n", "merged work")
	f.merge("feat/merged")
	target = canonical(t, target)

	// Plan with Apply=false; the Rex gate lives in ApplyCleanup too.
	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeFakeRex(t, "exit 1\n")
	if _, err := ApplyCleanup(context.Background(), f.cfg, plan); err == nil ||
		!strings.Contains(err.Error(), "cannot verify live Rex sessions before applying") {
		t.Fatalf("apply err = %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("preview plan was applied without Rex: %v", err)
	}
}

func TestCleanupApplyFallsBackToForceDeleteWhenHeadIsBehind(t *testing.T) {
	f := newCleanupFixture(t)
	behind := headSHA(t, f.repo)
	target := f.worktree("feat/merged")
	f.commit(target, "merged.txt", "merged\n", "merged work")
	f.merge("feat/merged")
	targetPath := canonical(t, target)
	// The default branch main contains the merge, but the main checkout is on
	// an older branch. git branch -d checks HEAD and refuses; the base-relative
	// proof still holds, so apply must fall back to -D.
	gitRun(t, f.repo, "checkout", "-q", "-b", "work", behind)

	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	report, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, report, "feat/merged")
	if item.Result != CleanupResultRemoved {
		t.Errorf("item = %#v", item)
	}
	if hasBranch(t, f.repo, "feat/merged") {
		t.Error("merged branch remains when the main checkout is behind")
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Errorf("worktree remains: %v", err)
	}
}

func TestCleanupApplyRechecksRexImmediatelyBeforeMutation(t *testing.T) {
	f := newCleanupFixture(t)
	target := f.worktree("feat/merged")
	f.commit(target, "merged.txt", "merged\n", "merged work")
	f.merge("feat/merged")
	targetPath := canonical(t, target)

	counter := filepath.Join(t.TempDir(), "rex.count")
	t.Setenv("FAKE_REX_COUNTER", counter)
	writeFakeRex(t, `counter="$FAKE_REX_COUNTER"
n=0
if [ -f "$counter" ]; then n=$(cat "$counter"); fi
n=$((n+1))
printf '%s' "$n" > "$counter"
if [ "$n" -ge 3 ]; then
  printf '%s\n' '{"sessions":[{"session_id":"session:late","label":"repo/feat/merged"}]}'
else
  printf '%s\n' '{"sessions":[]}'
fi
`)

	// Plan lists Rex once (empty). Apply gates once (empty), then re-lists
	// immediately before mutating (active), which must keep the item.
	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	report, err := ApplyCleanup(context.Background(), f.cfg, plan)
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, report, "feat/merged")
	if item.Result != CleanupResultFailed || !strings.Contains(item.Error, "started after planning") {
		t.Errorf("item = %#v", item)
	}
	if _, err := os.Stat(targetPath); err != nil {
		t.Errorf("worktree was removed despite the late session: %v", err)
	}
	if !hasBranch(t, f.repo, "feat/merged") {
		t.Error("branch was deleted despite the late session")
	}
}

func TestCleanupUnrelatedHistoryIsUnknown(t *testing.T) {
	f := newCleanupFixture(t)
	gitRun(t, f.repo, "checkout", "-q", "--orphan", "unrelated")
	gitRun(t, f.repo, "commit", "--allow-empty", "-q", "-m", "orphan")
	gitRun(t, f.repo, "checkout", "-q", "main")

	plan, err := PlanCleanup(context.Background(), f.cfg, CleanupOptions{Apply: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	item := cleanupItemByBranch(t, plan, "unrelated")
	if item.Classification != CleanupUnknown || item.BranchState != "" || item.Safe || item.Removable || item.Action != CleanupActionNone {
		t.Errorf("unrelated branch = %#v", item)
	}
	if !strings.Contains(item.Detail, "no common ancestor") {
		t.Errorf("detail does not explain the unknown state: %q", item.Detail)
	}
}

func hasBranch(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	err := cmd.Run()
	if err == nil {
		return true
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false
	}
	t.Fatalf("show-ref %s: %v", branch, err)
	return false
}

func worktreeLocked(t *testing.T, repo, path string) bool {
	t.Helper()
	worktrees, err := git.ListWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, worktree := range worktrees {
		if worktree.Path == path {
			return worktree.Locked
		}
	}
	return false
}
