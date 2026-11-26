package ops

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
	"github.com/danrabinowitz/rexws/internal/provider"
	"github.com/danrabinowitz/rexws/internal/rex"
)

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
}

// RemoveResult is a completed removal.
type RemoveResult struct {
	Project         string
	Branch          string
	Path            string
	Provider        string
	ClosedSessionID string
}

// PrepareRemove runs the main-worktree, dirty-worktree, Rex, and provider
// pre-checks and renders the provider command without changing anything.
func PrepareRemove(ctx context.Context, cfg config.Config, path string) (RemovePlan, error) {
	canonical, err := pathutil.Canonical(path)
	if err != nil {
		return RemovePlan{}, err
	}
	discovery := Discover(ctx, cfg)
	project, target, found, err := discovery.Worktree(canonical)
	if err != nil {
		return RemovePlan{}, err
	}
	if !found {
		return RemovePlan{}, notFoundError(discovery, canonical)
	}

	mainPath, err := pathutil.Canonical(project.Path)
	if err != nil {
		return RemovePlan{}, err
	}
	if canonical == mainPath {
		return RemovePlan{}, fmt.Errorf("refusing to remove the main worktree %s", canonical)
	}

	dirty, err := git.IsDirty(ctx, canonical)
	if err != nil {
		return RemovePlan{}, fmt.Errorf("check worktree %s: %w", canonical, err)
	}
	if dirty {
		return RemovePlan{}, fmt.Errorf("worktree %s has uncommitted or untracked changes; commit or discard them first", canonical)
	}

	branch := target.Branch
	if branch == "" {
		branch = target.ShortHead()
	}
	sessionName := project.Name + "/" + branch

	// Rex must be reachable to close a live session.
	client := rex.New(cfg.RexBinary())
	sessions, err := client.ListSessions(ctx)
	if err != nil {
		return RemovePlan{}, fmt.Errorf("check Rex sessions: %w", err)
	}
	var matches []rex.Session
	for _, session := range sessions {
		if session.Label == sessionName {
			matches = append(matches, session)
		}
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, session := range matches {
			ids = append(ids, session.ID)
		}
		return RemovePlan{}, fmt.Errorf("multiple live Rex sessions labeled %q: %s; kill one manually", sessionName, strings.Join(ids, ", "))
	}
	var sessionID string
	if len(matches) == 1 {
		sessionID = matches[0].ID
	}

	providerName := cfg.ProviderFor(project)
	p, ok := cfg.Providers[providerName]
	if !ok {
		return RemovePlan{}, fmt.Errorf("project %q uses unknown provider %q", project.Name, providerName)
	}
	data := provider.Data{
		Project:      project.Name,
		RepoPath:     project.Path,
		RepoName:     filepath.Base(project.Path),
		Branch:       branch,
		Slug:         BranchSlug(branch),
		WorktreePath: canonical,
	}
	removeArgs, err := provider.Render(p.Remove, data)
	if err != nil {
		return RemovePlan{}, fmt.Errorf("provider %q remove: %w", providerName, err)
	}
	workdir, err := provider.RenderWorkdir(p.Workdir, data)
	if err != nil {
		return RemovePlan{}, fmt.Errorf("provider %q workdir: %w", providerName, err)
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
	}, nil
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
// project's provider, and deletes the workstream's state.
func RemoveWorkstream(ctx context.Context, cfg config.Config, path string, stdin io.Reader, stdout, stderr io.Writer) (RemoveResult, error) {
	plan, err := PrepareRemove(ctx, cfg, path)
	if err != nil {
		return RemoveResult{}, err
	}
	closedSessionID, err := plan.CloseSession(ctx, cfg)
	if err != nil {
		return RemoveResult{}, err
	}
	if err := provider.Run(ctx, plan.Command, plan.Workdir, stdin, stdout, stderr); err != nil {
		return RemoveResult{}, fmt.Errorf("provider %q remove: %w", plan.ProviderName, err)
	}
	if err := plan.Cleanup(cfg); err != nil {
		return RemoveResult{}, err
	}
	return RemoveResult{
		Project:         plan.Project.Name,
		Branch:          plan.Branch,
		Path:            plan.Path,
		Provider:        plan.ProviderName,
		ClosedSessionID: closedSessionID,
	}, nil
}
