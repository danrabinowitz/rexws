package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
	"github.com/danrabinowitz/rexws/internal/rex"
)

func newSwitchCmd() *cobra.Command {
	var (
		client string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "switch <project/branch>",
		Short: "Show (or create) a workstream's Rex session in the app",
		Long: "Show the Rex session labeled <project>/<branch> in the Rex app.\n" +
			"When no such session exists, create it in the worktree directory and then show it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			projectName, _, split := strings.Cut(name, "/")
			if !split || projectName == "" {
				return fmt.Errorf("want <project>/<branch>, got %q", name)
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			project, ok := cfg.Project(projectName)
			if !ok {
				return fmt.Errorf("no registered project %q (registered: %s)", projectName, projectNames(cfg))
			}
			worktrees, err := git.ListWorktrees(cmd.Context(), project.Path)
			if err != nil {
				return err
			}
			target, ok := findWorktree(worktrees, project.Name, name)
			if !ok {
				return fmt.Errorf("project %q has no worktree for branch %q", projectName, strings.TrimPrefix(name, projectName+"/"))
			}
			path, err := pathutil.Canonical(target.Path)
			if err != nil {
				return err
			}

			result, err := rex.New().Switch(cmd.Context(), name, path, client)
			if err != nil {
				return err
			}
			if asJSON {
				return encodeJSON(cmd, struct {
					SessionName string `json:"session_name"`
					SessionID   string `json:"session_id"`
					Created     bool   `json:"created"`
				}{name, result.SessionID, result.Created})
			}
			if result.Created {
				fmt.Fprintf(cmd.OutOrStdout(), "Created Rex session %s and switched to it\n", result.SessionID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Switched to Rex session %s\n", result.SessionID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", "", "Rex client label or ID (default: first connected Rex app)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// findWorktree matches a worktree by its <project>/<branch> identity, using
// the short HEAD for detached worktrees.
func findWorktree(worktrees []git.Worktree, project, sessionName string) (git.Worktree, bool) {
	for _, wt := range worktrees {
		if wt.Bare || wt.Prunable {
			continue
		}
		branch := wt.Branch
		if branch == "" {
			branch = wt.ShortHead()
		}
		if project+"/"+branch == sessionName {
			return wt, true
		}
	}
	return git.Worktree{}, false
}

// knownWorktree is defined in status.go: status is its only caller.

func projectNames(cfg config.Config) string {
	names := make([]string, 0, len(cfg.Projects))
	for _, project := range cfg.SortedProjects() {
		names = append(names, project.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
