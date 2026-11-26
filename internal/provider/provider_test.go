package provider

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testData() Data {
	return Data{
		Project:      "rexws",
		RepoPath:     "/code/rexws",
		RepoName:     "rexws",
		Branch:       "feat/auth",
		Slug:         "feat-auth",
		TargetPath:   "/code/worktrees/rexws/feat/auth",
		WorktreePath: "/code/worktrees/rexws/feat/auth",
	}
}

func TestRender(t *testing.T) {
	args := []string{
		"git", "-C", "{{.RepoPath}}", "worktree", "add", "-b",
		"{{.Branch}}", "{{.TargetPath}}", "{{.Project}}/{{.Slug}}",
	}
	got, err := Render(args, testData())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{
		"git", "-C", "/code/rexws", "worktree", "add", "-b",
		"feat/auth", "/code/worktrees/rexws/feat/auth", "rexws/feat-auth",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestRenderErrors(t *testing.T) {
	tests := map[string][]string{
		"empty":         nil,
		"empty command": {""},
		"unknown field": {"git", "{{.Nope}}"},
		"bad template":  {"git", "{{"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(args, testData()); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestRenderWorkdir(t *testing.T) {
	got, err := RenderWorkdir("{{.RepoPath}}", testData())
	if err != nil {
		t.Fatalf("RenderWorkdir: %v", err)
	}
	if got != "/code/rexws" {
		t.Errorf("workdir = %q", got)
	}
	if got, err := RenderWorkdir("", testData()); err != nil || got != "" {
		t.Errorf("empty workdir = %q, %v", got, err)
	}
}

func TestCommand(t *testing.T) {
	cmd, err := Command([]string{"git", "worktree", "add", "-b", "feat", "/tmp/wt"}, "/code/repo")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if cmd.Dir != "/code/repo" {
		t.Errorf("Dir = %q", cmd.Dir)
	}
	if got := strings.Join(cmd.Args, "\x00"); got != "git\x00worktree\x00add\x00-b\x00feat\x00/tmp/wt" {
		t.Errorf("Args = %q", cmd.Args)
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Error("Command must leave stdio for the caller")
	}

	if _, err := Command(nil, ""); err == nil {
		t.Error("empty command accepted")
	}
	if _, err := Command([]string{" "}, ""); err == nil {
		t.Error("blank command name accepted")
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	err := Run(context.Background(), []string{"/bin/pwd", "-P"}, dir, nil, &out, &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != resolvedDir {
		t.Errorf("pwd = %q, want %q", strings.TrimSpace(out.String()), resolvedDir)
	}

	out.Reset()
	if err := Run(context.Background(), []string{"/bin/sh", "-c", "printf hi"}, "", nil, &out, &out); err != nil {
		t.Fatalf("Run without workdir: %v", err)
	}
	if out.String() != "hi" {
		t.Errorf("output = %q", out.String())
	}

	if err := Run(context.Background(), []string{filepath.Join(os.TempDir(), "missing-provider")}, "", nil, nil, nil); err == nil {
		t.Fatal("expected an error for a missing command")
	}

	err = Run(context.Background(), []string{"/bin/sh", "-c", "echo boom >&2; exit 3"}, "", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want it to include the command's stderr", err)
	}
}
