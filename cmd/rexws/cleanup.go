package main

import (
	"bufio"
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

func newCleanupCmd() *cobra.Command {
	var (
		project string
		asJSON  bool
		apply   bool
		force   bool
		fix     bool
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Report stale worktrees, branches, and orphaned state",
		Long: "Report every worktree and local branch with the reason it is or is not\n" +
			"safe to remove, plus every orphan: unmatched Rex sessions, state.json\n" +
			"entries that are not current worktrees, worktrees outside their\n" +
			"project's worktree_root, and duplicate or ambiguous session labels.\n" +
			"Only labels under a registered project are considered Rex workstreams.\n" +
			"The default is\n" +
			"report-only and changes no refs, worktrees, branches, sessions, or state.\n" +
			"--apply removes only prunable worktree bookkeeping, branches merged into\n" +
			"the project's default branch, and branches whose commits are\n" +
			"patch-equivalent (rebased or cherry-picked).\n" +
			"Protected branches, locked or detached worktrees, live Rex sessions,\n" +
			"unmerged branches, and dirty worktrees are never removed without --force;\n" +
			"--force does not override protected, locked, detached, or active items and\n" +
			"prints exactly what would be lost before acting.\n" +
			"--fix prunes only state.json entries whose path provably names no current\n" +
			"Git worktree. It never kills or renames Rex sessions, registers projects,\n" +
			"or adopts sessions. It asks for confirmation on a terminal; scripts and\n" +
			"--json runs with candidates must pass --yes. --fix cannot be combined\n" +
			"with --apply or --force.\n" +
			"--project narrows the scan; --json emits the versioned report.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if fix && apply {
				return errors.New("cleanup --fix only prunes state.json; run --apply separately")
			}
			if fix && force {
				return errors.New("cleanup --fix only prunes state.json; --force does not apply")
			}
			if yes && !fix {
				return errors.New("cleanup --yes requires --fix")
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			report, err := ops.PlanCleanup(cmd.Context(), cfg, ops.CleanupOptions{
				Project: project,
				Apply:   apply,
				Force:   force,
				Fix:     fix,
			})
			if err != nil {
				return err
			}
			for _, warning := range report.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
			}

			var applyErr, fixErr error
			if apply {
				writeCleanupLosses(cmd, report)
				report, applyErr = ops.ApplyCleanup(cmd.Context(), cfg, report)
			} else if force {
				// A force preview does not act, but show the same loss report a
				// forced apply would print before acting.
				writeCleanupLosses(cmd, report)
			}
			if fix {
				confirmed, err := confirmCleanupFix(cmd, report, asJSON, yes)
				if err != nil {
					fixErr = err
				} else if confirmed {
					report, fixErr = ops.ApplyCleanupFix(cmd.Context(), cfg, report)
				}
			}

			if asJSON {
				if err := encodeJSON(cmd, report); err != nil {
					return err
				}
			} else if err := writeCleanupReport(cmd, report); err != nil {
				return err
			}
			if applyErr != nil {
				return applyErr
			}
			if fixErr != nil {
				return fixErr
			}
			return report.Err()
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "only scan this registered project")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().BoolVar(&apply, "apply", false, "remove the safe set instead of only reporting it")
	cmd.Flags().BoolVar(&force, "force", false, "also remove dirty or unmerged items, after printing what is lost")
	cmd.Flags().BoolVar(&fix, "fix", false, "prune only provably stale state.json entries, after confirmation")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm --fix without prompting (required for scripts and --json with candidates)")
	return cmd
}

