package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func newCleanupCmd() *cobra.Command {
	var (
		project string
		asJSON  bool
		apply   bool
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Report and remove stale worktrees and branches",
		Long: "Report every worktree and local branch with the reason it is or is not\n" +
			"safe to remove. The default is report-only and changes no refs,\n" +
			"worktrees, branches, or state.\n" +
			"--apply removes only prunable worktree bookkeeping, branches merged into\n" +
			"the project's default branch, and branches whose commits are\n" +
			"patch-equivalent (rebased or cherry-picked).\n" +
			"Protected branches, locked or detached worktrees, live Rex sessions,\n" +
			"unmerged branches, and dirty worktrees are never removed without --force;\n" +
			"--force does not override protected, locked, detached, or active items and\n" +
			"prints exactly what would be lost before acting. --project narrows the scan;\n" +
			"--json emits the schema_version 1 report.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			report, err := ops.PlanCleanup(cmd.Context(), cfg, ops.CleanupOptions{
				Project: project,
				Apply:   apply,
				Force:   force,
			})
			if err != nil {
				return err
			}
			for _, warning := range report.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
			}

			var applyErr error
			if apply {
				writeCleanupLosses(cmd, report)
				report, applyErr = ops.ApplyCleanup(cmd.Context(), cfg, report)
			} else if force {
				// A force preview does not act, but show the same loss report a
				// forced apply would print before acting.
				writeCleanupLosses(cmd, report)
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
			return report.Err()
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "only scan this registered project")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().BoolVar(&apply, "apply", false, "remove the safe set instead of only reporting it")
	cmd.Flags().BoolVar(&force, "force", false, "also remove dirty or unmerged items, after printing what is lost")
	return cmd
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
	if len(report.Projects) == 0 {
		if len(report.Errors) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No registered projects. Register a repository with: rexws project add <path>")
		}
		return nil
	}

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
		fmt.Fprintf(cmd.OutOrStdout(), "Scanned %d item(s): %d removed, %d failed, %d kept.\n",
			totals.Scanned, totals.Removed, totals.Failed, totals.Kept)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Scanned %d item(s): %d safe, %d removable with the current flags, %d kept.\n",
		totals.Scanned, totals.Safe, totals.Removable, totals.Kept)
	if totals.Removable == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Preview only: nothing was changed; no items are removable with the current flags.")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Preview only: nothing was changed. Rerun with --apply to remove %d item(s).\n", totals.Removable)
	}
	if !report.Force {
		if needsForce := cleanupForceCount(report); needsForce > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "%d item(s) require --force (dirty or unmerged); --force prints what would be lost first.\n", needsForce)
		}
	}
	return nil
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
