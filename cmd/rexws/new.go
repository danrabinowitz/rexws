package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
	"github.com/danrabinowitz/rexws/internal/provider"
)

func newNewCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "new <project> <branch>",
		Short: "Create a worktree with the project's provider",
		Long: "Create a worktree for a new branch using the project's configured provider.\n" +
			"The target path is <worktree_root>/<branch> when the project sets worktree_root,\n" +
			"otherwise <repo>-wt/<branch> beside the repository.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, branch := args[0], args[1]

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			project, ok := cfg.Project(projectName)
			if !ok {
				return fmt.Errorf("no registered project %q (registered: %s)", projectName, projectNames(cfg))
			}
			if err := git.CheckBranchName(cmd.Context(), project.Path, branch); err != nil {
				return err
			}

			worktrees, err := git.ListWorktrees(cmd.Context(), project.Path)
			if err != nil {
				return err
			}
			if existing := git.WorktreesForBranch(worktrees, branch); len(existing) > 0 {
				return fmt.Errorf("branch %q is already checked out at %s", branch, existing[0].Path)
			}

			providerName := cfg.ProviderFor(project)
			p, ok := cfg.Providers[providerName]
			if !ok {
				return fmt.Errorf("project %q uses unknown provider %q", project.Name, providerName)
			}

			target := targetPath(project, branch)

			data := provider.Data{
				Project:    project.Name,
				RepoPath:   project.Path,
				RepoName:   filepath.Base(project.Path),
				Branch:     branch,
				Slug:       branchSlug(branch),
				TargetPath: target,
			}
			createArgs, err := provider.Render(p.Create, data)
			if err != nil {
				return fmt.Errorf("provider %q create: %w", providerName, err)
			}
			workdir, err := provider.RenderWorkdir(p.Workdir, data)
			if err != nil {
				return fmt.Errorf("provider %q workdir: %w", providerName, err)
			}

			providerOut := cmd.OutOrStdout()
			if asJSON {
				providerOut = cmd.ErrOrStderr()
			}
			if err := provider.Run(cmd.Context(), createArgs, workdir, cmd.InOrStdin(), providerOut, cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("provider %q create: %w", providerName, err)
			}

			worktrees, err = git.ListWorktrees(cmd.Context(), project.Path)
			if err != nil {
				return err
			}
			matches := git.WorktreesForBranch(worktrees, branch)
			switch len(matches) {
			case 0:
				return fmt.Errorf("provider %q finished but no worktree for branch %q was found; a provider must leave a valid Git worktree", providerName, branch)
			case 1:
			default:
				paths := make([]string, 0, len(matches))
				for _, wt := range matches {
					paths = append(paths, wt.Path)
				}
				return fmt.Errorf("provider %q left %d worktrees for branch %q: %s", providerName, len(matches), branch, strings.Join(paths, ", "))
			}
			path, err := pathutil.Canonical(matches[0].Path)
			if err != nil {
				return err
			}

			if asJSON {
				return encodeJSON(cmd, struct {
					Project  string `json:"project"`
					Branch   string `json:"branch"`
					Path     string `json:"path"`
					Provider string `json:"provider"`
				}{project.Name, branch, path, providerName})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created worktree %s at %s\n", branch, path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// targetPath is the provider's suggested destination for a new worktree.
func targetPath(project config.Project, branch string) string {
	if project.WorktreeRoot != "" {
		return filepath.Join(project.WorktreeRoot, branch)
	}
	repoName := filepath.Base(project.Path)
	return filepath.Join(filepath.Dir(project.Path), repoName+"-wt", branch)
}

// branchSlug is a flat directory-name form of a branch for provider scripts.
func branchSlug(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}
