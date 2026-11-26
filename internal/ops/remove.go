package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
	"github.com/danrabinowitz/rexws/internal/provider"
	"github.com/danrabinowitz/rexws/internal/rex"
)

// RemoveOptions controls what a removal may discard. The zero value keeps the
// historical behaviour: a dirty worktree is refused and the branch is retained.
type RemoveOptions struct {
	// Force permits removing a dirty worktree after the loss is reported.
	Force bool
	// DeleteBranch deletes the branch after the worktree is removed, but only
	// when it is merged or patch-equivalent to the project's default branch.
	DeleteBranch bool
}

// RemoveLoss names the consequences of a removal. DirtyFiles and
// DetachedCommits are truly discarded: the worktree directory is deleted, and
// a detached HEAD's unique commits lose their only ref. Unpushed and Unmerged
// describe a retained branch and are reported separately. AssessmentError is
// set when force was used but the branch or detached HEAD could not be
// assessed; it is treated conservatively as a loss to confirm.
type RemoveLoss struct {
	DirtyFiles      []string     `json:"dirty_files"`
	Detached        bool         `json:"detached"`
	DetachedCommits []git.Commit `json:"detached_commits"`
	AssessmentError string       `json:"assessment_error,omitempty"`

	// Retained branch facts. They are not losses and are serialized separately
	// through RemoveRetained so JSON cannot confuse the two.
	Unpushed      []git.Commit       `json:"-"`
	Unmerged      []git.Commit       `json:"-"`
	BranchState   CleanupBranchState `json:"-"`
	DefaultBranch string             `json:"-"`
}

// RemoveRetained is the plan-time retained-branch report: what the branch keeps
// even though the worktree is removed. It is separate from Loss on purpose.
type RemoveRetained struct {
	Branch        string             `json:"branch,omitempty"`
	State         CleanupBranchState `json:"state,omitempty"`
	DefaultBranch string             `json:"default_branch,omitempty"`
	Unpushed      []git.Commit       `json:"unpushed_commits"`
	Unmerged      []git.Commit       `json:"unmerged_commits"`
}

// Retained returns the plan-time retained-branch report. It is empty for a
// detached worktree, which has no branch to retain.
func (p RemovePlan) Retained() *RemoveRetained {
	if p.Branch == "" || p.DeleteBranch {
		return nil
	}
	return &RemoveRetained{
		Branch:        p.Branch,
		State:         p.Loss.BranchState,
		DefaultBranch: p.Loss.DefaultBranch,
		Unpushed:      nonNilCommits(p.Loss.Unpushed),
		Unmerged:      nonNilCommits(p.Loss.Unmerged),
	}
}

// RemovePlan is a prepared workstream removal: every pre-check has passed and
// the provider command is rendered. Nothing has been mutated yet.
type RemovePlan struct {
	Project      config.Project
	Path         string
	Branch       string
	SessionName  string
	SessionID    string // live session to close, empty when none
	ProviderName string
	Command      []string
	Workdir      string
	Warnings     []error // projects that could not be read, if any

	Force        bool
	DeleteBranch bool
	HeadSHA      string
	Loss         RemoveLoss
}

// RetainedBranch describes a branch that still exists after the worktree
// removal. State is the same merged/patch-equivalent/unmerged classification
// cleanup uses, so a cleanly merged branch is not reported as unmerged.
type RetainedBranch struct {
	Branch        string             `json:"branch"`
	State         CleanupBranchState `json:"state"`
	DefaultBranch string             `json:"default_branch,omitempty"`
	Commits       []git.Commit       `json:"commits,omitempty"`
	Error         string             `json:"error,omitempty"`
}

// RemoveResult is a completed removal.
type RemoveResult struct {
	Project          string
	Branch           string
	Path             string
	Provider         string
	ClosedSessionID  string
	DeletedBranch    string
	RetainedBranches []RetainedBranch
	Retained         *RemoveRetained
	Warnings         []string
	Loss             RemoveLoss
}

// RequiresConfirmation reports whether force would truly discard something:
// dirty paths, unique detached-HEAD commits, or an assessment that could not be
// proven. A retained branch's unmerged or unpushed commits are not losses.
func (p RemovePlan) RequiresConfirmation() bool {
	if !p.Force {
		return false
	}
	return len(p.Loss.DirtyFiles) > 0 ||
		len(p.Loss.DetachedCommits) > 0 ||
		p.Loss.AssessmentError != ""
}

