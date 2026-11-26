package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status <selector> <stage>",
		Short: "Set a workstream's workflow status",
		Long: "Set a workstream's workflow status. Stages are configured in config.json;\n" +
			"the stage may be given by ID or by its display label.\n" +
			"SELECTOR uses the grammar in README.md; a literal worktree path still works.",
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
			change, warnings, err := ops.SetStatusWithWarnings(cmd.Context(), cfg, args[0], status)
			writeSelectorWarnings(cmd, warnings)
			if err != nil {
				return resolveError(cfg, args[0], err)
			}

			if asJSON {
				return encodeJSON(cmd, StatusPayload{
					SchemaVersion:       SchemaVersion,
					Path:                change.Path,
					WorkflowStatus:      change.Status,
					WorkflowStatusLabel: change.Label,
					UpdatedAt:           change.UpdatedAt,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now %s\n", change.Path, change.Label)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
