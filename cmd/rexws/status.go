package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status <path> <stage>",
		Short: "Set a workstream's workflow status",
		Long: "Set a workstream's workflow status. Stages are configured in config.json;\n" +
			"the stage may be given by ID or by its display label.\n" +
			"PATH is the worktree path shown by `rexws list`.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			status, err := cfg.ParseStatus(args[1])
			if err != nil {
				return err
			}
			path, err := pathutil.Canonical(args[0])
			if err != nil {
				return err
			}

			inputs, err := discover(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			known, err := knownWorktree(inputs, path)
			if err != nil {
				return err
			}
			if !known {
				return fmt.Errorf("%s is not a worktree of a registered project; run `rexws list` to see known paths", path)
			}

			state, err := config.LoadState(cfg.StatusIDs())
			if err != nil {
				return err
			}
			stored := state.SetStatus(path, status, time.Now())
			if err := state.Save(); err != nil {
				return err
			}
			label := cfg.StatusLabel(stored.Status)

			if asJSON {
				return encodeJSON(cmd, struct {
					Path                string        `json:"path"`
					WorkflowStatus      config.Status `json:"workflow_status"`
					WorkflowStatusLabel string        `json:"workflow_status_label"`
					UpdatedAt           time.Time     `json:"updated_at"`
				}{path, stored.Status, label, stored.UpdatedAt})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now %s\n", path, label)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// knownWorktree reports whether path belongs to the discovered worktrees.
func knownWorktree(inputs []core.Input, path string) (bool, error) {
	for _, input := range inputs {
		for _, wt := range input.Worktrees {
			if wt.Bare || wt.Prunable {
				continue
			}
			candidate, err := pathutil.Canonical(wt.Path)
			if err != nil {
				return false, err
			}
			if candidate == path {
				return true, nil
			}
		}
	}
	return false, nil
}