// LossLines describes only what a forced removal truly discards: dirty paths
// and unique detached-HEAD commits, plus an assessment that could not be
// proven. Both the CLI and the TUI render this same list so they cannot drift.
func (p RemovePlan) LossLines() []string {
	var lines []string
	if len(p.Loss.DirtyFiles) > 0 {
		lines = append(lines, "uncommitted, untracked, or ignored changes (the worktree directory is deleted):")
		for _, file := range p.Loss.DirtyFiles {
			lines = append(lines, "  "+file)
		}
	}
	if len(p.Loss.DetachedCommits) > 0 {
		lines = append(lines, "commits unique to the detached HEAD (the worktree is deleted and these commits lose their only ref):")
		for _, commit := range p.Loss.DetachedCommits {
			lines = append(lines, "  "+commit.SHA+" "+commit.Subject)
		}
	}
	if p.Loss.AssessmentError != "" {
		lines = append(lines, "state could not be assessed: "+p.Loss.AssessmentError)
	}
	if len(lines) == 0 {
		return []string{"nothing would be lost"}
	}
	return lines
}

// RetainedLines describes the branch state that is not lost because the branch
// stays. Unpushed commits are listed here, not under loss.
func (p RemovePlan) RetainedLines() []string {
	if p.Branch == "" || p.DeleteBranch {
		return nil
	}
	var lines []string
	switch p.Loss.BranchState {
	case CleanupBranchUnmerged:
		lines = append(lines, "branch "+p.Branch+" is unmerged: "+commitCount(len(p.Loss.Unmerged))+" not in "+p.Loss.DefaultBranch)
	case CleanupBranchMerged:
		lines = append(lines, "branch "+p.Branch+" is merged into "+p.Loss.DefaultBranch)
	case CleanupBranchPatchEquivalent:
		lines = append(lines, "branch "+p.Branch+" is patch-equivalent to "+p.Loss.DefaultBranch)
	default:
		if p.Loss.AssessmentError == "" {
			lines = append(lines, "branch "+p.Branch+" state is unknown")
		}
	}
	if len(p.Loss.Unpushed) > 0 {
		lines = append(lines, "commits not on any remote-tracking branch:")
		for _, commit := range p.Loss.Unpushed {
			lines = append(lines, "  "+commit.SHA+" "+commit.Subject)
		}
	}
	return lines
}

// ActionLines lists the mutations ExecuteRemove will perform, in order. Both
// frontends render this same list.
func (p RemovePlan) ActionLines() []string {
	var lines []string
	if p.SessionID != "" {
		lines = append(lines, "close Rex session "+p.SessionID)
	} else {
		lines = append(lines, "no live Rex session to close")
	}
	lines = append(lines, fmt.Sprintf("run provider %q remove: %s", p.ProviderName, strings.Join(p.Command, " ")))
	if p.DeleteBranch && p.Branch != "" {
		switch p.Loss.BranchState {
		case CleanupBranchMerged:
			lines = append(lines, fmt.Sprintf("delete branch %s (merged into %s; git branch -d with atomic update-ref fallback)", p.Branch, p.Loss.DefaultBranch))
		case CleanupBranchPatchEquivalent:
			lines = append(lines, fmt.Sprintf("delete branch %s (patch-equivalent to %s; atomic update-ref -d after a fresh proof)", p.Branch, p.Loss.DefaultBranch))
		default:
			lines = append(lines, "delete branch "+p.Branch)
		}
	} else if p.Branch != "" {
		lines = append(lines, "retain branch "+p.Branch+" ("+retainedBranchState(p.Loss.BranchState, p.Loss.DefaultBranch, len(p.Loss.Unmerged))+")")
	}
	lines = append(lines, "clear workflow state for "+p.Path)
	return lines
}

// PrepareRemove runs the main-worktree, dirty-worktree, Rex, and provider
// pre-checks and renders the provider command without changing anything. The
// selector names the workstream using the shared grammar. The branch is always
// retained; use PrepareRemoveWithOptions to delete it.
func PrepareRemove(ctx context.Context, cfg config.Config, input string) (RemovePlan, error) {
	return PrepareRemoveWithOptions(ctx, cfg, input, RemoveOptions{})
}

