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
	"github.com/danrabinowitz/rexws/internal/rex"
)

func newRmCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "rm <path>",
		Short: "Remove a workstream's worktree and Rex session",
		Long: "Close the worktree's Rex session if one is running, run the project's\n" +
			"provider remove command, and delete its workflow state.\n" +
			"The project's main worktree is refused.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := pathutil.Canonical(args[0])
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			inputs, err := discover(cmd.Context(), cfg)
			if err != nil {
				return err
			}

			var (
				project config.Project
				target  git.Worktree
				found   bool
			)
			for _, input := range inputs {
				for _, wt := range input.Worktrees {
					if wt.Bare || wt.Prunable {
						continue
					}
					candidate, err := pathutil.Canonical(wt.Path)
					if err != nil {
						return err
					}
					if candidate == path {
						project, target, found = input.Project, wt, true
					}
				}
			}
			if !found {
				return fmt.Errorf("%s is not a worktree of a registered project", path)
			}
			mainPath, err := pathutil.Canonical(project.Path)
			if err != nil {
				return err
			}
			if path == mainPath {
				return fmt.Errorf("refusing to remove the main worktree %s", path)
			}
			dirty, err := git.IsDirty(cmd.Context(), path)
			if err != nil {
				return fmt.Errorf("check worktree %s: %w", path, err)
			}
			if dirty {
				return fmt.Errorf("worktree %s has uncommitted or untracked changes; commit or discard them first", path)
			}

			branch := target.Branch
			if branch == "" {
				branch = target.ShortHead()
			}
			sessionName := project.Name + "/" + branch

			providerName := cfg.ProviderFor(project)
			p, ok := cfg.Providers[providerName]
			if !ok {
				return fmt.Errorf("project %q uses unknown provider %q", project.Name, providerName)
			}
			data := provider.Data{
				Project:      project.Name,
				RepoPath:     project.Path,
				RepoName:     filepath.Base(project.Path),
				Branch:       branch,
				Slug:         branchSlug(branch),
				WorktreePath: path,
			}
			removeArgs, err := provider.Render(p.Remove, data)
			if err != nil {
				return fmt.Errorf("provider %q remove: %w", providerName, err)
			}
			workdir, err := provider.RenderWorkdir(p.Workdir, data)
			if err != nil {
				return fmt.Errorf("provider %q workdir: %w", providerName, err)
			}

			state, err := config.LoadState(cfg.StatusIDs())
			if err != nil {
				return err
			}

			client := rex.New(cfg.RexBinary())
			sessions, err := client.ListSessions(cmd.Context())
			if err != nil {
				return fmt.Errorf("check Rex sessions: %w", err)
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
				return fmt.Errorf("multiple live Rex sessions labeled %q: %s; kill one manually", sessionName, strings.Join(ids, ", "))
			}
			closedSessionID := ""
			if len(matches) == 1 {
				if err := client.KillSession(cmd.Context(), matches[0].ID); err != nil {
					return fmt.Errorf("close Rex session %s: %w", matches[0].ID, err)
				}
				closedSessionID = matches[0].ID
			}

			providerOut := cmd.OutOrStdout()
			if asJSON {
				providerOut = cmd.ErrOrStderr()
			}
			if err := provider.Run(cmd.Context(), removeArgs, workdir, cmd.InOrStdin(), providerOut, cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("provider %q remove: %w", providerName, err)
			}

			if _, ok := state.Workstreams[path]; ok {
				delete(state.Workstreams, path)
				if err := state.Save(); err != nil {
					return fmt.Errorf("clean state for %s: %w", path, err)
				}
			}

			if asJSON {
				return encodeJSON(cmd, struct {
					Project         string `json:"project"`
					Branch          string `json:"branch"`
					Path            string `json:"path"`
					Provider        string `json:"provider"`
					ClosedSessionID string `json:"closed_session_id,omitempty"`
				}{project.Name, branch, path, providerName, closedSessionID})
			}
			message := fmt.Sprintf("Removed worktree %s", path)
			if closedSessionID != "" {
				message += fmt.Sprintf(" (closed Rex session %s)", closedSessionID)
			}
			fmt.Fprintln(cmd.OutOrStdout(), message)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