func cleanupFDIsTerminal(fd uintptr) bool {
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// cleanupStdinIsTerminal is a variable so tests can exercise both the prompt
// and the non-interactive refusal without a real terminal.
var cleanupStdinIsTerminal = func(cmd *cobra.Command) bool {
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	return cleanupFDIsTerminal(file.Fd())
}

// confirmCleanupFix asks before pruning stale state entries. --yes is the
// scripted confirmation; --json always requires it so stdout stays a single
// document and no prompt can hang a pipeline. With no candidates there is
// nothing to confirm: --fix is an authorized no-op that reports fixed.
func confirmCleanupFix(cmd *cobra.Command, report ops.CleanupReport, asJSON, yes bool) (bool, error) {
	count := report.Orphans.Totals.Fixable
	if count == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "No stale state.json entries to prune.")
		return true, nil
	}
	if yes {
		return true, nil
	}
	if asJSON {
		return false, errors.New("refusing to prune state.json with --json without confirmation; rerun with --fix --yes")
	}
	if !cleanupStdinIsTerminal(cmd) {
		return false, errors.New("refusing to prune state.json without confirmation; rerun with --fix --yes")
	}

	out := cmd.ErrOrStderr()
	fmt.Fprintln(out, "Stale state.json entries to prune:")
	for _, item := range report.Orphans.Items {
		if item.Kind == ops.CleanupOrphanStaleState && item.Fixable {
			fmt.Fprintf(out, "  %s\n", render.Sanitize(item.Path))
		}
	}
	word := "entries"
	if count == 1 {
		word = "entry"
	}
	fmt.Fprintf(out, "Remove %d stale state.json %s? [y/N] ", count, word)
	// Bound the read so a device such as /dev/zero cannot grow the input, and
	// treat EOF with a complete answer as an answer.
	line, readErr := bufio.NewReader(io.LimitReader(cmd.InOrStdin(), 1024)).ReadString('\n')
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

// writeCleanupLosses prints every forced removal's exact loss before
// ApplyCleanup runs. It goes to stderr so --json keeps stdout parseable.
func writeCleanupLosses(cmd *cobra.Command, report ops.CleanupReport) {
	out := cmd.ErrOrStderr()
	for _, project := range report.Projects {
		for _, item := range project.Items {
			if !item.Removable || !item.RequiresForce {
				continue
			}
			target := item.Path
			if target == "" {
				target = item.Branch
			} else if item.Branch != "" {
				target += " [" + item.Branch + "]"
			}
			fmt.Fprintf(out, "LOSS %s\n", target)
			if item.Loss == nil {
				fmt.Fprintf(out, "  %s\n", item.Detail)
				continue
			}
			if len(item.Loss.DirtyFiles) > 0 {
				fmt.Fprintln(out, "  uncommitted, untracked, or ignored changes:")
				for _, file := range item.Loss.DirtyFiles {
					fmt.Fprintf(out, "    %s\n", file)
				}
			}
			if len(item.Loss.Commits) > 0 {
				fmt.Fprintf(out, "  commits not in %s:\n", project.DefaultBranch)
				for _, commit := range item.Loss.Commits {
					fmt.Fprintf(out, "    %s %s\n", commit.SHA, commit.Subject)
				}
			}
		}
	}
}

func writeCleanupReport(cmd *cobra.Command, report ops.CleanupReport) error {
	out := cmd.OutOrStdout()
	if len(report.Projects) == 0 {
		if len(report.Errors) == 0 {
			fmt.Fprintln(out, "No registered projects. Register a repository with: rexws project add <path>")
		}
	} else {
		writer := newTabWriter(cmd)
		if report.Applied {
			fmt.Fprintln(writer, "PROJECT\tCLASSIFICATION\tBRANCH\tPATH\tRESULT\tREASON")
		} else {
			fmt.Fprintln(writer, "PROJECT\tCLASSIFICATION\tBRANCH\tPATH\tREASON")
		}
		for _, project := range report.Projects {
			for _, item := range project.Items {
				reason := item.Detail
				if item.Error != "" {
					reason += "; error: " + item.Error
				}
				if report.Applied {
					fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
						item.Project, item.Classification, cleanupBranchColumn(item), cleanupPathColumn(item), item.Result, reason)
				} else {
					fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
						item.Project, item.Classification, cleanupBranchColumn(item), cleanupPathColumn(item), reason)
				}
			}
		}
		if err := writer.Flush(); err != nil {
			return err
		}

		totals := report.Totals
		if report.Applied {
			fmt.Fprintf(out, "Scanned %d item(s): %d removed, %d failed, %d kept.\n",
				totals.Scanned, totals.Removed, totals.Failed, totals.Kept)
		} else {
			fmt.Fprintf(out, "Scanned %d item(s): %d safe, %d removable with the current flags, %d kept.\n",
				totals.Scanned, totals.Safe, totals.Removable, totals.Kept)
			if report.Orphans.Fixed {
				fmt.Fprintln(out, "State fix only: no worktrees or branches were changed.")
			} else if totals.Removable == 0 {
				fmt.Fprintln(out, "Preview only: nothing was changed; no items are removable with the current flags.")
			} else {
				fmt.Fprintf(out, "Preview only: nothing was changed. Rerun with --apply to remove %d item(s).\n", totals.Removable)
			}
			if !report.Force {
				if needsForce := cleanupForceCount(report); needsForce > 0 {
					fmt.Fprintf(out, "%d item(s) require --force (dirty or unmerged); --force prints what would be lost first.\n", needsForce)
				}
			}
		}
	}
	return writeCleanupOrphans(cmd, report)
}

