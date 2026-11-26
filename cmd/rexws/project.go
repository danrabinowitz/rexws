package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage registered repositories",
	}
	cmd.AddCommand(newProjectAddCmd())
	return cmd
}

func newProjectAddCmd() *cobra.Command {
	var (
		name         string
		provider     string
		worktreeRoot string
		asJSON       bool
	)
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Register a Git repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := git.ProjectRoot(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			root, err = pathutil.Canonical(root)
			if err != nil {
				return err
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(root)
			}
			project, created, err := cfg.RegisterProject(root, name, provider, worktreeRoot)
			if err != nil {
				return err
			}
			if created {
				if err := cfg.Save(); err != nil {
					return err
				}
			}

			if asJSON {
				payload := struct {
					Project config.Project `json:"project"`
					Created bool           `json:"created"`
				}{project, created}
				return encodeJSON(cmd, payload)
			}
			if created {
				fmt.Fprintf(cmd.OutOrStdout(), "Registered project %q at %s\n", project.Name, project.Path)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Project %q is already registered at %s\n", project.Name, project.Path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project name (defaults to the repository directory name)")
	cmd.Flags().StringVar(&provider, "provider", "", "worktree provider for this project")
	cmd.Flags().StringVar(&worktreeRoot, "worktree-root", "", "default directory for new worktrees")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
