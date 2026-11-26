package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/render"
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

			rexState := ops.ReadRex(cmd.Context(), cfg)
			warnings := rexState.Warnings
			if rexState.Err != nil {
				warnings = []string{fmt.Sprintf("Rex unavailable: %v", rexState.Err)}
			}
			workstreams, err := core.Build(inputs, state, rexState.Sessions, cfg)
			if err != nil {
				return err
			}
			workstreams = core.FilterStatuses(workstreams, filter)

			for _, warning := range warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
			}

			if asJSON {
				return encodeJSON(cmd, ListPayload{
					SchemaVersion: SchemaVersion,
					Workstreams:   workstreams,
					Warnings:      warnings,
				})
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
