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
		window string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "switch <project/branch>",
		Short: "Show (or create) a workstream's Rex session in the app",
		Long: "Show the Rex session labeled <project>/<branch> in the Rex app.\n" +
			"When no such session exists, create it in the worktree directory and then show it.\n" +
			"--window selects a window inside an existing session, by label or window ID.",
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
			branch := strings.TrimPrefix(name, projectName+"/")
			target, ok := findWorktree(worktrees, project.Name, name)
			if !ok {
				return fmt.Errorf("project %q has no worktree for branch %q; create it with `rexws new %s %s`", projectName, branch, projectName, branch)
			}
			path, err := pathutil.Canonical(target.Path)
			if err != nil {
				return err
			}

			selectedClient := client
			if selectedClient == "" {
				selectedClient = cfg.RexAppClient()
			}
			result, err := rex.New(cfg.RexBinary()).Switch(cmd.Context(), name, path, selectedClient, window)
			if err != nil {
				return err
			}
			if asJSON {
				return encodeJSON(cmd, struct {
					SessionName string `json:"session_name"`
					SessionID   string `json:"session_id"`
					WindowID    string `json:"window_id,omitempty"`
					WindowLabel string `json:"window_label,omitempty"`
					Created     bool   `json:"created"`
				}{name, result.SessionID, result.WindowID, result.WindowLabel, result.Created})
			}
			if result.Created {
				fmt.Fprintf(cmd.OutOrStdout(), "Created Rex session %s and switched to it\n", result.SessionID)
			} else if result.WindowID != "" {
				label := result.WindowLabel
				if label == "" {
					label = result.WindowID
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Switched to Rex session %s (window %s)\n", result.SessionID, label)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Switched to Rex session %s\n", result.SessionID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", "", "Rex client label or ID (default: rex_client config, then the first connected Rex app)")
	cmd.Flags().StringVar(&window, "window", "", "window label or ID to select (requires an existing session)")
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
