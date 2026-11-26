package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/render"
	"github.com/danrabinowitz/rexws/internal/rex"
)

func newListCmd() *cobra.Command {
	var (
		asJSON   bool
		statuses []string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workstreams with workflow and Rex state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			filter, err := parseStatusFilters(cfg, statuses)
			if err != nil {
				return err
			}
			state, err := config.LoadState(cfg.StatusIDs())
			if err != nil {
				return err
			}
			inputs, err := discover(cmd.Context(), cfg)
			if err != nil {
				return err
			}

			sessions, warnings := rexSessions(cmd, rex.New(cfg.RexBinary()))
			workstreams, err := core.Build(inputs, state, sessions, cfg.DefaultStatusValue())
			if err != nil {
				return err
			}
			workstreams = core.FilterStatuses(workstreams, filter)
			for i := range workstreams {
				workstreams[i].WorkflowStatusLabel = cfg.StatusLabel(workstreams[i].WorkflowStatus)
			}

			for _, warning := range warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
			}

			if asJSON {
				return encodeJSON(cmd, struct {
					Workstreams []core.Workstream `json:"workstreams"`
					Warnings    []string          `json:"warnings,omitempty"`
				}{workstreams, warnings})
			}

			if len(workstreams) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No workstreams found. Register a repository with: rexws project add <path>")
				return nil
			}

			writer := newTabWriter(cmd)
			fmt.Fprintln(writer, "PROJECT\tBRANCH\tSTATUS\tAGENT\tSESSION\tPATH")
			for _, ws := range workstreams {
				fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
					ws.Project, ws.Branch, ws.WorkflowStatusLabel, render.Default.Format(ws), shortSession(ws.RexSessionID), ws.Path)
			}
			return writer.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "only show these workflow statuses by ID or label (repeatable or comma-separated)")
	return cmd
}

// rexSessions reads Rex state. A missing or stopped Rex server degrades to
// warnings rather than hiding the Git workstreams.
func rexSessions(cmd *cobra.Command, client *rex.Client) ([]core.Session, []string) {
	snapshot, err := client.Snapshot(cmd.Context())
	if err != nil {
		return nil, []string{fmt.Sprintf("Rex unavailable: %v", err)}
	}
	return coreSessions(snapshot)
}

// coreSessions converts Rex's per-block view into the core model. A session
// whose blocks could not be listed is unreadable; a block whose status could
// not be read becomes an unknown agent with its error preserved.
func coreSessions(snapshot []rex.SessionStatus) ([]core.Session, []string) {
	var warnings []string
	sessions := make([]core.Session, 0, len(snapshot))
	for _, item := range snapshot {
		session := core.Session{ID: item.ID, Label: item.Label}
		if item.Err != nil {
			session.Unreadable = true
			warnings = append(warnings, fmt.Sprintf("session %q: %v", item.Label, item.Err))
			sessions = append(sessions, session)
			continue
		}
		session.Agents = make([]core.Agent, 0, len(item.Blocks))
		for _, block := range item.Blocks {
			agent := core.Agent{
				BlockID:      block.BlockID,
				BlockLabel:   block.BlockLabel,
				WindowID:     block.WindowID,
				WindowLabel:  block.WindowLabel,
				App:          block.App,
				EffectiveApp: block.EffectiveApp,
				State:        block.State,
				Detail:       block.Detail,
				Msg:          block.Msg,
				Title:        block.Title,
				UpdatedAt:    block.UpdatedAt,
			}
			if block.Err != nil {
				agent.State = core.AgentUnknown
				agent.Error = block.Err.Error()
				warnings = append(warnings, fmt.Sprintf("session %q %s: %v", item.Label, agentLocation(block), block.Err))
			}
			session.Agents = append(session.Agents, agent)
		}
		sessions = append(sessions, session)
	}
	return sessions, warnings
}

// agentLocation names the pane a status warning is about.
func agentLocation(block rex.BlockStatus) string {
	window := block.WindowLabel
	if window == "" {
		window = shortSession(block.WindowID)
	}
	label := block.BlockLabel
	if label == "" {
		label = shortSession(block.BlockID)
	}
	return fmt.Sprintf("window %q block %q", window, label)
}

func parseStatusFilters(cfg config.Config, values []string) ([]config.Status, error) {
	var statuses []config.Status
	for _, value := range values {
		status, err := cfg.ParseStatus(value)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func shortSession(id string) string {
	if id == "" {
		return "-"
	}
	const width = 6
	if len(id) > width {
		return id[len(id)-width:]
	}
	return id
}
