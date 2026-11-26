// Command rexws is a Git worktree workspace manager for Rex.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/selector"
	"github.com/danrabinowitz/rexws/internal/tui"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rexws:", err)
		os.Exit(exitCode(err))
	}
}

// exitCode gives selector failures stable, scriptable exit codes. Everything
// else stays 1.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, selector.ErrNoActiveWorkstream):
		return 4
	case errors.Is(err, selector.ErrAmbiguous):
		return 3
	case errors.Is(err, selector.ErrNoMatch):
		return 2
	default:
		return 1
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "rexws",
		Short:         "Manage Git worktree workstreams in Rex",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.Run()
		},
	}
	cmd.AddCommand(
		newTuiCmd(),
		newListCmd(),
		newHereCmd(),
		newProjectCmd(),
		newStatusCmd(),
		newSwitchCmd(),
		newNewCmd(),
		newRmCmd(),
	)
	return cmd
}

func newTuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch the interactive dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tui.Run()
		},
	}
}

// discover reads the worktrees of every registered project. The CLI treats a
// project that cannot be read as fatal; the TUI uses ops.Discover directly and
// keeps the readable projects.
func discover(ctx context.Context, cfg config.Config) ([]core.Input, error) {
	discovery := ops.Discover(ctx, cfg)
	if err := discovery.Err(); err != nil {
		return nil, err
	}
	return discovery.Inputs, nil
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
