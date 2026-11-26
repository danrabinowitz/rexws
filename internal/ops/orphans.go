package ops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/rex"
)

// CleanupOrphanKind is the inconsistency an orphan item reports. It is the
// stable value scripts can switch on.
type CleanupOrphanKind string

const (
	// CleanupOrphanUnmatchedSession is a live Rex session whose label matches
	// no worktree in the registered projects.
	CleanupOrphanUnmatchedSession CleanupOrphanKind = "unmatched-session"
	// CleanupOrphanStaleState is a state.json entry whose path is not a
	// current Git worktree.
	CleanupOrphanStaleState CleanupOrphanKind = "stale-state"
	// CleanupOrphanUnregisteredWorktree is a worktree inside a registered
	// project's checkout that is outside that project's worktree_root.
	CleanupOrphanUnregisteredWorktree CleanupOrphanKind = "unregistered-worktree"
	// CleanupOrphanSessionLabelConflict is a duplicate session label or a
	// label that matches more than one worktree.
	CleanupOrphanSessionLabelConflict CleanupOrphanKind = "session-label-conflict"
)

// CleanupOrphan is one reported inconsistency. Every field is always present
// in JSON: empty values are "", false, or an empty array. Fixable is true only
// for a stale state entry that cleanup --fix can prove names nothing. Result
// is empty in a preview and kept, removed, or failed after a fix pass.
type CleanupOrphan struct {
	Kind       CleanupOrphanKind `json:"kind"`
	Project    string            `json:"project"`
	Label      string            `json:"label"`
	Path       string            `json:"path"`
	SessionIDs []string          `json:"session_ids"`
	Detail     string            `json:"detail"`
	Fixable    bool              `json:"fixable"`
	Result     CleanupResult     `json:"result"`
	Error      string            `json:"error"`
}

// CleanupOrphanTotals summarizes the orphan section. ByKind always carries
// every kind, including zero counts, so the JSON shape is fixed.
type CleanupOrphanTotals struct {
	Scanned int            `json:"scanned"`
	Fixable int            `json:"fixable"`
	Removed int            `json:"removed"`
	Failed  int            `json:"failed"`
	Kept    int            `json:"kept"`
	ByKind  map[string]int `json:"by_kind"`
}

// CleanupOrphans is the orphan section of the cleanup report. Fixed is true
// after an authorized --fix pass, including a zero-candidate no-op. Complete
// is true only when stale-state proof covered every registered project and
// state.json loaded; false means stale entries were not examined.
type CleanupOrphans struct {
	Fixed    bool                `json:"fixed"`
	Complete bool                `json:"complete"`
	Items    []CleanupOrphan     `json:"items"`
	Totals   CleanupOrphanTotals `json:"totals"`
}

// cleanupOrphanProject is one project's worktrees. Listed records whether Git
// returned the worktree list; a classification failure after that keeps the
// worktrees available for session matching and unregistered-worktree reports.
// Err is the project-planning error, if any.
type cleanupOrphanProject struct {
	Project   config.Project
	Worktrees []git.Worktree
	Listed    bool
	Err       error
}

type cleanupWorktreeIdentity struct {
	project string
	path    string
	label   string
}

func emptyCleanupOrphans() CleanupOrphans {
	return CleanupOrphans{
		Items:  []CleanupOrphan{},
		Totals: CleanupOrphanTotals{ByKind: newCleanupOrphanKindCounts()},
	}
}

func newCleanupOrphanKindCounts() map[string]int {
	return map[string]int{
		string(CleanupOrphanUnmatchedSession):     0,
		string(CleanupOrphanStaleState):           0,
		string(CleanupOrphanUnregisteredWorktree): 0,
		string(CleanupOrphanSessionLabelConflict): 0,
	}
}

func listCleanupSessions(ctx context.Context, cfg config.Config) ([]rex.Session, error) {
	return rex.New(cfg.RexBinary()).ListSessions(ctx)
}

