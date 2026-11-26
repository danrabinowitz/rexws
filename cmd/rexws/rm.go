package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func newRmCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "rm <selector>",
		Short: "Remove a workstream's worktree and Rex session",
		Long: "Close the worktree's Rex session if one is running, run the project's\n" +
			"provider remove command, and delete its workflow state.\n" +
			"SELECTOR uses the grammar in README.md; a literal worktree path still works.\n" +
			"The project's main worktree is refused.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			providerOut := cmd.OutOrStdout()
			if asJSON {
				providerOut = cmd.ErrOrStderr()
			}
			plan, err := ops.PrepareRemove(cmd.Context(), cfg, args[0])
			if err != nil {
				return resolveError(cfg, args[0], err)
			}
			for _, warning := range plan.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
			}
			fmt.Fprintf(providerOut, "Removing %s (%s)\n", plan.SessionName, plan.Path)

			result, err := ops.ExecuteRemove(cmd.Context(), cfg, plan, cmd.InOrStdin(), providerOut, cmd.ErrOrStderr())
			if err != nil {
				return err
			}

			if asJSON {
				return encodeJSON(cmd, struct {
					Project         string `json:"project"`
					Branch          string `json:"branch"`
					Path            string `json:"path"`
					Provider        string `json:"provider"`
					ClosedSessionID string `json:"closed_session_id,omitempty"`
				}{result.Project, result.Branch, result.Path, result.Provider, result.ClosedSessionID})
			}
			message := fmt.Sprintf("Removed worktree %s", result.Path)
			if result.ClosedSessionID != "" {
				message += fmt.Sprintf(" (closed Rex session %s)", result.ClosedSessionID)
			}
			fmt.Fprintln(cmd.OutOrStdout(), message)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
