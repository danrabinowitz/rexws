package ops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/rex"
)

// CleanupClassification is the primary reason a worktree or branch was
// classified. It is the stable value scripts and the future S2 JSON contract
// can switch on.
type CleanupClassification string

const (
	CleanupPrunable        CleanupClassification = "prunable"
	CleanupMerged          CleanupClassification = "merged"
	CleanupPatchEquivalent CleanupClassification = "patch-equivalent"
	CleanupUnmerged        CleanupClassification = "unmerged"
	CleanupDirty           CleanupClassification = "dirty"
	CleanupProtected       CleanupClassification = "protected"
	CleanupLocked          CleanupClassification = "locked"
	CleanupDetached        CleanupClassification = "detached"
	CleanupActive          CleanupClassification = "active"
	CleanupUnknown         CleanupClassification = "unknown"
)

// CleanupBranchState is the branch's state relative to the project's default
// branch, independent of whether the checkout itself is dirty, locked,
// detached, protected, or live in Rex.
type CleanupBranchState string

const (
	CleanupBranchMerged          CleanupBranchState = "merged"
	CleanupBranchPatchEquivalent CleanupBranchState = "patch-equivalent"
	CleanupBranchUnmerged        CleanupBranchState = "unmerged"
)

// CleanupAction is what --apply would do to one item. The same value is
// returned in a preview and then carried through the apply pass.
type CleanupAction string

const (
	CleanupActionNone                    CleanupAction = "none"
	CleanupActionPrune                   CleanupAction = "prune"
	CleanupActionRemoveWorktree          CleanupAction = "remove-worktree"
	CleanupActionDeleteBranch            CleanupAction = "delete-branch"
	CleanupActionRemoveWorktreeAndBranch CleanupAction = "remove-worktree-and-branch"
)

// CleanupResult is the outcome of an apply pass. It is empty in a preview.
type CleanupResult string

const (
	CleanupResultKept    CleanupResult = "kept"
	CleanupResultRemoved CleanupResult = "removed"
	CleanupResultFailed  CleanupResult = "failed"
)

// CleanupOptions selects what to scan and whether to mutate. The zero value is
// the report-only default.
type CleanupOptions struct {
	Project string
	Apply   bool
	Force   bool
}

// CleanupLoss names exactly what a forced removal discards. DirtyFiles are
// git status --porcelain lines, including ignored paths. Commits are the
// commits reachable from the branch but not from the default branch. Both are
// always arrays when Loss is present so the JSON shape does not change.
type CleanupLoss struct {
	DirtyFiles []string     `json:"dirty_files"`
	Commits    []git.Commit `json:"commits"`
}

// CleanupItem is one worktree or one local branch that is not checked out in a
// worktree. Every item carries every field: empty paths, branches, and session
// IDs are empty strings, and Loss is null when nothing would be lost. Safe
// means the item is in the no-force safe set; Removable means the current flags
// will act on it. Action is what apply will do, and Result is filled in after
// apply.
type CleanupItem struct {
	Project        string                `json:"project"`
	Kind           string                `json:"kind"` // worktree or branch
	Path           string                `json:"path"`
	Branch         string                `json:"branch"`
	Head           string                `json:"head"`
	Classification CleanupClassification `json:"classification"`
	BranchState    CleanupBranchState    `json:"branch_state"`
	Safe           bool                  `json:"safe"`
	Removable      bool                  `json:"removable"`
	RequiresForce  bool                  `json:"requires_force"`
	Dirty          bool                  `json:"dirty"`
	Locked         bool                  `json:"locked"`
	Detached       bool                  `json:"detached"`
	ActiveSession  bool                  `json:"active_session"`
	SessionID      string                `json:"session_id"`
	Action         CleanupAction         `json:"action"`
	Result         CleanupResult         `json:"result"`
	Detail         string                `json:"detail"`
	Loss           *CleanupLoss          `json:"loss"`
	Error          string                `json:"error"`

	// sessionIDs keeps every label match for the detail text when Rex has
	// duplicate labels. JSON carries the first as session_id.
	sessionIDs []string
}

// CleanupProject is the cleanup view of one registered project.
type CleanupProject struct {
	Project       string        `json:"project"`
	Path          string        `json:"path"`
	DefaultBranch string        `json:"default_branch"`
	Items         []CleanupItem `json:"items"`
}

