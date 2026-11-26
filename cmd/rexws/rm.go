package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
	"github.com/danrabinowitz/rexws/internal/render"
)

// rmJSON is the stable result document for rm --json. Error is set on a
// partial failure after the worktree was already removed, so stdout still
// carries the retained-branch report and the reason.
type rmJSON struct {
	Project          string               `json:"project"`
	Branch           string               `json:"branch"`
	Path             string               `json:"path"`
	Provider         string               `json:"provider"`
	ClosedSessionID  string               `json:"closed_session_id,omitempty"`
	DeletedBranch    string               `json:"deleted_branch,omitempty"`
	RetainedBranches []ops.RetainedBranch `json:"retained_branches"`
	Retained         *ops.RemoveRetained  `json:"retained,omitempty"`
	Warnings         []string             `json:"warnings,omitempty"`
	Loss             *ops.RemoveLoss      `json:"loss,omitempty"`
	Error            string               `json:"error,omitempty"`
}

func newRmJSON(result ops.RemoveResult, force bool, err error) rmJSON {
	doc := rmJSON{
		Project:          result.Project,
		Branch:           result.Branch,
		Path:             result.Path,
		Provider:         result.Provider,
		ClosedSessionID:  result.ClosedSessionID,
		DeletedBranch:    result.DeletedBranch,
		RetainedBranches: result.RetainedBranches,
		Retained:         result.Retained,
		Warnings:         result.Warnings,
	}
	if force {
		doc.Loss = &result.Loss
	}
	if err != nil {
		doc.Error = err.Error()
	}
	return doc
}

func newRmCmd() *cobra.Command {
	var (
		asJSON       bool
		force        bool
		deleteBranch bool
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "rm <selector>",
		Short: "Remove a workstream's worktree and Rex session",
		Long: "Close the worktree's Rex session if one is running, run the project's\n" +
			"provider remove command, and delete its workflow state.\n" +
			"SELECTOR uses the grammar in README.md; a literal worktree path still works.\n" +
			"The project's main worktree and a dirty or detached worktree are refused.\n" +
			"--force removes a dirty or detached worktree after printing exactly what is\n" +
			"discarded: uncommitted, untracked, and ignored paths, and unique detached-\n" +
			"HEAD commits. It asks for confirmation on a terminal; scripts must pass\n" +
			"--yes, and --json always requires --yes so stdout stays a single document.\n" +
			"Unmerged commits and unpushed commits on the retained branch are reported\n" +
			"separately; they are not losses and do not prompt.\n" +
			"--delete-branch deletes the branch after the worktree, but only when the\n" +
			"branch is merged or patch-equivalent to the project's default branch. An\n" +
			"unmerged branch is never deleted, even with --force. Remote branches are\n" +
			"never touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if yes && !force && !deleteBranch {
				return errors.New("rm --yes requires --force or --delete-branch")
			}
			if asJSON && force && !yes {
				return errors.New("refusing forced removal with --json without confirmation; rerun with --force --yes")
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			providerOut := cmd.OutOrStdout()
			if asJSON {
				providerOut = cmd.ErrOrStderr()
			}
			plan, err := ops.PrepareRemoveWithOptions(cmd.Context(), cfg, args[0], ops.RemoveOptions{
				Force:        force,
				DeleteBranch: deleteBranch,
			})
			if err != nil {
				return resolveError(cfg, args[0], err)
			}
			for _, warning := range plan.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
			}

			// A forced removal states its consequences before anything runs.
			// The report goes to stderr so --json keeps stdout parseable.
			if force {
				writeRemovePlan(cmd, plan)
			}
			if !yes && plan.RequiresConfirmation() {
				confirmed, err := confirmRemove(cmd, plan)
				if err != nil {
					return err
				}
				if !confirmed {
					return nil
				}
			}

			fmt.Fprintf(providerOut, "Removing %s (%s)\n", plan.SessionName, plan.Path)

			result, err := ops.ExecuteRemove(cmd.Context(), cfg, plan, cmd.InOrStdin(), providerOut, cmd.ErrOrStderr())
			if err != nil {
				// A partial failure after the worktree was removed still emits
				// the structured result so a script can see the retained
				// branch and the reason.
				if asJSON && result.Path != "" {
					if encodeErr := encodeJSON(cmd, newRmJSON(result, force, err)); encodeErr != nil {
						return encodeErr
					}
				}
				writeRetainedBranches(cmd.ErrOrStderr(), result.RetainedBranches)
				writeRemoveWarnings(cmd.ErrOrStderr(), result.Warnings)
				return err
			}

			if asJSON {
				return encodeJSON(cmd, newRmJSON(result, force, nil))
			}
			message := fmt.Sprintf("Removed worktree %s", result.Path)
			if result.ClosedSessionID != "" {
				message += fmt.Sprintf(" (closed Rex session %s)", result.ClosedSessionID)
			}
			fmt.Fprintln(cmd.OutOrStdout(), message)
			if result.DeletedBranch != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted branch %s.\n", result.DeletedBranch)
			}
			writeRetainedBranches(cmd.OutOrStdout(), result.RetainedBranches)
			writeRemoveWarnings(cmd.ErrOrStderr(), result.Warnings)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().BoolVar(&force, "force", false, "remove a dirty or detached worktree after printing exactly what is discarded; asks for confirmation")
	cmd.Flags().BoolVar(&deleteBranch, "delete-branch", false, "delete the branch after the worktree, only when merged or patch-equivalent to the default branch")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm a forced removal without prompting (required for scripts and --json)")
	return cmd
}

