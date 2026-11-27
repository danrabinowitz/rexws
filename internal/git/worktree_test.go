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
	data := "worktree /Users/djr/code/proj\x00" +
		"HEAD 0123456789abcdef\x00" +
		"branch refs/heads/main\x00" +
		"\x00" +
		"worktree /Users/djr/code/worktrees/proj/feat-auth\x00" +
		"HEAD fedcba9876543210\x00" +
		"branch refs/heads/feat/auth\x00" +
		"\x00" +
		"worktree /Users/djr/code/worktrees/proj/detached\x00" +
		"HEAD 1111111111222222\x00" +
		"detached\x00" +
		"locked maintenance window\x00" +
		"\x00" +
		"worktree /Users/djr/code/worktrees/proj/gone\x00" +
		"HEAD 3333333333444444\x00" +
		"branch refs/heads/gone\x00" +
		"prunable gitdir file points to non-existent location\x00"

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

// TestParseWorktreeListNewlineInPath pins the reason the parser reads the -z
// form: a path may contain a newline, which the non-NUL porcelain output
// cannot represent.
func TestParseWorktreeListNewlineInPath(t *testing.T) {
	data := "worktree /Users/djr/code/worktrees/proj/line\nbreak\x00" +
		"HEAD abcdef0123456789\x00" +
		"branch refs/heads/feat/newline\x00" +
		"\x00" +
		"worktree /Users/djr/code/worktrees/proj/next\x00" +
		"HEAD 0123456789abcdef\x00" +
		"branch refs/heads/next\x00"
	got, err := ParseWorktreeList([]byte(data))
	if err != nil {
		t.Fatalf("ParseWorktreeList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d worktrees, want 2", len(got))
	}
	if got[0].Path != "/Users/djr/code/worktrees/proj/line\nbreak" ||
		got[0].Branch != "feat/newline" {
		t.Errorf("newline worktree = %#v", got[0])
	}
	if got[1].Path != "/Users/djr/code/worktrees/proj/next" || got[1].Branch != "next" {
		t.Errorf("following worktree = %#v", got[1])
	}
}

func TestParseWorktreeListCarriageReturnInPath(t *testing.T) {
	// -z preserves every byte of a path, including a carriage return that the
	// old newline parser stripped as a CRLF terminator.
	data := "worktree /a\r\x00HEAD abc\x00branch refs/heads/x\x00"
	got, err := ParseWorktreeList([]byte(data))
	if err != nil {
		t.Fatalf("ParseWorktreeList: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/a\r" {
		t.Fatalf("got %#v, want the carriage return intact", got)
	}
}

func TestParseWorktreeListBare(t *testing.T) {
	got, err := ParseWorktreeList([]byte("worktree /srv/repo.git\x00bare\x00"))
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

func TestParseWorktreeListFinalRecordWithoutSeparator(t *testing.T) {
	data := "worktree /a\x00HEAD abc\x00branch refs/heads/x\x00\x00worktree /b\x00HEAD def\x00branch refs/heads/y"
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
	data := "worktree /a\x00HEAD abc\x00future-attribute value\x00branch refs/heads/x\x00"
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
		"attribute before worktree": "HEAD abc\x00worktree /a\x00",
		"empty worktree path":       "worktree \x00HEAD abc\x00",
		"missing field separator":   "worktree /a\x00HEAD abc\x00worktree /b\x00",
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseWorktreeList([]byte(data)); err == nil {
				t.Fatal("expected an error")
			} else if !strings.Contains(err.Error(), "field") {
				t.Errorf("error should name the field: %v", err)
			}
		})
	}
}

// TestListWorktreesPreservesNewlinePath exercises the real `-z` output: Git
// reports the path verbatim, with no quoting and no truncation at the newline.
func TestListWorktreesPreservesNewlinePath(t *testing.T) {
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

	path := filepath.Join(t.TempDir(), "feat", "line\nbreak")
	run("worktree", "add", "-q", "-b", "feat/newline", path)
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}

	worktrees, err := ListWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	var found *Worktree
	for i := range worktrees {
		if worktrees[i].Branch == "feat/newline" {
			found = &worktrees[i]
		}
	}
	if found == nil {
		t.Fatalf("newline worktree missing from %#v", worktrees)
	}
	if found.Path != want || found.Detached || found.Prunable {
		t.Errorf("newline worktree = %#v, want path %q", *found, want)
	}
}

func TestParseGitVersion(t *testing.T) {
	tests := map[string]struct {
		major, minor int
		ok           bool
	}{
		"git version 2.36.0":                   {2, 36, true},
		"git version 2.35.8\n":                 {2, 35, true},
		"git version 2.39.5 (Apple Git-154)\n": {2, 39, true},
		"git version 2.36.0.windows.1\n":       {2, 36, true},
		"git version 3.0.0":                    {3, 0, true},
		"not a version":                        {0, 0, false},
		"git version 2":                        {0, 0, false},
		"git version two.thirty-six":           {0, 0, false},
	}
	for input, want := range tests {
		major, minor, ok := parseGitVersion(input)
		if ok != want.ok || (ok && (major != want.major || minor != want.minor)) {
			t.Errorf("parseGitVersion(%q) = %d.%d,%v want %d.%d,%v",
				input, major, minor, ok, want.major, want.minor, want.ok)
		}
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

func TestTopLevel(t *testing.T) {
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
	sub := filepath.Join(repo, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	top, err := TopLevel(context.Background(), sub)
	if err != nil {
		t.Fatalf("TopLevel: %v", err)
	}
	gotTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		t.Fatal(err)
	}
	wantTop, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if gotTop != wantTop {
		t.Errorf("TopLevel from a subdirectory = %q, want %q", gotTop, wantTop)
	}

	outside := t.TempDir()
	if _, err := TopLevel(context.Background(), outside); err == nil ||
		!strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("TopLevel outside a worktree err = %v", err)
	}
}
