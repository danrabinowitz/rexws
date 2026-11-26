package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func newSwitchCmd() *cobra.Command {
	var (
		client string
		window string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "switch <selector>",
		Short: "Show (or create) a workstream's Rex session in the app",
		Long: "Show the Rex session for the workstream named by SELECTOR in the Rex app.\n" +
			"SELECTOR uses the grammar in README.md; <project>/<branch> remains the exact form.\n" +
			"When no such session exists, create it in the worktree directory and then show it.\n" +
			"--window selects a window inside an existing session, by label or window ID.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			candidate, err := ops.ResolveWorkstream(cmd.Context(), cfg, args[0])
			if err != nil {
				return resolveError(cfg, args[0], err)
			}

			selectedClient := client
			if selectedClient == "" {
				selectedClient = cfg.RexAppClient()
			}
			result, err := ops.SwitchWorkstreamWithClient(cmd.Context(), cfg, selectedClient, candidate.SessionName, candidate.Path, window)
			if err != nil {
				return err
			}
			if asJSON {
				return encodeJSON(cmd, SwitchPayload{
					SchemaVersion: SchemaVersion,
					SessionName:   candidate.SessionName,
					SessionID:     result.SessionID,
					WindowID:      result.WindowID,
					WindowLabel:   result.WindowLabel,
					Created:       result.Created,
				})
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
