// Command rexws is a Git worktree workspace manager for Rex.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/git"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rexws:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "rexws",
		Short:         "Manage Git worktree workstreams in Rex",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.ErrOrStderr(), "The interactive dashboard arrives in Milestone 4. Use `rexws list` for now.")
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newListCmd(),
		newProjectCmd(),
		newStatusCmd(),
		newSwitchCmd(),
		newNewCmd(),
		newRmCmd(),
	)
	return cmd
}

// discover reads the worktrees of every registered project.
func discover(ctx context.Context, cfg config.Config) ([]core.Input, error) {
	inputs := make([]core.Input, 0, len(cfg.Projects))
	for _, project := range cfg.SortedProjects() {
		worktrees, err := git.ListWorktrees(ctx, project.Path)
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", project.Name, err)
		}
		inputs = append(inputs, core.Input{Project: project, Worktrees: worktrees})
	}
	return inputs, nil
}

// encodeJSON writes an indented JSON document followed by a newline.
func encodeJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// newTabWriter returns a tab writer matching the CLI's table style.
func newTabWriter(cmd *cobra.Command) *tabwriter.Writer {
	return tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
}