// PrepareRemoveWithOptions is PrepareRemove with explicit force and
// branch-deletion choices. It records the exact loss before anything runs and
// collects every refusal reason instead of stopping at the first one.
func PrepareRemoveWithOptions(ctx context.Context, cfg config.Config, input string, opts RemoveOptions) (RemovePlan, error) {
	candidate, discovery, err := resolveWorkstream(ctx, cfg, input)
	if err != nil {
		return RemovePlan{}, resolutionError(err, discovery)
	}
	project := candidate.Project
	canonical := candidate.Path

	var warnings []error
	if discoveryErr := discovery.Err(); discoveryErr != nil {
		warnings = append(warnings, discoveryErr)
	}

	var reasons []string
	refuse := func(format string, args ...any) {
		reasons = append(reasons, fmt.Sprintf(format, args...))
	}

	mainPath, err := pathutil.Canonical(project.Path)
	if err != nil {
		return RemovePlan{}, err
	}
	if canonical == mainPath {
		return RemovePlan{}, fmt.Errorf("refusing to remove the main worktree %s", canonical)
	}

	worktrees, err := git.ListWorktrees(ctx, project.Path)
	if err != nil {
		return RemovePlan{}, fmt.Errorf("list worktrees of %s: %w", project.Path, err)
	}
	current, ok := worktreeAt(worktrees, canonical)
	if !ok {
		return RemovePlan{}, fmt.Errorf("worktree %s is not registered for project %q", canonical, project.Name)
	}

	branch := current.Branch
	detached := current.Detached || branch == ""

	if current.Locked {
		refuse("worktree %s is locked; unlock it first", canonical)
	}

	dirtyFiles, err := git.StatusLinesIncludingIgnored(ctx, canonical)
	if err != nil {
		return RemovePlan{}, fmt.Errorf("check worktree %s: %w", canonical, err)
	}
	if detached && !opts.Force {
		refuse("worktree %s has a detached HEAD %s; pass --force to remove it", canonical, shortCleanupSHA(current.Head))
	}
	if len(dirtyFiles) > 0 && !opts.Force {
		refuse("worktree %s has uncommitted or untracked changes (including ignored paths); commit or discard them first, or pass --force", canonical)
	}

	loss := RemoveLoss{
		DirtyFiles:      nonNilStrings(dirtyFiles),
		Detached:        detached,
		DetachedCommits: []git.Commit{},
		Unpushed:        []git.Commit{},
		Unmerged:        []git.Commit{},
	}

	// Only force or branch deletion needs a branch proof. Plain rm stays usable
	// in repositories without a resolvable default branch; the retained report
	// then says the branch state is unknown.
	needAssessment := opts.Force || opts.DeleteBranch
	var base string
	if needAssessment && !detached {
		base, err = git.DefaultBranch(ctx, project.Path)
		if err != nil {
			if opts.DeleteBranch {
				refuse("cannot resolve the project's default branch: %v", err)
			} else {
				loss.AssessmentError = err.Error()
			}
		}
	}

	if needAssessment && detached && opts.Force {
		commits, err := git.CommitsNotInRefs(ctx, canonical, "HEAD")
		if err != nil {
			loss.AssessmentError = fmt.Sprintf("check detached HEAD commits: %v", err)
		} else {
			loss.DetachedCommits = nonNilCommits(commits)
		}
	}

	if needAssessment && branch != "" && base != "" {
		loss.DefaultBranch = base
		state, proofErr := inspectCleanupBranch(ctx, project.Path, base, branch)
		if proofErr != nil {
			if opts.DeleteBranch {
				refuse("cannot prove branch %s: %v", branch, proofErr)
			} else {
				loss.AssessmentError = proofErr.Error()
			}
		} else {
			loss.BranchState = state
			if state == CleanupBranchUnmerged {
				unmerged, err := git.CommitsOutside(ctx, project.Path, base, branch)
				if err != nil {
					if opts.DeleteBranch {
						refuse("cannot list unmerged commits on %s: %v", branch, err)
					} else {
						loss.AssessmentError = err.Error()
					}
				} else {
					loss.Unmerged = nonNilCommits(unmerged)
				}
			}
		}
		unpushed, err := git.UnpushedCommits(ctx, project.Path, base, branch)
		if err != nil {
			if opts.DeleteBranch {
				refuse("cannot check commits on %s: %v", branch, err)
			} else {
				loss.AssessmentError = err.Error()
			}
		} else {
			loss.Unpushed = nonNilCommits(unpushed)
		}
	}

	if opts.DeleteBranch {
		switch {
		case detached:
			refuse("worktree %s is not on a local branch; nothing to delete", canonical)
		case protectedCleanupBranch(branch, base) || branch == base:
			refuse("refusing to delete protected branch %s", branch)
		case loss.BranchState == CleanupBranchUnmerged:
			target := base
			if target == "" {
				target = "the default branch"
			}
			refuse("branch %s is unmerged relative to %s (%s not in %s); refusing --delete-branch; run without --delete-branch to remove the worktree and retain the branch",
				branch, target, commitCount(len(loss.Unmerged)), target)
		}
		if branch != "" {
			for _, worktree := range git.WorktreesForBranch(worktrees, branch) {
				if worktree.Path != canonical {
					refuse("branch %s is checked out in worktree %s; remove that worktree first", branch, worktree.Path)
				}
			}
		}
	}

	sessionName := candidate.SessionName

	// Rex must be reachable to close a live session.
	client := rex.New(cfg.RexBinary())
	sessions, rexErr := client.ListSessions(ctx)
	var sessionID string
	if rexErr != nil {
		refuse("check Rex sessions: %v", rexErr)
	} else {
		var matches []rex.Session
		for _, session := range sessions {
			if session.Label == sessionName {
				matches = append(matches, session)
			}
		}
		switch len(matches) {
		case 0:
		case 1:
			sessionID = matches[0].ID
		default:
			ids := make([]string, 0, len(matches))
			for _, session := range matches {
				ids = append(ids, session.ID)
			}
			refuse("multiple live Rex sessions labeled %q: %s; kill one manually", sessionName, strings.Join(ids, ", "))
		}
	}

	providerName := cfg.ProviderFor(project)
	p, ok := cfg.Providers[providerName]
	if !ok {
		refuse("project %q uses unknown provider %q", project.Name, providerName)
	}
	var removeArgs []string
	var workdir string
	if ok {
		data := provider.Data{
			Project:      project.Name,
			RepoPath:     project.Path,
			RepoName:     filepath.Base(project.Path),
			Branch:       branch,
			Slug:         BranchSlug(branch),
			WorktreePath: canonical,
		}
		var renderErr error
		removeArgs, renderErr = provider.Render(p.Remove, data)
		if renderErr != nil {
			refuse("provider %q remove: %v", providerName, renderErr)
		} else if opts.Force && len(dirtyFiles) > 0 {
			// A clean worktree does not need --force, even when the user passed
			// it to authorise an unmerged branch or a detached HEAD.
			removeArgs = forceGitWorktreeRemove(removeArgs)
		}
		workdir, renderErr = provider.RenderWorkdir(p.Workdir, data)
		if renderErr != nil {
			refuse("provider %q workdir: %v", providerName, renderErr)
		}
	}

	if err := refusalError(canonical, reasons); err != nil {
		return RemovePlan{}, err
	}

	return RemovePlan{
		Project:      project,
		Path:         canonical,
		Branch:       branch,
		SessionName:  sessionName,
		SessionID:    sessionID,
		ProviderName: providerName,
		Command:      removeArgs,
		Workdir:      workdir,
		Warnings:     warnings,
		Force:        opts.Force,
		DeleteBranch: opts.DeleteBranch,
		HeadSHA:      current.Head,
		Loss:         loss,
	}, nil
}