func sessionsByLabel(sessions []rex.Session) map[string][]string {
	byLabel := make(map[string][]string, len(sessions))
	for _, session := range sessions {
		byLabel[session.Label] = append(byLabel[session.Label], session.ID)
	}
	return byLabel
}

// cleanupOrphanProofScans returns worktree lists for every registered project,
// reusing the selected project scans. The orphan state proof needs every
// project, even under --project, so preview fixability matches what
// ApplyCleanupFix re-lists before pruning.
func cleanupOrphanProofScans(ctx context.Context, cfg config.Config, selected []cleanupOrphanProject) []cleanupOrphanProject {
	selectedByName := make(map[string]cleanupOrphanProject, len(selected))
	for _, scan := range selected {
		selectedByName[scan.Project.Name] = scan
	}
	proof := make([]cleanupOrphanProject, 0, len(cfg.Projects))
	for _, project := range cfg.SortedProjects() {
		if scan, ok := selectedByName[project.Name]; ok {
			proof = append(proof, scan)
			continue
		}
		worktrees, err := git.ListWorktrees(ctx, project.Path)
		proof = append(proof, cleanupOrphanProject{
			Project:   project,
			Worktrees: worktrees,
			Listed:    err == nil,
			Err:       err,
		})
	}
	return proof
}

// scanCleanupOrphans reports every inconsistency it can prove from the selected
// project scans, all-project worktree proof, the live Rex sessions, and
// state.json. It never mutates anything. Warnings explain data it could not
// check; errors are reserved for state that could not be read during a fix
// request.
func scanCleanupOrphans(ctx context.Context, cfg config.Config, reportScans, proofScans []cleanupOrphanProject, sessions []rex.Session, sessionsOK, narrowed, fixRequested bool) (CleanupOrphans, []string, []string) {
	orphans := emptyCleanupOrphans()
	var warnings, errs []string

	byLabel := make(map[string][]cleanupWorktreeIdentity)
	failedSessionProjects := make(map[string]bool)
	var failedSessionNames []string
	anySelectedListed := false
	for _, scan := range reportScans {
		if !scan.Listed {
			if !failedSessionProjects[scan.Project.Name] {
				failedSessionProjects[scan.Project.Name] = true
				failedSessionNames = append(failedSessionNames, scan.Project.Name)
			}
			continue
		}
		anySelectedListed = true
		for _, worktree := range scan.Worktrees {
			if worktree.Bare {
				continue
			}
			branch := worktree.Branch
			if branch == "" {
				branch = worktree.ShortHead()
			}
			identity := cleanupWorktreeIdentity{
				project: scan.Project.Name,
				path:    worktree.Path,
				label:   scan.Project.Name + "/" + branch,
			}
			byLabel[identity.label] = append(byLabel[identity.label], identity)
		}
	}

	knownWorktrees := make(map[string]bool)
	proofReadable := true
	var unreadableProof []string
	for _, scan := range proofScans {
		if !scan.Listed {
			proofReadable = false
			unreadableProof = append(unreadableProof, scan.Project.Name)
			continue
		}
		for _, worktree := range scan.Worktrees {
			knownWorktrees[cleanupComparablePath(worktree.Path)] = true
		}
	}
	if !proofReadable {
		warnings = append(warnings, fmt.Sprintf(
			"stale state.json entries were not checked because project(s) %s could not be read",
			strings.Join(unreadableProof, ", ")))
	}
	if len(failedSessionNames) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Rex sessions for project(s) %s were not checked because their worktrees could not be listed",
			strings.Join(failedSessionNames, ", ")))
	}

	if len(cfg.Projects) == 0 {
		warnings = append(warnings, "Rex sessions were not checked for orphan labels because no projects are registered")
	} else if anySelectedListed {
		sessionIDs := make(map[string][]string)
		if sessionsOK {
			sessionIDs = sessionsByLabel(sessions)
		}
		labels := make([]string, 0, len(sessionIDs)+len(byLabel))
		seen := make(map[string]bool, len(sessionIDs)+len(byLabel))
		for label := range sessionIDs {
			if !seen[label] {
				seen[label] = true
				labels = append(labels, label)
			}
		}
		for label := range byLabel {
			if !seen[label] {
				seen[label] = true
				labels = append(labels, label)
			}
		}
		sort.Strings(labels)
		for _, label := range labels {
			project := cleanupOrphanLabelProjectName(label, cfg)
			if project == "" || failedSessionProjects[project] {
				continue
			}
			if !cleanupOrphanLabelInScope(label, cfg, reportScans, narrowed) {
				continue
			}
			ids := sortedStrings(sessionIDs[label])
			matches := byLabel[label]
			if sessionsOK && len(ids) > 0 && len(matches) == 0 {
				orphans.Items = append(orphans.Items, CleanupOrphan{
					Kind:       CleanupOrphanUnmatchedSession,
					Project:    cleanupOrphanLabelProjectName(label, cfg),
					Label:      label,
					SessionIDs: ids,
					Detail:     unmatchedSessionDetail(label, ids),
				})
			}
			if len(ids) > 1 || len(matches) > 1 {
				orphans.Items = append(orphans.Items, CleanupOrphan{
					Kind:       CleanupOrphanSessionLabelConflict,
					Project:    cleanupOrphanLabelProjectName(label, cfg),
					Label:      label,
					Path:       singleMatchPath(matches),
					SessionIDs: ids,
					Detail:     sessionLabelConflictDetail(label, ids, matches),
				})
			}
		}
	}

	orphans.Items = append(orphans.Items, cleanupUnregisteredWorktrees(reportScans)...)
	staleItems, complete := cleanupStaleState(
		ctx, cfg, reportScans, knownWorktrees, proofReadable, narrowed, fixRequested, &warnings, &errs)
	orphans.Items = append(orphans.Items, staleItems...)
	orphans.Complete = complete

	sortCleanupOrphans(orphans.Items)
	recountCleanupOrphans(&orphans)
	return orphans, warnings, errs
}

