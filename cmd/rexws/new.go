package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

type newPayload struct {
	Project        string `json:"project"`
	Branch         string `json:"branch"`
	Path           string `json:"path"`
	Provider       string `json:"provider"`
	SessionID      string `json:"session_id,omitempty"`
	SessionCreated *bool  `json:"session_created,omitempty"`
}

func newNewCmd() *cobra.Command {
	var (
		asJSON   bool
		switchTo bool
		client   string
	)
	cmd := &cobra.Command{
		Use:   "new <project> <branch>",
		Short: "Create a worktree with the project's provider",
		Long: "Create a worktree for a new branch using the project's configured provider.\n" +
			"The target path is <worktree_root>/<branch> when the project sets worktree_root,\n" +
			"otherwise <repo>-wt/<branch> beside the repository.\n" +
			"With --switch, also create or select the Rex session labeled <project>/<branch>\n" +
			"in the new worktree. A live session with that label is selected instead of\n" +
			"creating a second one. --client chooses the Rex app client for the switch.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if client != "" && !switchTo {
				return fmt.Errorf("--client requires --switch")
			}
			projectName, branch := args[0], args[1]

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			// Resolve the app client before creating anything so a missing app
			// cannot leave a worktree or an unselected session behind.
			selectedClient := ""
			if switchTo {
				selectedClient, err = ops.ResolveAppClient(cmd.Context(), cfg, client)
				if err != nil {
					return fmt.Errorf("--switch needs a Rex app client: %w", err)
				}
			}

			providerOut := cmd.OutOrStdout()
			if asJSON {
				providerOut = cmd.ErrOrStderr()
			}
			result, err := ops.CreateWorktree(cmd.Context(), cfg, projectName, branch, cmd.InOrStdin(), providerOut, cmd.ErrOrStderr())
			if err != nil {
				return err
			}

			var (
				sessionID      string
				sessionCreated bool
			)
			if switchTo {
				sessionName := result.Project + "/" + result.Branch
				switched, err := ops.SwitchWorkstreamWithClient(cmd.Context(), cfg, selectedClient, sessionName, result.Path, "")
				if err != nil {
					return fmt.Errorf("created worktree %s at %s, but could not open Rex session %q: %w; run `rexws switch %s` to retry",
						result.Branch, result.Path, sessionName, err, sessionName)
				}
				sessionID = switched.SessionID
				sessionCreated = switched.Created
			}

			if asJSON {
				payload := newPayload{
					Project:  result.Project,
					Branch:   result.Branch,
					Path:     result.Path,
					Provider: result.Provider,
				}
				if switchTo {
					payload.SessionID = sessionID
					created := sessionCreated
					payload.SessionCreated = &created
				}
				return encodeJSON(cmd, payload)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created worktree %s at %s\n", result.Branch, result.Path)
			if switchTo {
				if sessionCreated {
					fmt.Fprintf(cmd.OutOrStdout(), "Created and switched to Rex session %s\n", sessionID)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Switched to Rex session %s\n", sessionID)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().BoolVar(&switchTo, "switch", false, "create or select the Rex session <project>/<branch> after creating the worktree")
	cmd.Flags().StringVar(&client, "client", "", "Rex app client label or ID for --switch (default: rex_client config, then the first connected Rex app)")
	return cmd
}