func removeFDIsTerminal(fd uintptr) bool {
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// removeStdinIsTerminal is a variable so tests can exercise both the prompt
// and the non-interactive refusal without a real terminal.
var removeStdinIsTerminal = func(cmd *cobra.Command) bool {
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	return removeFDIsTerminal(file.Fd())
}

// writeRemovePlan prints what is discarded, what the retained branch keeps,
// and the exact list of actions a forced removal would apply. It goes to
// stderr so --json keeps stdout a single document.
func writeRemovePlan(cmd *cobra.Command, plan ops.RemovePlan) {
	out := cmd.ErrOrStderr()
	fmt.Fprintln(out, "LOSS")
	for _, line := range plan.LossLines() {
		fmt.Fprintln(out, "  "+render.Sanitize(line))
	}
	if lines := plan.RetainedLines(); len(lines) > 0 {
		fmt.Fprintln(out, "RETAINED")
		for _, line := range lines {
			fmt.Fprintln(out, "  "+render.Sanitize(line))
		}
	}
	fmt.Fprintln(out, "ACTIONS")
	for _, line := range plan.ActionLines() {
		fmt.Fprintln(out, "  "+render.Sanitize(line))
	}
}

// confirmRemove asks before a forced removal discards data. --yes is handled
// by the caller; --json runs cannot reach this prompt because they require
// --yes. A forced removal that loses nothing is not confirmed.
func confirmRemove(cmd *cobra.Command, plan ops.RemovePlan) (bool, error) {
	if !removeStdinIsTerminal(cmd) {
		return false, errors.New("refusing forced removal without confirmation; rerun with --force --yes")
	}

	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "Remove worktree %s and accept this loss? [y/N] ", render.Sanitize(plan.Path))
	line, readErr := readPromptLine(cmd.InOrStdin(), 1024)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Nothing was changed.")
		return false, nil
	}
	answer := strings.TrimSpace(line)
	if answer == "" && errors.Is(readErr, io.EOF) {
		fmt.Fprintln(out)
	}
	if strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
		return true, nil
	}
	fmt.Fprintln(out, "Nothing was changed.")
	return false, nil
}

// readPromptLine reads one line without buffering past it, so a provider that
// later reads stdin still sees the remaining bytes. It is bounded so a device
// such as /dev/zero cannot grow the input.
func readPromptLine(r io.Reader, limit int) (string, error) {
	var b strings.Builder
	var one [1]byte
	for b.Len() < limit {
		if _, err := io.ReadFull(r, one[:]); err != nil {
			return b.String(), err
		}
		if one[0] == '\n' {
			return b.String(), nil
		}
		b.WriteByte(one[0])
	}
	return b.String(), nil
}

func writeRetainedBranches(out io.Writer, branches []ops.RetainedBranch) {
	for _, branch := range branches {
		if branch.Error != "" {
			fmt.Fprintf(out, "Retained branch %s: state unknown (%s).\n", branch.Branch, render.Sanitize(branch.Error))
			continue
		}
		switch branch.State {
		case ops.CleanupBranchMerged:
			fmt.Fprintf(out, "Retained branch %s: merged into %s; no unmerged commits.\n", branch.Branch, branch.DefaultBranch)
		case ops.CleanupBranchPatchEquivalent:
			fmt.Fprintf(out, "Retained branch %s: patch-equivalent to %s; its changes are already there.\n", branch.Branch, branch.DefaultBranch)
		case ops.CleanupBranchUnmerged:
			fmt.Fprintf(out, "Retained branch %s: unmerged, %d commit(s) not in %s.\n", branch.Branch, len(branch.Commits), branch.DefaultBranch)
		default:
			fmt.Fprintf(out, "Retained branch %s.\n", branch.Branch)
		}
	}
}

func writeRemoveWarnings(out io.Writer, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintln(out, "warning:", render.Sanitize(warning))
	}
}
