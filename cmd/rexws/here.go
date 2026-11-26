package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func newHereCmd() *cobra.Command {
	var (
		dir    string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "here",
		Short: "Show the workstream for the current directory",
		Long: "Show the workstream whose worktree contains the current directory, including\n" +
			"any subdirectory of it. --dir resolves from another directory instead.\n" +
			"Outside every registered worktree this fails with exit code 4; it never\n" +
			"prints an empty result.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("dir") && dir == "" {
				return errors.New("--dir must name a directory")
			}
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			candidate, discovery, warnings, err := ops.ResolveHereWithWarnings(cmd.Context(), cfg, dir)
			if err != nil {
				writeSelectorWarnings(cmd, warnings)
				return err
			}
			state, err := config.LoadState(cfg.StatusIDs())
			if err != nil {
				return err
			}
			workstreams, buildWarnings := core.BuildWithWarnings(discovery.Inputs, state, nil, cfg)
			writeSelectorWarnings(cmd, buildWarnings)
			workstream, ok := workstreamByPath(workstreams, candidate.Path)
			if !ok {
				return fmt.Errorf("resolved workstream %s is not among the discovered worktrees", candidate.Path)
			}
			selector := ops.ContextSelector(workstreams, workstream)

			if asJSON {
				return encodeJSON(cmd, HerePayload{
					SchemaVersion:       SchemaVersion,
					Project:             workstream.Project,
					Branch:              workstream.Branch,
					Path:                workstream.Path,
					SessionName:         workstream.SessionName,
					WorkflowStatus:      workstream.WorkflowStatus,
					WorkflowStatusLabel: workstream.WorkflowStatusLabel,
					Selector:            selector,
					Detached:            workstream.Detached,
				})
			}

			writer := newTabWriter(cmd)
			fmt.Fprintf(writer, "project\t%s\n", workstream.Project)
			fmt.Fprintf(writer, "branch\t%s\n", workstream.Branch)
			fmt.Fprintf(writer, "path\t%s\n", workstream.Path)
			fmt.Fprintf(writer, "session\t%s\n", workstream.SessionName)
			fmt.Fprintf(writer, "status\t%s\n", workstream.WorkflowStatusLabel)
			fmt.Fprintf(writer, "selector\t%s\n", selector)
			return writer.Flush()
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "resolve from this directory instead of the current one (a leading ~/ expands to home)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func workstreamByPath(workstreams []core.Workstream, path string) (core.Workstream, bool) {
	for _, workstream := range workstreams {
		if workstream.Path == path {
			return workstream, true
		}
	}
	return core.Workstream{}, false
}