// refusalError keeps a single refusal message unchanged and joins multiple
// reasons, so a refused removal names every reason it found.
func refusalError(path string, reasons []string) error {
	switch len(reasons) {
	case 0:
		return nil
	case 1:
		return errors.New(reasons[0])
	default:
		return fmt.Errorf("refusing to remove %s: %s", path, strings.Join(reasons, "; "))
	}
}

// forceGitWorktreeRemove appends --force to the built-in git provider's
// `worktree remove` command. A dirty worktree cannot be removed otherwise. Any
// other provider keeps its rendered command; its own remove semantics apply.
func forceGitWorktreeRemove(args []string) []string {
	for _, arg := range args {
		if arg == "--force" || arg == "-f" {
			return args
		}
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "worktree" || args[i+1] != "remove" {
			continue
		}
		out := make([]string, 0, len(args)+1)
		out = append(out, args[:i+2]...)
		out = append(out, "--force")
		out = append(out, args[i+2:]...)
		return out
	}
	return args
}

// Cmd builds the provider process for tea.ExecProcess.
func (p RemovePlan) Cmd() (*exec.Cmd, error) {
	return provider.Command(p.Command, p.Workdir)
}

// CloseSession kills the workstream's live Rex session, if any, and returns
// the closed session ID.
func (p RemovePlan) CloseSession(ctx context.Context, cfg config.Config) (string, error) {
	if p.SessionID == "" {
		return "", nil
	}
	if err := rex.New(cfg.RexBinary()).KillSession(ctx, p.SessionID); err != nil {
		return "", fmt.Errorf("close Rex session %s: %w", p.SessionID, err)
	}
	return p.SessionID, nil
}