func cleanupUnregisteredWorktrees(scans []cleanupOrphanProject) []CleanupOrphan {
	var items []CleanupOrphan
	for _, scan := range scans {
		if !scan.Listed || scan.Project.WorktreeRoot == "" {
			// Without a configured root there is no convention to violate.
			continue
		}
		projectPath := cleanupComparablePath(scan.Project.Path)
		root := cleanupComparablePath(scan.Project.WorktreeRoot)
		for _, worktree := range scan.Worktrees {
			if worktree.Bare || worktree.Prunable {
				continue
			}
			path := cleanupComparablePath(worktree.Path)
			if path == projectPath || !cleanupPathWithin(path, projectPath) {
				continue
			}
			if cleanupPathWithin(path, root) {
				continue
			}
			branch := worktree.Branch
			if branch == "" {
				branch = worktree.ShortHead()
			}
			items = append(items, CleanupOrphan{
				Kind:       CleanupOrphanUnregisteredWorktree,
				Project:    scan.Project.Name,
				Label:      scan.Project.Name + "/" + branch,
				Path:       worktree.Path,
				SessionIDs: []string{},
				Detail:     "worktree is inside the project checkout but outside its configured worktree_root; rexws did not register it",
			})
		}
	}
	return items
}

func cleanupStaleState(ctx context.Context, cfg config.Config, scans []cleanupOrphanProject, knownWorktrees map[string]bool, proofReadable, narrowed, fixRequested bool, warnings, errs *[]string) ([]CleanupOrphan, bool) {
	if len(cfg.Projects) == 0 {
		*warnings = append(*warnings, "stale state.json entries were not checked because no projects are registered")
		return nil, false
	}
	if !proofReadable {
		// scanCleanupOrphans already explained the incomplete proof.
		return nil, false
	}

	state, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		message := fmt.Sprintf("state.json: %v; stale state entries were not checked", err)
		if fixRequested {
			*errs = append(*errs, message)
		} else {
			*warnings = append(*warnings, message)
		}
		return nil, false
	}

	keys := make([]string, 0, len(state.Workstreams))
	for key := range state.Workstreams {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	complete := true
	var items []CleanupOrphan
	for _, key := range keys {
		if !cleanupOrphanStateInScope(key, scans, narrowed) {
			continue
		}
		if knownWorktrees[cleanupComparablePath(key)] {
			// A prunable worktree is still a Git worktree registration;
			// cleanup --apply owns pruning it and clearing its state.
			continue
		}
		current, proofErr := cleanupWorktreeAtPath(ctx, key)
		if proofErr != nil {
			complete = false
			*warnings = append(*warnings, fmt.Sprintf("state entry %s could not be checked: %v; kept", key, proofErr))
			continue
		}
		if current {
			continue
		}
		missing, statErr := cleanupPathMissing(key)
		if statErr != nil {
			complete = false
			*warnings = append(*warnings, fmt.Sprintf("state entry %s could not be checked: %v; kept", key, statErr))
			continue
		}
		fixable := missing && filepath.IsAbs(key)
		items = append(items, CleanupOrphan{
			Kind:       CleanupOrphanStaleState,
			Project:    cleanupProjectForPath(key, scans),
			Path:       key,
			SessionIDs: []string{},
			Detail:     staleStateDetail(missing, filepath.IsAbs(key)),
			Fixable:    fixable,
		})
	}
	return items, complete
}