func writeCleanupOrphans(cmd *cobra.Command, report ops.CleanupReport) error {
	out := cmd.OutOrStdout()
	orphans := report.Orphans
	if len(orphans.Items) == 0 {
		if orphans.Fixed {
			fmt.Fprintln(out, "Orphans: none; no stale state.json entries were pruned.")
		} else {
			fmt.Fprintln(out, "Orphans: none.")
		}
		return nil
	}

	writer := newTabWriter(cmd)
	if orphans.Fixed {
		fmt.Fprintln(writer, "ORPHAN\tPROJECT\tLABEL\tPATH\tFIXABLE\tRESULT\tREASON")
	} else {
		fmt.Fprintln(writer, "ORPHAN\tPROJECT\tLABEL\tPATH\tFIXABLE\tREASON")
	}
	for _, item := range orphans.Items {
		reason := render.Sanitize(item.Detail)
		if item.Error != "" {
			reason += "; error: " + render.Sanitize(item.Error)
		}
		project := orphanColumn(render.Sanitize(item.Project))
		label := orphanColumn(render.Sanitize(item.Label))
		path := orphanColumn(render.Sanitize(item.Path))
		fixable := "no"
		if item.Fixable {
			fixable = "yes"
		}
		if orphans.Fixed {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				item.Kind, project, label, path, fixable, orphanColumn(string(item.Result)), reason)
		} else {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
				item.Kind, project, label, path, fixable, reason)
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(out, "Orphans: %d scanned (%s); %d fixable.\n",
		orphans.Totals.Scanned, orphanKindSummary(orphans.Totals.ByKind), orphans.Totals.Fixable)
	if orphans.Fixed {
		fmt.Fprintf(out, "State fix: %d removed, %d failed, %d retained.\n",
			orphans.Totals.Removed, orphans.Totals.Failed, orphans.Totals.Kept)
	} else if orphans.Totals.Fixable > 0 {
		fmt.Fprintln(out, "Rerun with --fix to prune stale state.json entries after confirmation.")
	}
	return nil
}

func orphanColumn(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func orphanKindSummary(counts map[string]int) string {
	kinds := []struct {
		key   string
		label string
	}{
		{string(ops.CleanupOrphanUnmatchedSession), "unmatched sessions"},
		{string(ops.CleanupOrphanStaleState), "stale state"},
		{string(ops.CleanupOrphanUnregisteredWorktree), "unregistered worktrees"},
		{string(ops.CleanupOrphanSessionLabelConflict), "session label conflicts"},
	}
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s %d", kind.label, counts[kind.key]))
	}
	return strings.Join(parts, ", ")
}

func cleanupForceCount(report ops.CleanupReport) int {
	count := 0
	for _, project := range report.Projects {
		for _, item := range project.Items {
			if item.RequiresForce {
				count++
			}
		}
	}
	return count
}

func cleanupBranchColumn(item ops.CleanupItem) string {
	if item.Branch != "" {
		return item.Branch
	}
	if item.Detached {
		head := item.Head
		if len(head) > 12 {
			head = head[:12]
		}
		return "(detached " + head + ")"
	}
	return "-"
}

func cleanupPathColumn(item ops.CleanupItem) string {
	if item.Path == "" {
		return "-"
	}
	return item.Path
}
