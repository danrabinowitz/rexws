// Package provider renders and runs worktree provider command templates.
package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"text/template"
)

// Data is the template context for provider commands.
type Data struct {
	Project      string
	RepoPath     string
	RepoName     string
	Branch       string
	Slug         string
	TargetPath   string
	WorktreePath string
}

// Render renders every argument of a provider command. Unknown template
// fields are errors, so a typo does not silently become a literal.
func Render(args []string, data Data) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New("provider command is empty")
	}
	rendered := make([]string, 0, len(args))
	for i, arg := range args {
		value, err := render(arg, data)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		rendered = append(rendered, value)
	}
	if strings.TrimSpace(rendered[0]) == "" {
		return nil, errors.New("provider command name is empty")
	}
	return rendered, nil
}

// RenderWorkdir renders the optional provider working directory.
func RenderWorkdir(workdir string, data Data) (string, error) {
	if workdir == "" {
		return "", nil
	}
	return render(workdir, data)
}

func render(value string, data Data) (string, error) {
	if !strings.Contains(value, "{{") {
		return value, nil
	}
	tmpl, err := template.New("provider").Option("missingkey=error").Parse(value)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Command builds an exec.Cmd for a rendered provider command. It does not run
// the command and does not touch stdio, so a TUI can hand the process to
// tea.ExecProcess for full terminal control.
func Command(args []string, workdir string) (*exec.Cmd, error) {
	if len(args) == 0 {
		return nil, errors.New("provider command is empty")
	}
	if strings.TrimSpace(args[0]) == "" {
		return nil, errors.New("provider command name is empty")
	}
	cmd := exec.Command(args[0], args[1:]...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	return cmd, nil
}

// Run executes a rendered command with the caller's stdio. It does not use a
// shell, so the rendered arguments are passed to the program unchanged.
func Run(ctx context.Context, args []string, workdir string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("provider command is empty")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	var stderrBuf bytes.Buffer
	stderrWriter := io.Writer(&stderrBuf)
	if stderr != nil {
		stderrWriter = io.MultiWriter(stderr, &stderrBuf)
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderrWriter
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(stderrBuf.String()); message != "" {
			return fmt.Errorf("run %s: %w: %s", args[0], err, message)
		}
		return fmt.Errorf("run %s: %w", args[0], err)
	}
	return nil
}