// Cleanup removes the workstream's state.json entry, if one exists.
func (p RemovePlan) Cleanup(cfg config.Config) error {
	state, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		return err
	}
	if _, ok := state.Workstreams[p.Path]; !ok {
		return nil
	}
	delete(state.Workstreams, p.Path)
	if err := state.Save(); err != nil {
		return fmt.Errorf("clean state for %s: %w", p.Path, err)
	}
	return nil
}

// RemoveWorkstream closes the Rex session, removes the worktree with the
// project's provider, and deletes the workstream's state. The selector names
// the workstream using the shared grammar. It is the one-shot form for callers
// that do not need to show the resolved target first; the CLI and TUI use
// PrepareRemove and ExecuteRemove. The branch is retained.
func RemoveWorkstream(ctx context.Context, cfg config.Config, input string, stdin io.Reader, stdout, stderr io.Writer) (RemoveResult, error) {
	plan, err := PrepareRemove(ctx, cfg, input)
	if err != nil {
		return RemoveResult{}, err
	}
	return ExecuteRemove(ctx, cfg, plan, stdin, stdout, stderr)
}

// ExecuteRemove performs a prepared removal. It is separate from
// PrepareRemove so a frontend can show the resolved target and the exact loss
// before anything is mutated. Every mutation re-verifies what the plan proved:
// no new Rex session may have appeared, the worktree must still be clean (or
// still have exactly the reported dirty paths), its branch and HEAD must not
// have moved, and a branch proof is re-run immediately before deletion.
func ExecuteRemove(ctx context.Context, cfg config.Config, plan RemovePlan, stdin io.Reader, stdout, stderr io.Writer) (RemoveResult, error) {
	// Verify the worktree before killing a session: a change after
	// confirmation must not leave the session closed but the worktree kept.
	if err := plan.verifyWorktree(ctx); err != nil {
		return RemoveResult{}, err
	}
	present, err := plan.verifyRex(ctx, cfg)
	if err != nil {
		return RemoveResult{}, err
	}
	var closedSessionID string
	if present {
		closedSessionID, err = plan.CloseSession(ctx, cfg)
		if err != nil {
			return RemoveResult{}, err
		}
	}
	// Re-verify immediately before the provider destroys the worktree.
	if err := plan.verifyWorktree(ctx); err != nil {
		return RemoveResult{}, err
	}
	if _, err := plan.verifyRex(ctx, cfg); err != nil {
		return RemoveResult{}, err
	}
	// Every pre-check has passed. From here on a mutation may already have
	// happened, so a failure still returns the partial result: the CLI emits it
	// as the rm --json document with error set. A retained branch is reported
	// even when the plan would have deleted it later, because it still exists.
	keepBranch := func(res RemoveResult) RemoveResult {
		if retained := plan.retainedBranch(ctx); retained != nil {
			res.RetainedBranches = append(res.RetainedBranches, *retained)
		}
		return res
	}
	partial := RemoveResult{
		Project:          plan.Project.Name,
		Branch:           plan.Branch,
		Path:             plan.Path,
		Provider:         plan.ProviderName,
		ClosedSessionID:  closedSessionID,
		RetainedBranches: []RetainedBranch{},
		Retained:         plan.Retained(),
		Warnings:         []string{},
		Loss:             plan.Loss,
	}
	if err := provider.Run(ctx, plan.Command, plan.Workdir, stdin, stdout, stderr); err != nil {
		return keepBranch(partial), fmt.Errorf("provider %q remove: %w", plan.ProviderName, err)
	}
	leftover, err := plan.verifyRemoved(ctx)
	if err != nil {
		return keepBranch(partial), err
	}

	result := partial
	if leftover != "" {
		result.Warnings = append(result.Warnings, leftover)
	}
	if warning := plan.MissingBranchWarning(ctx); warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}

	if plan.DeleteBranch && plan.Branch != "" {
		if _, err := plan.verifyRex(ctx, cfg); err != nil {
			return keepBranch(result), fmt.Errorf("worktree removed at %s but branch %s kept: %w", plan.Path, plan.Branch, err)
		}
		if err := plan.deleteBranch(ctx); err != nil {
			return keepBranch(result), fmt.Errorf("worktree removed at %s but branch %s kept: %w", plan.Path, plan.Branch, err)
		}
		result.DeletedBranch = plan.Branch
	}

	// Report the retained branch before state cleanup so a state write failure
	// still carries the C19 report.
	if retained := plan.retainedBranch(ctx); retained != nil {
		result.RetainedBranches = append(result.RetainedBranches, *retained)
	}

	if err := plan.Cleanup(cfg); err != nil {
		return result, err
	}
	return result, nil
}

