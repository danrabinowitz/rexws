package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseWorktreeList(t *testing.T) {
	data := `worktree /Users/djr/code/proj
HEAD 0123456789abcdef
branch refs/heads/main

worktree /Users/djr/code/worktrees/proj/feat-auth
HEAD fedcba9876543210
branch refs/heads/feat/auth

worktree /Users/djr/code/worktrees/proj/detached
HEAD 1111111111222222
detached
locked maintenance window

worktree /Users/djr/code/worktrees/proj/gone
HEAD 3333333333444444
branch refs/heads/gone
prunable gitdir file points to non-existent location
`

	got, err := ParseWorktreeList([]byte(data))
	if err != nil {
		t.Fatalf("ParseWorktreeList: %v", err)
	}
	want := []Worktree{
		{Path: "/Users/djr/code/proj", Head: "0123456789abcdef", Branch: "main"},
		{Path: "/Users/djr/code/worktrees/proj/feat-auth", Head: "fedcba9876543210", Branch: "feat/auth"},
		{Path: "/Users/djr/code/worktrees/proj/detached", Head: "1111111111222222", Detached: true, Locked: true},
		{Path: "/Users/djr/code/worktrees/proj/gone", Head: "3333333333444444", Branch: "gone", Prunable: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

func TestParseWorktreeListBare(t *testing.T) {
	got, err := ParseWorktreeList([]byte("worktree /srv/repo.git\nbare\n"))
	if err != nil {
		t.Fatalf("ParseWorktreeList: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d worktrees, want 1", len(got))
	}
	if !got[0].Bare || got[0].Path != "/srv/repo.git" {
		t.Errorf("got %#v", got[0])
	}
}

func TestParseWorktreeListCRLFAndFinalRecord(t *testing.T) {
	data := "worktree /a\r\nHEAD abc\r\nbranch refs/heads/x\r\n\r\nworktree /b\r\nHEAD def\r\nbranch refs/heads/y"
	got, err := ParseWorktreeList([]byte(data))
	if err != nil {
		t.Fatalf("ParseWorktreeList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d worktrees, want 2", len(got))
	}
	if got[1].Path != "/b" || got[1].Branch != "y" || got[1].Head != "def" {
		t.Errorf("got %#v", got[1])
	}
}

func TestParseWorktreeListIgnoresUnknownAttributes(t *testing.T) {
	data := "worktree /a\nHEAD abc\nfuture-attribute value\nbranch refs/heads/x\n"
	got, err := ParseWorktreeList([]byte(data))
	if err != nil {
		t.Fatalf("ParseWorktreeList: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/a" || got[0].Branch != "x" {
		t.Fatalf("got %#v", got)
	}
}

func TestParseWorktreeListEmpty(t *testing.T) {
	got, err := ParseWorktreeList(nil)
	if err != nil {
		t.Fatalf("ParseWorktreeList: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %#v, want none", got)
	}
}

func TestParseWorktreeListErrors(t *testing.T) {
	tests := map[string]string{
		"attribute before worktree": "HEAD abc\nworktree /a\n",
		"empty worktree path":       "worktree \nHEAD abc\n",
		"missing blank separator":   "worktree /a\nHEAD abc\nworktree /b\n",
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseWorktreeList([]byte(data)); err == nil {
				t.Fatal("expected an error")
			} else if !strings.Contains(err.Error(), "line") {
				t.Errorf("error should name the line: %v", err)
			}
		})
	}
}

func TestShortHead(t *testing.T) {
	tests := map[string]string{
		"0123456789abcdef": "0123456",
		"abc":              "abc",
		"":                 "",
	}
	for head, want := range tests {
		if got := (Worktree{Head: head}).ShortHead(); got != want {
			t.Errorf("ShortHead(%q) = %q, want %q", head, got, want)
		}
	}
}

func TestWorktreesForBranch(t *testing.T) {
	worktrees := []Worktree{
		{Path: "/repo", Branch: "main"},
		{Path: "/wt/feat", Branch: "feat/auth"},
		{Path: "/wt/feat-2", Branch: "feat/auth"},
		{Path: "/wt/bare", Branch: "feat/auth", Bare: true},
		{Path: "/wt/gone", Branch: "feat/auth", Prunable: true},
	}
	got := WorktreesForBranch(worktrees, "feat/auth")
	if len(got) != 2 || got[0].Path != "/wt/feat" || got[1].Path != "/wt/feat-2" {
		t.Errorf("got %#v", got)
	}
	if got := WorktreesForBranch(worktrees, "missing"); len(got) != 0 {
		t.Errorf("got %#v", got)
	}
}

func TestCheckBranchName(t *testing.T) {
	dir := t.TempDir()
	if err := CheckBranchName(context.Background(), dir, "feat/auth"); err != nil {
		t.Fatalf("valid branch: %v", err)
	}
	for _, branch := range []string{"", "bad name", "-bad"} {
		if err := CheckBranchName(context.Background(), dir, branch); err == nil {
			t.Errorf("branch %q was accepted", branch)
		}
	}
}

func TestIsDirty(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-q", "-m", "init")

	dirty, err := IsDirty(context.Background(), repo)
	if err != nil || dirty {
		t.Fatalf("clean worktree: dirty=%v err=%v", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = IsDirty(context.Background(), repo)
	if err != nil || !dirty {
		t.Fatalf("dirty worktree: dirty=%v err=%v", dirty, err)
	}
}