func cleanupWorktreeAtPath(ctx context.Context, path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	top, err := git.TopLevel(ctx, path)
	if err != nil {
		// Git's own "not a repository" and bare-repository messages prove the
		// path is not a worktree. Anything else is indeterminate.
		if strings.Contains(err.Error(), "not a git repository") ||
			strings.Contains(err.Error(), "must be run in a work tree") {
			return false, nil
		}
		return false, err
	}
	if topInfo, topErr := os.Stat(top); topErr == nil {
		if pathInfo, pathErr := os.Stat(path); pathErr == nil {
			// SameFile follows case-insensitive and symlinked spellings.
			return os.SameFile(topInfo, pathInfo), nil
		}
	}
	return cleanupComparablePath(top) == cleanupComparablePath(path), nil
}

func cleanupPathMissing(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func staleStateDetail(missing, absolute bool) string {
	switch {
	case missing && !absolute:
		return "state.json entry points at a path that no longer exists, but the key is not absolute; rexws keeps the entry"
	case missing:
		return "state.json entry points at a path that no longer exists and is not a current Git worktree"
	case !absolute:
		return "state.json entry is not a current Git worktree, and the key is not absolute; rexws keeps the entry"
	default:
		return "state.json entry is not a current Git worktree; the path still exists, so rexws keeps the entry"
	}
}

func unmatchedSessionDetail(label string, ids []string) string {
	if len(ids) == 1 {
		return fmt.Sprintf("live Rex session %s uses label %q, which matches no worktree in the registered projects", ids[0], label)
	}
	return fmt.Sprintf("%d live Rex sessions use label %q, which matches no worktree in the registered projects (sessions: %s)",
		len(ids), label, strings.Join(ids, ", "))
}

func sessionLabelConflictDetail(label string, ids []string, matches []cleanupWorktreeIdentity) string {
	var parts []string
	switch len(ids) {
	case 0:
		parts = append(parts, "no live Rex session uses the label")
	case 1:
		parts = append(parts, "one live Rex session uses the label")
	default:
		parts = append(parts, fmt.Sprintf("%d live Rex sessions share the label", len(ids)))
	}
	switch len(matches) {
	case 0:
		parts = append(parts, "the label matches no worktree")
	case 1:
		parts = append(parts, "the label matches worktree "+matches[0].path)
	default:
		paths := make([]string, 0, len(matches))
		for _, match := range matches {
			paths = append(paths, match.path)
		}
		parts = append(parts, fmt.Sprintf("the label matches %d worktrees (%s)", len(matches), strings.Join(paths, ", ")))
	}
	parts = append(parts, "rexws never renames or closes Rex sessions")
	detail := fmt.Sprintf("%s %q; %s", parts[0], label, strings.Join(parts[1:], "; "))
	if len(ids) > 0 {
		detail += fmt.Sprintf(" (sessions: %s)", strings.Join(ids, ", "))
	}
	return detail
}

func singleMatchPath(matches []cleanupWorktreeIdentity) string {
	if len(matches) == 1 {
		return matches[0].path
	}
	return ""
}

func cleanupOrphanLabelInScope(label string, cfg config.Config, scans []cleanupOrphanProject, narrowed bool) bool {
	project := cleanupOrphanLabelProjectName(label, cfg)
	if project == "" {
		// A hand-named session is not a rexws-managed workstream.
		return false
	}
	if !narrowed {
		return true
	}
	for _, scan := range scans {
		if scan.Project.Name == project {
			return true
		}
	}
	return false
}

func cleanupOrphanStateInScope(path string, scans []cleanupOrphanProject, narrowed bool) bool {
	if !narrowed {
		return true
	}
	return cleanupProjectForPath(path, scans) != ""
}

func cleanupProjectForPath(path string, scans []cleanupOrphanProject) string {
	comparable := cleanupComparablePath(path)
	for _, scan := range scans {
		if cleanupPathWithin(comparable, cleanupComparablePath(scan.Project.Path)) {
			return scan.Project.Name
		}
		if scan.Project.WorktreeRoot != "" && cleanupPathWithin(comparable, cleanupComparablePath(scan.Project.WorktreeRoot)) {
			return scan.Project.Name
		}
	}
	return ""
}

// cleanupOrphanLabelProjectName resolves a label's first segment to a
// registered project. Labels for unmanaged sessions return "" and are ignored.
func cleanupOrphanLabelProjectName(label string, cfg config.Config) string {
	name, _, ok := strings.Cut(label, "/")
	if !ok {
		return ""
	}
	if _, found := cfg.Project(name); !found {
		return ""
	}
	return name
}

// cleanupComparablePath makes two spellings of the same worktree compare
// equal, even when the leaf path is gone. It resolves the deepest existing
// ancestor and rejoins the missing suffix, so a deleted nested worktree under
// a symlinked temp directory still compares with its configured root.
func cleanupComparablePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	missing := make([]string, 0)
	current := abs
	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		missing = append(missing, filepath.Base(current))
		current = parent
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved
		}
	}
	return filepath.Clean(abs)
}