// CleanupTotals summarizes a report. ByClassification always carries every
// classification key, including zero counts, so consumers see a fixed shape.
type CleanupTotals struct {
	Scanned          int            `json:"scanned"`
	Safe             int            `json:"safe"`
	Removable        int            `json:"removable"`
	Kept             int            `json:"kept"`
	Removed          int            `json:"removed"`
	Failed           int            `json:"failed"`
	ByClassification map[string]int `json:"by_classification"`
}

// CleanupReport is the stable JSON document behind `rexws cleanup --json`.
// SchemaVersion is 1 for this shape. Projects and Items are sorted, so two
// scans of the same repository produce identical JSON.
type CleanupReport struct {
	SchemaVersion int              `json:"schema_version"`
	Applied       bool             `json:"applied"`
	Force         bool             `json:"force"`
	Projects      []CleanupProject `json:"projects"`
	Totals        CleanupTotals    `json:"totals"`
	Warnings      []string         `json:"warnings"`
	Errors        []string         `json:"errors"`
}

// Err returns the project errors and any failed apply actions. A preview with
// unreadable projects still prints its report, then exits nonzero through this.
func (r CleanupReport) Err() error {
	parts := append([]string{}, r.Errors...)
	if r.Totals.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d cleanup action(s) failed", r.Totals.Failed))
	}
	if len(parts) == 0 {
		return nil
	}
	return errors.New(strings.Join(parts, "; "))
}

// PlanCleanup classifies every worktree and branch in the selected projects
// without mutating anything. Apply is used only to decide whether actions are
// allowed: an apply plan requires Rex to be reachable so an active session can
// never be missed. A report-only plan proceeds with a warning when Rex cannot
// be read.
func PlanCleanup(ctx context.Context, cfg config.Config, opts CleanupOptions) (CleanupReport, error) {
	projects := cfg.SortedProjects()
	if opts.Project != "" {
		project, ok := cfg.Project(opts.Project)
		if !ok {
			return CleanupReport{}, fmt.Errorf("unknown project %q (registered: %s)", opts.Project, projectNames(cfg))
		}
		projects = []config.Project{project}
	}

	report := CleanupReport{
		SchemaVersion: 1,
		Force:         opts.Force,
		Projects:      make([]CleanupProject, 0, len(projects)),
		Warnings:      []string{},
		Errors:        []string{},
	}

	active, rexErr := activeSessions(ctx, cfg)
	if rexErr != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Rex unavailable: %v; active sessions could not be checked", rexErr))
		if opts.Apply {
			return report, fmt.Errorf("cannot verify live Rex sessions: %w; refusing to remove anything", rexErr)
		}
	}

	for _, project := range projects {
		planned, err := planCleanupProject(ctx, project, opts, active)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("project %s: %v", project.Name, err))
			continue
		}
		report.Projects = append(report.Projects, planned)
	}

	recountCleanup(&report, true)
	return report, nil
}