// VerifyRemoval re-checks the plan immediately before a frontend closes the
// session and runs the provider. The TUI uses it because it runs the provider
// with ExecProcess instead of ExecuteRemove. It refuses when the worktree
// changed, its loss is stale, or a new live Rex session appeared.
func (p RemovePlan) VerifyRemoval(ctx context.Context, cfg config.Config) error {
	if err := p.verifyWorktree(ctx); err != nil {
		return err
	}
	_, err := p.verifyRex(ctx, cfg)
	return err
}

// VerifyRemoved re-checks that the provider removed the worktree. It is the
// post-provider half of ExecuteRemove, exported for the TUI's ExecProcess path.
// A non-empty warning means the worktree was deregistered but its directory
// remains; the caller may clear state and report the leftover directory.
func (p RemovePlan) VerifyRemoved(ctx context.Context) (string, error) {
	return p.verifyRemoved(ctx)
}

// RetainedBranch reports the branch left behind after removal, or nil when the
// branch is gone. It is exported for the TUI, which cleans state itself.
func (p RemovePlan) RetainedBranch(ctx context.Context) *RetainedBranch {
	return p.retainedBranch(ctx)
}

// MissingBranchWarning reports a retained branch that no longer resolves after
// the provider remove. A provider must not delete the branch; rm only deletes
// one through --delete-branch. The warning names the commits that may now be
// unreachable.
func (p RemovePlan) MissingBranchWarning(ctx context.Context) string {
	if p.DeleteBranch || p.Branch == "" {
		return ""
	}
	if _, err := git.BranchSHA(ctx, p.Project.Path, p.Branch); err == nil {
		return ""
	}
	commits := p.Loss.Unpushed
	if len(commits) == 0 && p.HeadSHA != "" {
		// Plain rm skips the assessment, and the branch ref is already gone.
		// Use the recorded tip to find commits that are now on no ref at all.
		if unique, err := git.CommitsNotInRefs(ctx, p.Project.Path, p.HeadSHA); err == nil {
			commits = unique
		}
	}
	return branchDisappearanceWarning(p.Branch, commits)
}

func branchDisappearanceWarning(branch string, commits []git.Commit) string {
	warning := fmt.Sprintf("branch %s disappeared during the provider remove; rm did not delete it", branch)
	if len(commits) == 0 {
		return warning
	}
	parts := make([]string, 0, len(commits))
	for _, commit := range commits {
		parts = append(parts, commit.SHA+" "+commit.Subject)
	}
	return warning + "; these commits may now be unreachable: " + strings.Join(parts, ", ")
}

// verifyRex refuses when a live Rex session with the workstream label appeared
// after planning. The recorded session may still exist: it is about to be
// closed, or a kill may not have propagated yet. It reports whether the
// recorded session is still present.
func (p RemovePlan) verifyRex(ctx context.Context, cfg config.Config) (bool, error) {
	sessions, err := rex.New(cfg.RexBinary()).ListSessions(ctx)
	if err != nil {
		return false, fmt.Errorf("check Rex sessions: %w", err)
	}
	present := false
	for _, session := range sessions {
		if session.Label != p.SessionName {
			continue
		}
		if session.ID != p.SessionID {
			return false, fmt.Errorf("a live Rex session labeled %q (%s) started after planning; kept", p.SessionName, session.ID)
		}
		present = true
	}
	return present, nil
}

