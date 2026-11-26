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
)

// CreatePlan is a prepared worktree creation. Validation is done and the
// provider command is rendered, but nothing has run yet. A TUI can run
// Cmd through tea.ExecProcess and then call Verify.
type CreatePlan struct {
	Project      config.Project
	Branch       string
	ProviderName string
	Command      []string
	Workdir      string
	TargetPath   string
}

// CreateResult is a verified worktree creation.
type CreateResult struct {
	Project  string
	Branch   string
	Path     string
	Provider string
}

// PrepareCreate validates a new-worktree request and renders the provider
// command without running it.
func PrepareCreate(ctx context.Context, cfg config.Config, projectName, branch string) (CreatePlan, error) {
	project, ok := cfg.Project(projectName)
	if !ok {
		return CreatePlan{}, fmt.Errorf("no registered project %q (registered: %s)", projectName, projectNames(cfg))
	}
	if strings.TrimSpace(branch) == "" {
		return CreatePlan{}, fmt.Errorf("branch name is empty")
	}
	if err := git.CheckBranchName(ctx, project.Path, branch); err != nil {
		return CreatePlan{}, err
	}

	worktrees, err := git.ListWorktrees(ctx, project.Path)
	if err != nil {
		return CreatePlan{}, err
	}
	if existing := git.WorktreesForBranch(worktrees, branch); len(existing) > 0 {
		return CreatePlan{}, fmt.Errorf("branch %q is already checked out at %s", branch, existing[0].Path)
	}

	providerName := cfg.ProviderFor(project)
	p, ok := cfg.Providers[providerName]
	if !ok {
		return CreatePlan{}, fmt.Errorf("project %q uses unknown provider %q", project.Name, providerName)
	}

	target := TargetPath(project, branch)
	data := provider.Data{
		Project:    project.Name,
		RepoPath:   project.Path,
		RepoName:   filepath.Base(project.Path),
		Branch:     branch,
		Slug:       BranchSlug(branch),
		TargetPath: target,
	}
	createArgs, err := provider.Render(p.Create, data)
	if err != nil {
		return CreatePlan{}, fmt.Errorf("provider %q create: %w", providerName, err)
	}
	workdir, err := provider.RenderWorkdir(p.Workdir, data)
	if err != nil {
		return CreatePlan{}, fmt.Errorf("provider %q workdir: %w", providerName, err)
	}
	return CreatePlan{
		Project:      project,
		Branch:       branch,
		ProviderName: providerName,
		Command:      createArgs,
		Workdir:      workdir,
		TargetPath:   target,
	}, nil
}

// Cmd builds the provider process for tea.ExecProcess.
func (p CreatePlan) Cmd() (*exec.Cmd, error) {
	return provider.Command(p.Command, p.Workdir)
}

// Verify checks that the provider left exactly one worktree for the branch and
// returns its canonical path.
func (p CreatePlan) Verify(ctx context.Context) (string, error) {
	worktrees, err := git.ListWorktrees(ctx, p.Project.Path)
	if err != nil {
		return "", err
	}
	matches := git.WorktreesForBranch(worktrees, p.Branch)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("provider %q finished but no worktree for branch %q was found; a provider must leave a valid Git worktree", p.ProviderName, p.Branch)
	case 1:
	default:
		paths := make([]string, 0, len(matches))
		for _, wt := range matches {
			paths = append(paths, wt.Path)
		}
		return "", fmt.Errorf("provider %q left %d worktrees for branch %q: %s", p.ProviderName, len(matches), p.Branch, strings.Join(paths, ", "))
	}
	return pathutil.Canonical(matches[0].Path)
}

// CreateWorktree runs the provider for a new worktree and verifies the result.
func CreateWorktree(ctx context.Context, cfg config.Config, projectName, branch string, stdin io.Reader, stdout, stderr io.Writer) (CreateResult, error) {
	plan, err := PrepareCreate(ctx, cfg, projectName, branch)
	if err != nil {
		return CreateResult{}, err
	}
	if err := provider.Run(ctx, plan.Command, plan.Workdir, stdin, stdout, stderr); err != nil {
		return CreateResult{}, fmt.Errorf("provider %q create: %w", plan.ProviderName, err)
	}
	path, err := plan.Verify(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{
		Project:  plan.Project.Name,
		Branch:   plan.Branch,
		Path:     path,
		Provider: plan.ProviderName,
	}, nil
}

// TargetPath is the provider's suggested destination for a new worktree.
func TargetPath(project config.Project, branch string) string {
	if project.WorktreeRoot != "" {
		return filepath.Join(project.WorktreeRoot, branch)
	}
	repoName := filepath.Base(project.Path)
	return filepath.Join(filepath.Dir(project.Path), repoName+"-wt", branch)
}

// BranchSlug is a flat directory-name form of a branch for provider scripts.
func BranchSlug(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}