// ApplyCleanup executes a plan. Before touching anything it re-lists Rex
// sessions and every project's worktrees, then re-verifies each item: no live
// session may have appeared, the worktree must still be clean (or still have
// exactly the reported dirty paths), the branch must not have moved, and the
// merged or patch-equivalent proof must still hold. A failed re-check keeps the
// item and reports it, so a stale plan cannot delete data.
func ApplyCleanup(ctx context.Context, cfg config.Config, report CleanupReport) (CleanupReport, error) {
	// Gate before doing any scan work, then list Rex again immediately before
	// the mutation loop so a session started during setup is seen.
	if _, err := activeSessions(ctx, cfg); err != nil {
		return report, fmt.Errorf("cannot verify live Rex sessions before applying: %w; refusing to remove anything", err)
	}

	pruned := make(map[string]bool)
	removedWorktrees := make([]string, 0)
	worktrees := make(map[string][]git.Worktree, len(report.Projects))
	worktreeErrs := make(map[string]error, len(report.Projects))
	for _, project := range report.Projects {
		list, err := git.ListWorktrees(ctx, project.Path)
		worktrees[project.Path] = list
		worktreeErrs[project.Path] = err
	}

	active, err := activeSessions(ctx, cfg)
	if err != nil {
		return report, fmt.Errorf("cannot verify live Rex sessions before applying: %w; refusing to remove anything", err)
	}
	report.Applied = true

	for pi := range report.Projects {
		project := &report.Projects[pi]
		for ii := range project.Items {
			item := &project.Items[ii]
			item.Result = CleanupResultKept
			switch item.Action {
			case CleanupActionNone:
				continue
			case CleanupActionPrune:
				if worktreeErrs[project.Path] != nil {
					item.fail(fmt.Errorf("re-list worktrees: %w", worktreeErrs[project.Path]))
					continue
				}
				if err := verifyCleanupPrune(worktrees[project.Path], *item); err != nil {
					item.fail(err)
					continue
				}
				if !pruned[project.Path] {
					if err := git.PruneWorktrees(ctx, project.Path); err != nil {
						item.fail(err)
						continue
					}
					pruned[project.Path] = true
				}
				item.Result = CleanupResultRemoved
				if item.Path != "" {
					removedWorktrees = append(removedWorktrees, item.Path)
				}
			case CleanupActionDeleteBranch:
				if ids := active[cleanupSessionLabel(*item)]; len(ids) > 0 {
					item.fail(fmt.Errorf("live Rex session %s started after planning; kept", strings.Join(ids, ", ")))
					continue
				}
				deleteForce, err := verifyCleanupBranch(ctx, project.Path, project.DefaultBranch, *item, report.Force)
				if err != nil {
					item.fail(err)
					continue
				}
				if err := deleteCleanupBranch(ctx, project.Path, project.DefaultBranch, item, deleteForce); err != nil {
					item.fail(err)
					continue
				}
				item.Result = CleanupResultRemoved
			case CleanupActionRemoveWorktree, CleanupActionRemoveWorktreeAndBranch:
				if ids := active[cleanupSessionLabel(*item)]; len(ids) > 0 {
					item.fail(fmt.Errorf("live Rex session %s started after planning; kept", strings.Join(ids, ", ")))
					continue
				}
				if worktreeErrs[project.Path] != nil {
					item.fail(fmt.Errorf("re-list worktrees: %w", worktreeErrs[project.Path]))
					continue
				}
				if err := verifyCleanupWorktree(ctx, worktrees[project.Path], *item); err != nil {
					item.fail(err)
					continue
				}
				var deleteForce bool
				if item.Action == CleanupActionRemoveWorktreeAndBranch && item.Branch != "" {
					deleteForce, err = verifyCleanupBranch(ctx, project.Path, project.DefaultBranch, *item, report.Force)
					if err != nil {
						item.fail(err)
						continue
					}
				}
				if err := git.RemoveWorktree(ctx, project.Path, item.Path, item.RequiresForce); err != nil {
					item.fail(err)
					continue
				}
				removedWorktrees = append(removedWorktrees, item.Path)
				if item.Action == CleanupActionRemoveWorktreeAndBranch && item.Branch != "" {
					if deleteForce {
						// -D has no built-in re-check; closing the post-removal
						// window requires proving the branch once more.
						if _, err := verifyCleanupBranch(ctx, project.Path, project.DefaultBranch, *item, report.Force); err != nil {
							item.fail(fmt.Errorf("worktree removed but branch kept: %w", err))
							continue
						}
					}
					if err := deleteCleanupBranch(ctx, project.Path, project.DefaultBranch, item, deleteForce); err != nil {
						item.fail(err)
						continue
					}
				}
				item.Result = CleanupResultRemoved
			default:
				item.fail(fmt.Errorf("unknown cleanup action %q", item.Action))
			}
		}
	}

	var stateErr error
	if len(removedWorktrees) > 0 {
		stateErr = clearWorktreeState(cfg, removedWorktrees)
	}
	recountCleanup(&report, false)
	if stateErr != nil {
		return report, stateErr
	}
	return report, nil
}

func (item *CleanupItem) fail(err error) {
	item.Result = CleanupResultFailed
	item.Error = err.Error()
}

func cleanupSessionLabel(item CleanupItem) string {
	name := item.Branch
	if name == "" {
		// Match git.Worktree.ShortHead, which planning uses for detached
		// workstream session labels.
		name = shortCleanupSessionHead(item.Head)
	}
	return item.Project + "/" + name
}