// verifyWorktree re-proves the worktree is still the one the plan described.
func (p RemovePlan) verifyWorktree(ctx context.Context) error {
	mainPath, err := pathutil.Canonical(p.Project.Path)
	if err != nil {
		return err
	}
	if p.Path == mainPath {
		return fmt.Errorf("refusing to remove the main worktree %s", p.Path)
	}
	worktrees, err := git.ListWorktrees(ctx, p.Project.Path)
	if err != nil {
		return fmt.Errorf("list worktrees of %s: %w", p.Project.Path, err)
	}
	current, ok := worktreeAt(worktrees, p.Path)
	if !ok {
		return fmt.Errorf("worktree %s is no longer registered; kept", p.Path)
	}
	if current.Locked {
		return fmt.Errorf("worktree %s is locked; kept", p.Path)
	}
	if current.Head != p.HeadSHA || current.Branch != p.Branch {
		return fmt.Errorf("worktree %s changed branch or HEAD after planning; kept", p.Path)
	}
	dirtyFiles, err := git.StatusLinesIncludingIgnored(ctx, p.Path)
	if err != nil {
		return fmt.Errorf("re-check worktree %s: %w", p.Path, err)
	}
	if !p.Force {
		if len(dirtyFiles) > 0 {
			return fmt.Errorf("worktree %s changed after planning (%s); kept", p.Path, dirtyDescription(len(dirtyFiles), nil))
		}
		return nil
	}
	if !sameStrings(dirtyFiles, p.Loss.DirtyFiles) {
		return fmt.Errorf("worktree %s changed after planning; the reported loss is stale; kept", p.Path)
	}
	// A ref that pinned a detached HEAD at plan time may have moved or been
	// deleted while the confirmation was on screen. Recompute which commits are
	// now unique so a newly stranded commit is never removed silently.
	if p.Loss.Detached {
		commits, err := git.CommitsNotInRefs(ctx, p.Path, "HEAD")
		if err != nil {
			return fmt.Errorf("re-check detached HEAD commits in %s: %w", p.Path, err)
		}
		planned := make(map[string]bool, len(p.Loss.DetachedCommits))
		for _, commit := range p.Loss.DetachedCommits {
			planned[commit.SHA] = true
		}
		for _, commit := range commits {
			if !planned[commit.SHA] {
				return fmt.Errorf("detached HEAD commit %s became unique to worktree %s after planning; the reported loss is stale; kept", shortCleanupSHA(commit.SHA), p.Path)
			}
		}
	}
	return nil
}

// verifyRemoved proves the provider deregistered the worktree. A still-
// registered worktree is a hard failure: deleting a branch that is still
// checked out would fail or remove the wrong data, and state must be kept. A
// deregistered worktree whose directory remains is reported as a warning; the
// caller may clear state and tell the user to remove the leftover directory.
func (p RemovePlan) verifyRemoved(ctx context.Context) (string, error) {
	worktrees, err := git.ListWorktrees(ctx, p.Project.Path)
	if err != nil {
		return "", fmt.Errorf("re-list worktrees of %s: %w", p.Project.Path, err)
	}
	if _, ok := worktreeAt(worktrees, p.Path); ok {
		return "", fmt.Errorf("provider %q did not remove worktree %s; branch and state kept", p.ProviderName, p.Path)
	}
	if _, err := os.Stat(p.Path); err == nil {
		return fmt.Sprintf("provider %q deregistered worktree %s but its directory remains; remove the directory manually", p.ProviderName, p.Path), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("re-check worktree %s: %w", p.Path, err)
	}
	return "", nil
}

