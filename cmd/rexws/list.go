package main

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
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
			filter, err := parseStatusFilters(statuses)
			if err != nil {
				return err
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			state, err := config.LoadState()
			if err != nil {
				return err
			}
			inputs, err := discover(cmd.Context(), cfg)
			if err != nil {
				return err
			}

			sessions, warnings := rexSessions(cmd)
			workstreams, err := core.Build(inputs, state, sessions)
			if err != nil {
				return err
			}
			workstreams = core.FilterStatuses(workstreams, filter)

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
					ws.Project, ws.Branch, ws.WorkflowStatus, agentLabel(ws), shortSession(ws.RexSessionID), ws.Path)
			}
			return writer.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "only show these workflow statuses (repeatable or comma-separated)")
	return cmd
}

// rexSessions reads Rex state. A missing or stopped Rex server degrades to
// warnings rather than hiding the Git workstreams.
func rexSessions(cmd *cobra.Command) ([]core.Session, []string) {
	var warnings []string
	if _, err := exec.LookPath("rex"); err != nil {
		return nil, []string{fmt.Sprintf("Rex unavailable: %v", err)}
	}

	snapshot, err := rex.New().Snapshot(cmd.Context())
	if err != nil {
		return nil, []string{fmt.Sprintf("Rex unavailable: %v", err)}
	}

	sessions := make([]core.Session, 0, len(snapshot))
	for _, item := range snapshot {
		session := core.Session{
			ID:    item.ID,
			Label: item.Label,
		}
		if item.Err != nil {
			warnings = append(warnings, fmt.Sprintf("session %q: %v", item.Label, item.Err))
			session.AgentState = core.AgentUnknown
		} else {
			session.AgentState = core.AgentState(item.State)
			session.AgentDetail = item.Detail
		}
		sessions = append(sessions, session)
	}
	return sessions, warnings
}

func parseStatusFilters(values []string) ([]config.Status, error) {
	var statuses []config.Status
	for _, value := range values {
		status, err := config.ParseStatus(value)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func agentLabel(ws core.Workstream) string {
	switch {
	case ws.AgentState == core.AgentStopped:
		return "stopped"
	case ws.AgentState == core.AgentUnknown:
		return "unknown"
	case ws.AgentDetail != "":
		return fmt.Sprintf("%s (%s)", ws.AgentState, ws.AgentDetail)
	default:
		return string(ws.AgentState)
	}
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