func cleanupPathWithin(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func sortedStrings(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return out
}

func sortCleanupOrphans(items []CleanupOrphan) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.Kind != right.Kind {
			return cleanupOrphanKindOrder(left.Kind) < cleanupOrphanKindOrder(right.Kind)
		}
		if left.Project != right.Project {
			return left.Project < right.Project
		}
		if left.Label != right.Label {
			return left.Label < right.Label
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return strings.Join(left.SessionIDs, ",") < strings.Join(right.SessionIDs, ",")
	})
}

func cleanupOrphanKindOrder(kind CleanupOrphanKind) int {
	switch kind {
	case CleanupOrphanUnmatchedSession:
		return 0
	case CleanupOrphanStaleState:
		return 1
	case CleanupOrphanUnregisteredWorktree:
		return 2
	case CleanupOrphanSessionLabelConflict:
		return 3
	default:
		return 4
	}
}

func recountCleanupOrphans(orphans *CleanupOrphans) {
	totals := CleanupOrphanTotals{ByKind: newCleanupOrphanKindCounts()}
	for _, item := range orphans.Items {
		totals.Scanned++
		totals.ByKind[string(item.Kind)]++
		if item.Fixable {
			totals.Fixable++
		}
		switch item.Result {
		case CleanupResultRemoved:
			totals.Removed++
		case CleanupResultFailed:
			totals.Failed++
		case CleanupResultKept:
			totals.Kept++
		}
	}
	orphans.Totals = totals
}