func shortCleanupSessionHead(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func verifyCleanupPrune(worktrees []git.Worktree, item CleanupItem) error {
	var current *git.Worktree
	for i := range worktrees {
		if worktrees[i].Path == item.Path {
			current = &worktrees[i]
			break
		}
	}
	if current == nil {
		// Already pruned; nothing left to remove.
		return nil
	}
	if current.Locked {
		return fmt.Errorf("worktree %s is locked; kept", item.Path)
	}
	if !current.Prunable {
		return fmt.Errorf("worktree %s is no longer prunable; kept", item.Path)
	}
	if _, err := os.Stat(item.Path); err == nil {
		return fmt.Errorf("worktree %s directory still exists; kept", item.Path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("re-check worktree %s: %w", item.Path, err)
	}
	return nil
}

func verifyCleanupWorktree(ctx context.Context, worktrees []git.Worktree, item CleanupItem) error {
	var current *git.Worktree
	for i := range worktrees {
		if worktrees[i].Path == item.Path {
			current = &worktrees[i]
			break
		}
	}
	if current == nil {
		return fmt.Errorf("worktree %s is no longer registered; kept", item.Path)
	}
	if current.Locked || item.Locked {
		return fmt.Errorf("worktree %s is locked; kept", item.Path)
	}
	if current.Detached != item.Detached || current.Branch != item.Branch {
		return fmt.Errorf("worktree %s changed branch or HEAD after planning; kept", item.Path)
	}
	if current.Prunable {
		return fmt.Errorf("worktree %s became prunable after planning; kept", item.Path)
	}
	if _, err := os.Stat(item.Path); err != nil {
		return fmt.Errorf("worktree %s directory is missing; kept", item.Path)
	}
	lines, err := git.StatusLinesIncludingIgnored(ctx, item.Path)
	if err != nil {
		return fmt.Errorf("re-check worktree %s: %w", item.Path, err)
	}
	if !item.RequiresForce {
		if len(lines) > 0 {
			return fmt.Errorf("worktree %s changed after planning (%s); kept", item.Path, dirtyDescription(len(lines), nil))
		}
		return nil
	}
	if item.Loss == nil || !sameStrings(lines, item.Loss.DirtyFiles) {
		return fmt.Errorf("worktree %s changed after planning; the reported loss is stale; kept", item.Path)
	}
	return nil
}

// verifyCleanupBranch re-proves the branch with the same rule the plan used and
// returns whether the deletion must use `git branch -D`. Merged branches use
// -d; only a freshly re-proven patch-equivalent branch or an explicitly forced
// unmerged branch may use -D.
func verifyCleanupBranch(ctx context.Context, repoPath, base string, item CleanupItem, force bool) (bool, error) {
	if item.Branch == "" {
		return false, errors.New("item has no branch")
	}
	current, err := git.BranchSHA(ctx, repoPath, item.Branch)
	if err != nil {
		return false, fmt.Errorf("re-check branch %s: %w", item.Branch, err)
	}
	if item.Head != "" && current != item.Head {
		return false, fmt.Errorf("branch %s moved after planning; kept", item.Branch)
	}
	merged, err := git.IsAncestor(ctx, repoPath, item.Branch, base)
	if err != nil {
		return false, fmt.Errorf("re-check branch %s ancestry: %w", item.Branch, err)
	}
	if merged {
		return false, nil
	}
	if item.RequiresForce && (item.BranchState == CleanupBranchPatchEquivalent || item.BranchState == CleanupBranchUnmerged) {
		if item.Loss == nil {
			return false, fmt.Errorf("branch %s has no reported loss; kept", item.Branch)
		}
		commits, err := git.CommitsOutside(ctx, repoPath, base, item.Branch)
		if err != nil {
			return false, fmt.Errorf("re-check branch %s commits: %w", item.Branch, err)
		}
		if !sameCommits(commits, item.Loss.Commits) {
			return false, fmt.Errorf("branch %s loss changed after planning; kept", item.Branch)
		}
	}
	switch item.BranchState {
	case CleanupBranchPatchEquivalent:
		patchEquivalent, err := git.PatchEquivalent(ctx, repoPath, base, item.Branch)
		if err != nil {
			return false, fmt.Errorf("re-check branch %s patch-equivalence: %w", item.Branch, err)
		}
		if !patchEquivalent {
			return false, fmt.Errorf("branch %s is no longer patch-equivalent to %s; kept", item.Branch, base)
		}
		return true, nil
	case CleanupBranchUnmerged:
		if !force {
			return false, fmt.Errorf("branch %s is unmerged and --force was not used; kept", item.Branch)
		}
		return true, nil
	default:
		return false, fmt.Errorf("branch %s is no longer provably merged or patch-equivalent; kept", item.Branch)
	}
}

// deleteCleanupBranch deletes with -d first for a merged branch. git branch -d
// checks whether the branch is merged into HEAD, which is stricter than the
// base-relative ancestry proof; the main checkout may be on another branch. If
// -d refuses, re-prove the base-relative property and use -D when it still holds.
func deleteCleanupBranch(ctx context.Context, repoPath, base string, item *CleanupItem, force bool) error {
	if force {
		return git.DeleteBranch(ctx, repoPath, item.Branch, true)
	}
	err := git.DeleteBranch(ctx, repoPath, item.Branch, false)
	if err == nil {
		return nil
	}
	if _, proofErr := verifyCleanupBranch(ctx, repoPath, base, *item, false); proofErr != nil {
		return err
	}
	return git.DeleteBranch(ctx, repoPath, item.Branch, true)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameCommits(left, right []git.Commit) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func planCleanupProject(ctx context.Context, project config.Project, opts CleanupOptions, active map[string][]string) (CleanupProject, error) {
	worktrees, err := git.ListWorktrees(ctx, project.Path)
	if err != nil {
		return CleanupProject{}, err
	}
	base, err := git.DefaultBranch(ctx, project.Path)
	if err != nil {
		return CleanupProject{}, err
	}
	branches, err := git.ListBranches(ctx, project.Path)
	if err != nil {
		return CleanupProject{}, err
	}

	planned := CleanupProject{
		Project:       project.Name,
		Path:          project.Path,
		DefaultBranch: base,
		Items:         make([]CleanupItem, 0, len(worktrees)+len(branches)),
	}
	mainPath := ""
	if len(worktrees) > 0 {
		mainPath = worktrees[0].Path
	}

	checkedOut := make(map[string]bool, len(worktrees))
	for _, worktree := range worktrees {
		if worktree.Bare {
			continue
		}
		if worktree.Branch != "" {
			checkedOut[worktree.Branch] = true
		}
		planned.Items = append(planned.Items, planCleanupWorktree(ctx, project, worktree, mainPath, base, active, opts))
	}
	for _, branch := range branches {
		if checkedOut[branch] {
			continue
		}
		planned.Items = append(planned.Items, planCleanupBranch(ctx, project, branch, base, active, opts))
	}
	sortCleanupItems(planned.Items)
	return planned, nil
}

func planCleanupWorktree(ctx context.Context, project config.Project, worktree git.Worktree, mainPath, base string, active map[string][]string, opts CleanupOptions) CleanupItem {
	item := CleanupItem{
		Project:  project.Name,
		Kind:     "worktree",
		Path:     worktree.Path,
		Head:     worktree.Head,
		Branch:   worktree.Branch,
		Locked:   worktree.Locked,
		Detached: worktree.Detached,
		Action:   CleanupActionNone,
	}

	missing, inspectErr := worktreeMissing(worktree.Path)
	var dirtyFiles []string
	statusErr := inspectErr
	if !missing && inspectErr == nil {
		dirtyFiles, statusErr = git.StatusLinesIncludingIgnored(ctx, worktree.Path)
	}
	item.Dirty = statusErr != nil || len(dirtyFiles) > 0

	sessionBranch := worktree.Branch
	if sessionBranch == "" {
		sessionBranch = worktree.ShortHead()
	}
	if ids := active[project.Name+"/"+sessionBranch]; len(ids) > 0 {
		item.ActiveSession = true
		item.SessionID = ids[0]
		item.sessionIDs = ids
	}

	var branchState CleanupBranchState
	var commits []git.Commit
	var proofErr error
	if worktree.Branch != "" {
		branchState, proofErr = inspectCleanupBranch(ctx, project.Path, base, worktree.Branch)
		if branchState == CleanupBranchUnmerged || item.Dirty {
			var commitErr error
			commits, commitErr = git.CommitsOutside(ctx, project.Path, base, worktree.Branch)
			if commitErr != nil && proofErr == nil {
				proofErr = commitErr
			}
		}
		if proofErr == nil {
			item.BranchState = branchState
		}
	}

	switch {
	case worktree.Path == mainPath:
		item.Classification = CleanupProtected
	case worktree.Locked:
		item.Classification = CleanupLocked
	case worktree.Prunable || missing:
		item.Classification = CleanupPrunable
		item.Safe = worktree.Prunable && missing && inspectErr == nil
		item.Removable = item.Safe
		if item.Safe {
			item.Action = CleanupActionPrune
		}
	case worktree.Detached:
		item.Classification = CleanupDetached
	case worktree.Branch == "":
		item.Classification = CleanupUnknown
	case protectedCleanupBranch(worktree.Branch, base):
		item.Classification = CleanupProtected
	case item.ActiveSession:
		item.Classification = CleanupActive
	case proofErr != nil:
		item.Classification = CleanupUnknown
	case item.Dirty:
		item.Classification = CleanupDirty
	default:
		item.Classification = classificationForBranchState(branchState)
	}

	blocked := item.Classification.blocksRemoval()
	if !blocked && item.Classification != CleanupPrunable {
		item.RequiresForce = item.Dirty || branchState == CleanupBranchUnmerged
		item.Safe = !item.Dirty && (branchState == CleanupBranchMerged || branchState == CleanupBranchPatchEquivalent)
		item.Removable = item.Safe || (opts.Force && (item.Dirty || branchState == CleanupBranchUnmerged))
		if item.Removable {
			item.Action = cleanupWorktreeAction(worktree.Branch)
		}
	}

	if !blocked && item.Classification != CleanupPrunable && (item.Dirty || branchState == CleanupBranchUnmerged) {
		loss := CleanupLoss{DirtyFiles: nonNilStrings(dirtyFiles), Commits: nonNilCommits(commits)}
		item.Loss = &loss
	}
	item.Detail = cleanupWorktreeDetail(item, mainPath, base, len(commits), len(dirtyFiles), missing, worktree.Prunable, statusErr, proofErr)
	return item
}

func planCleanupBranch(ctx context.Context, project config.Project, branch, base string, active map[string][]string, opts CleanupOptions) CleanupItem {
	item := CleanupItem{
		Project: project.Name,
		Kind:    "branch",
		Branch:  branch,
		Action:  CleanupActionNone,
	}
	if head, err := git.BranchSHA(ctx, project.Path, branch); err == nil {
		item.Head = head
	}
	if ids := active[project.Name+"/"+branch]; len(ids) > 0 {
		item.ActiveSession = true
		item.SessionID = ids[0]
		item.sessionIDs = ids
	}

	state, proofErr := inspectCleanupBranch(ctx, project.Path, base, branch)
	var commits []git.Commit
	if state == CleanupBranchUnmerged {
		var commitErr error
		commits, commitErr = git.CommitsOutside(ctx, project.Path, base, branch)
		if commitErr != nil && proofErr == nil {
			proofErr = commitErr
		}
	}
	if proofErr == nil {
		item.BranchState = state
	}

	switch {
	case protectedCleanupBranch(branch, base):
		item.Classification = CleanupProtected
	case item.ActiveSession:
		item.Classification = CleanupActive
	case proofErr != nil:
		item.Classification = CleanupUnknown
	default:
		item.Classification = classificationForBranchState(state)
	}

	if !item.Classification.blocksRemoval() {
		item.RequiresForce = state == CleanupBranchUnmerged
		item.Safe = state == CleanupBranchMerged || state == CleanupBranchPatchEquivalent
		item.Removable = item.Safe || (opts.Force && state == CleanupBranchUnmerged)
		if item.Removable {
			item.Action = CleanupActionDeleteBranch
		}
		if state == CleanupBranchUnmerged && len(commits) > 0 {
			item.Loss = &CleanupLoss{DirtyFiles: []string{}, Commits: nonNilCommits(commits)}
		}
	}
	item.Detail = cleanupBranchDetail(item, base, len(commits), proofErr)
	return item
}

func inspectCleanupBranch(ctx context.Context, repoPath, base, branch string) (CleanupBranchState, error) {
	merged, err := git.IsAncestor(ctx, repoPath, branch, base)
	if err != nil {
		return CleanupBranchUnmerged, err
	}
	if merged {
		return CleanupBranchMerged, nil
	}
	patchEquivalent, err := git.PatchEquivalent(ctx, repoPath, base, branch)
	if err != nil {
		return CleanupBranchUnmerged, err
	}
	if patchEquivalent {
		return CleanupBranchPatchEquivalent, nil
	}
	return CleanupBranchUnmerged, nil
}

func classificationForBranchState(state CleanupBranchState) CleanupClassification {
	switch state {
	case CleanupBranchMerged:
		return CleanupMerged
	case CleanupBranchPatchEquivalent:
		return CleanupPatchEquivalent
	default:
		return CleanupUnmerged
	}
}

func cleanupWorktreeAction(branch string) CleanupAction {
	if branch == "" {
		return CleanupActionRemoveWorktree
	}
	return CleanupActionRemoveWorktreeAndBranch
}

func (c CleanupClassification) blocksRemoval() bool {
	switch c {
	case CleanupProtected, CleanupLocked, CleanupDetached, CleanupActive, CleanupUnknown:
		return true
	default:
		return false
	}
}

// protectedCleanupBranch follows git-cleanbranches: main, master, develop, and
// HEAD are never deleted. The project's own default branch is protected too,
// including an origin/HEAD-named branch such as trunk.
func protectedCleanupBranch(branch, base string) bool {
	if branch == "" {
		return false
	}
	switch branch {
	case "main", "master", "develop", "HEAD":
		return true
	}
	if branch == base {
		return true
	}
	return strings.HasPrefix(base, "origin/") && branch == strings.TrimPrefix(base, "origin/")
}

func worktreeMissing(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func cleanupWorktreeDetail(item CleanupItem, mainPath, base string, commitCount, dirtyCount int, missing, prunable bool, statusErr, proofErr error) string {
	var parts []string
	switch item.Classification {
	case CleanupPrunable:
		switch {
		case item.Safe:
			parts = append(parts, "worktree directory is missing; Git marks it prunable")
		case prunable && !missing:
			parts = append(parts, "Git marks the worktree prunable but its directory still exists; refusing to prune automatically")
		case !prunable:
			parts = append(parts, "worktree directory is missing but Git did not mark it prunable; run git worktree prune")
		default:
			parts = append(parts, "worktree is prunable but is not eligible for automatic pruning")
		}
	case CleanupProtected:
		if item.Path == mainPath {
			parts = append(parts, "main worktree")
		} else {
			parts = append(parts, "protected branch "+item.Branch)
		}
	case CleanupLocked:
		parts = append(parts, "worktree is locked")
	case CleanupDetached:
		parts = append(parts, "detached HEAD "+shortCleanupSHA(item.Head))
	case CleanupActive:
		parts = append(parts, sessionDescription(item.sessionIDs, item.SessionID))
	case CleanupDirty:
		parts = append(parts, "uncommitted, untracked, or ignored changes: "+dirtyDescription(dirtyCount, statusErr))
	case CleanupUnknown:
		if proofErr != nil {
			parts = append(parts, "branch check failed; refusing to remove")
		} else {
			parts = append(parts, "branch state is unknown")
		}
	default:
		parts = append(parts, branchStateDescription(item.BranchState, base, commitCount))
	}
	if item.Classification != CleanupLocked && item.Locked {
		parts = append(parts, "locked")
	}
	if item.Classification != CleanupDetached && item.Detached {
		parts = append(parts, "detached HEAD "+shortCleanupSHA(item.Head))
	}
	if item.Classification != CleanupActive && item.ActiveSession {
		parts = append(parts, sessionDescription(item.sessionIDs, item.SessionID))
	}
	if item.Classification != CleanupDirty && item.Dirty {
		parts = append(parts, "dirty: "+dirtyDescription(dirtyCount, statusErr))
	}
	if item.Classification != CleanupMerged && item.Classification != CleanupPatchEquivalent &&
		item.Classification != CleanupUnmerged && item.Classification != CleanupUnknown &&
		item.BranchState != "" {
		parts = append(parts, branchStateDescription(item.BranchState, base, commitCount))
	}
	if proofErr != nil {
		parts = append(parts, "branch check failed: "+proofErr.Error())
	}
	return strings.Join(parts, "; ")
}

func cleanupBranchDetail(item CleanupItem, base string, commitCount int, proofErr error) string {
	var parts []string
	switch item.Classification {
	case CleanupProtected:
		parts = append(parts, "protected branch "+item.Branch)
		if item.BranchState != "" {
			parts = append(parts, branchStateDescription(item.BranchState, base, commitCount))
		}
	case CleanupActive:
		parts = append(parts, sessionDescription(item.sessionIDs, item.SessionID))
		if item.BranchState != "" {
			parts = append(parts, branchStateDescription(item.BranchState, base, commitCount))
		}
	case CleanupUnknown:
		if proofErr != nil {
			parts = append(parts, "branch check failed; refusing to remove")
		} else {
			parts = append(parts, "branch state is unknown")
		}
	default:
		parts = append(parts, branchStateDescription(item.BranchState, base, commitCount))
	}
	if item.Classification != CleanupActive && item.ActiveSession {
		parts = append(parts, sessionDescription(item.sessionIDs, item.SessionID))
	}
	if proofErr != nil {
		parts = append(parts, "branch check failed: "+proofErr.Error())
	}
	return strings.Join(parts, "; ")
}

func branchStateDescription(state CleanupBranchState, base string, commitCount int) string {
	switch state {
	case CleanupBranchMerged:
		return "branch is an ancestor of " + base
	case CleanupBranchPatchEquivalent:
		return "all branch commits are patch-equivalent to " + base
	case CleanupBranchUnmerged:
		if commitCount == 1 {
			return "1 commit not in " + base
		}
		return fmt.Sprintf("%d commits not in %s", commitCount, base)
	default:
		return "branch state is unknown"
	}
}

func sessionDescription(ids []string, fallback string) string {
	switch len(ids) {
	case 0:
		if fallback == "" {
			return "live Rex session"
		}
		return "live Rex session " + fallback
	case 1:
		return "live Rex session " + ids[0]
	default:
		return "live Rex sessions " + strings.Join(ids, ", ")
	}
}

func dirtyDescription(count int, statusErr error) string {
	if statusErr != nil {
		return "worktree status could not be read: " + statusErr.Error()
	}
	if count == 1 {
		return "1 changed, untracked, or ignored path"
	}
	return fmt.Sprintf("%d changed, untracked, or ignored paths", count)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilCommits(values []git.Commit) []git.Commit {
	if values == nil {
		return []git.Commit{}
	}
	return values
}

func shortCleanupSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func sortCleanupItems(items []CleanupItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		leftName, rightName := left.Branch, right.Branch
		if leftName == "" {
			leftName = left.Head
		}
		if rightName == "" {
			rightName = right.Head
		}
		if leftName != rightName {
			return leftName < rightName
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Path < right.Path
	})
}

func newClassificationCounts() map[string]int {
	counts := map[string]int{
		string(CleanupPrunable):        0,
		string(CleanupMerged):          0,
		string(CleanupPatchEquivalent): 0,
		string(CleanupUnmerged):        0,
		string(CleanupDirty):           0,
		string(CleanupProtected):       0,
		string(CleanupLocked):          0,
		string(CleanupDetached):        0,
		string(CleanupActive):          0,
		string(CleanupUnknown):         0,
	}
	return counts
}

func recountCleanup(report *CleanupReport, preview bool) {
	totals := CleanupTotals{ByClassification: newClassificationCounts()}
	for _, project := range report.Projects {
		for _, item := range project.Items {
			totals.Scanned++
			totals.ByClassification[string(item.Classification)]++
			if item.Safe {
				totals.Safe++
			}
			if item.Removable {
				totals.Removable++
			}
			if preview {
				if !item.Removable {
					totals.Kept++
				}
				continue
			}
			switch item.Result {
			case CleanupResultRemoved:
				totals.Removed++
			case CleanupResultFailed:
				totals.Failed++
			default:
				totals.Kept++
			}
		}
	}
	report.Totals = totals
}

func activeSessions(ctx context.Context, cfg config.Config) (map[string][]string, error) {
	sessions, err := rex.New(cfg.RexBinary()).ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	byLabel := make(map[string][]string, len(sessions))
	for _, session := range sessions {
		byLabel[session.Label] = append(byLabel[session.Label], session.ID)
	}
	return byLabel, nil
}

func clearWorktreeState(cfg config.Config, paths []string) error {
	state, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		return err
	}
	changed := false
	for _, path := range paths {
		if _, ok := state.Workstreams[path]; !ok {
			continue
		}
		delete(state.Workstreams, path)
		changed = true
	}
	if !changed {
		return nil
	}
	if err := state.Save(); err != nil {
		return fmt.Errorf("clear cleanup state: %w", err)
	}
	return nil
}