// deleteBranch re-proves the plan's branch rule immediately before deletion.
// A merged branch uses git branch -d; -D is reserved for a freshly re-proven
// patch-equivalent branch. An unmerged branch is never deleted.
func (p RemovePlan) deleteBranch(ctx context.Context) error {
	if p.Branch == "" {
		return errors.New("no branch to delete")
	}
	worktrees, err := git.ListWorktrees(ctx, p.Project.Path)
	if err != nil {
		return fmt.Errorf("re-list worktrees: %w", err)
	}
	for _, worktree := range git.WorktreesForBranch(worktrees, p.Branch) {
		return fmt.Errorf("branch %s is still checked out in worktree %s; kept", p.Branch, worktree.Path)
	}
	current, err := git.BranchSHA(ctx, p.Project.Path, p.Branch)
	if err != nil {
		return fmt.Errorf("re-check branch %s: %w", p.Branch, err)
	}
	if p.HeadSHA != "" && current != p.HeadSHA {
		return fmt.Errorf("branch %s moved after planning; kept", p.Branch)
	}
	base := p.Loss.DefaultBranch
	if latest, err := git.DefaultBranch(ctx, p.Project.Path); err == nil {
		base = latest
	}
	if base == "" {
		return fmt.Errorf("default branch is unknown; branch %s kept", p.Branch)
	}
	if p.Branch == base || protectedCleanupBranch(p.Branch, base) {
		return fmt.Errorf("refusing to delete protected branch %s; kept", p.Branch)
	}
	merged, err := git.IsAncestor(ctx, p.Project.Path, p.Branch, base)
	if err != nil {
		return fmt.Errorf("re-check branch %s ancestry: %w", p.Branch, err)
	}
	if merged {
		if err := git.DeleteBranch(ctx, p.Project.Path, p.Branch, false); err == nil {
			return nil
		}
		// git branch -d checks HEAD, which may be on another branch. Re-prove
		// the base-relative ancestry with the unchanged SHA and delete
		// atomically so a moved branch cannot be deleted by accident.
		latest, err := git.BranchSHA(ctx, p.Project.Path, p.Branch)
		if err != nil || (p.HeadSHA != "" && latest != p.HeadSHA) {
			return fmt.Errorf("branch %s moved after planning; kept", p.Branch)
		}
		merged, err = git.IsAncestor(ctx, p.Project.Path, p.Branch, base)
		if err != nil || !merged {
			return fmt.Errorf("branch %s is merged into %s but git branch -d refused and the ancestry proof no longer holds; kept", p.Branch, base)
		}
		if err := git.DeleteBranchAt(ctx, p.Project.Path, p.Branch, latest); err != nil {
			return fmt.Errorf("branch %s is merged into %s but atomic deletion failed: %w", p.Branch, base, err)
		}
		git.ForgetBranchConfig(ctx, p.Project.Path, p.Branch)
		return nil
	}
	patchEquivalent, err := git.PatchEquivalent(ctx, p.Project.Path, base, p.Branch)
	if err != nil {
		return fmt.Errorf("re-check branch %s patch-equivalence: %w", p.Branch, err)
	}
	if !patchEquivalent {
		return fmt.Errorf("branch %s is no longer merged or patch-equivalent to %s; kept", p.Branch, base)
	}
	if err := git.DeleteBranchAt(ctx, p.Project.Path, p.Branch, current); err != nil {
		return fmt.Errorf("branch %s is patch-equivalent to %s but atomic deletion failed: %w", p.Branch, base, err)
	}
	git.ForgetBranchConfig(ctx, p.Project.Path, p.Branch)
	return nil
}

// retainedBranch reports a branch left behind after the worktree removal. It
// never fails: an unverifiable branch is still named, with Error explaining
// why its state is unknown.
func (p RemovePlan) retainedBranch(ctx context.Context) *RetainedBranch {
	if p.Branch == "" {
		return nil
	}
	if _, err := git.BranchSHA(ctx, p.Project.Path, p.Branch); err != nil {
		return nil // deleted, or never existed
	}
	retained := &RetainedBranch{Branch: p.Branch}
	base := p.Loss.DefaultBranch
	if latest, err := git.DefaultBranch(ctx, p.Project.Path); err == nil {
		base = latest
	}
	retained.DefaultBranch = base
	if base == "" {
		retained.Error = "no default branch found; state unknown"
		return retained
	}
	state, err := inspectCleanupBranch(ctx, p.Project.Path, base, p.Branch)
	if err != nil {
		retained.Error = err.Error()
		return retained
	}
	retained.State = state
	if state == CleanupBranchUnmerged {
		commits, err := git.CommitsOutside(ctx, p.Project.Path, base, p.Branch)
		if err != nil {
			retained.Error = err.Error()
		} else {
			retained.Commits = nonNilCommits(commits)
		}
	}
	return retained
}

func worktreeAt(worktrees []git.Worktree, path string) (git.Worktree, bool) {
	for _, worktree := range worktrees {
		if worktree.Path == path {
			return worktree, true
		}
	}
	for _, worktree := range worktrees {
		if canonical, err := pathutil.Canonical(worktree.Path); err == nil && canonical == path {
			return worktree, true
		}
	}
	return git.Worktree{}, false
}

func commitCount(count int) string {
	if count == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", count)
}

func retainedBranchState(state CleanupBranchState, base string, unmerged int) string {
	switch state {
	case CleanupBranchMerged:
		return "merged into " + base
	case CleanupBranchPatchEquivalent:
		return "patch-equivalent to " + base
	case CleanupBranchUnmerged:
		return "unmerged: " + commitCount(unmerged) + " not in " + base
	default:
		return "state unknown"
	}
}