func hasFixableOrphans(report CleanupReport) bool {
	for _, item := range report.Orphans.Items {
		if item.Kind == CleanupOrphanStaleState && item.Fixable {
			return true
		}
	}
	return false
}

// ApplyCleanupFix prunes only stale state.json entries the plan marked
// fixable. A fix pass with no candidates is a completed no-op: it marks the
// section fixed, keeps every report item, and validates that state.json is
// readable. Otherwise it re-lists every registered project's worktrees and
// re-proves each target before changing the file: the entry must still exist,
// the path must still be absent, and the path must not have become a current
// worktree. Everything else is retained and reported.
func ApplyCleanupFix(ctx context.Context, cfg config.Config, report CleanupReport) (CleanupReport, error) {
	if !hasFixableOrphans(report) {
		if _, err := config.LoadState(cfg.StatusIDs()); err != nil {
			return report, fmt.Errorf("reload state.json: %w", err)
		}
		report.Orphans.Fixed = true
		for i := range report.Orphans.Items {
			report.Orphans.Items[i].Result = CleanupResultKept
		}
		recountCleanupOrphans(&report.Orphans)
		return report, nil
	}

	knownWorktrees := make(map[string]bool)
	for _, project := range cfg.SortedProjects() {
		worktrees, err := git.ListWorktrees(ctx, project.Path)
		if err != nil {
			return report, fmt.Errorf("cannot re-list project %s before pruning state: %w; refusing to change anything", project.Name, err)
		}
		for _, worktree := range worktrees {
			knownWorktrees[cleanupComparablePath(worktree.Path)] = true
		}
	}

	state, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		return report, fmt.Errorf("reload state.json: %w", err)
	}

	// A fixable target means the plan's stale proof was complete; the re-list
	// and reload above cover every project again.
	report.Orphans.Complete = true
	report.Orphans.Fixed = true
	removed := make([]int, 0)
	for i := range report.Orphans.Items {
		item := &report.Orphans.Items[i]
		item.Result = CleanupResultKept
		if item.Kind != CleanupOrphanStaleState || !item.Fixable {
			continue
		}
		if !filepath.IsAbs(item.Path) {
			item.Result = CleanupResultFailed
			item.Error = "state key is not absolute; entry kept"
			continue
		}
		if _, ok := state.Workstreams[item.Path]; !ok {
			item.Error = "state entry disappeared before fixing; nothing to remove"
			continue
		}
		if _, err := os.Lstat(item.Path); err == nil {
			item.Result = CleanupResultFailed
			item.Error = "path exists again; entry kept"
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			item.Result = CleanupResultFailed
			item.Error = fmt.Sprintf("re-check path: %v; entry kept", err)
			continue
		}
		if knownWorktrees[cleanupComparablePath(item.Path)] {
			item.Result = CleanupResultFailed
			item.Error = "path names a current Git worktree again; entry kept"
			continue
		}
		delete(state.Workstreams, item.Path)
		item.Result = CleanupResultRemoved
		removed = append(removed, i)
	}

	if len(removed) > 0 {
		if err := state.Save(); err != nil {
			for _, i := range removed {
				report.Orphans.Items[i].Result = CleanupResultFailed
				report.Orphans.Items[i].Error = fmt.Sprintf("save state.json: %v", err)
			}
			recountCleanupOrphans(&report.Orphans)
			return report, fmt.Errorf("save state.json: %w", err)
		}
	}

	recountCleanupOrphans(&report.Orphans)
	return report, nil
}
