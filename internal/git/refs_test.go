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

func refsRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func refsRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	refsRun(t, repo, "init", "-q")
	refsRun(t, repo, "config", "user.name", "Test")
	refsRun(t, repo, "config", "user.email", "test@example.com")
	refsRun(t, repo, "commit", "--allow-empty", "-q", "-m", "init")
	return repo
}

func refsCommitFile(t *testing.T, repo, file, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	refsRun(t, repo, "add", file)
	refsRun(t, repo, "commit", "-q", "-m", message)
}

func TestDefaultBranch(t *testing.T) {
	repo := refsRepo(t)
	refsRun(t, repo, "branch", "-M", "main")
	if got, err := DefaultBranch(context.Background(), repo); err != nil || got != "main" {
		t.Fatalf("DefaultBranch = %q, %v; want main", got, err)
	}

	refsRun(t, repo, "branch", "-M", "master")
	if got, err := DefaultBranch(context.Background(), repo); err != nil || got != "master" {
		t.Fatalf("DefaultBranch = %q, %v; want master", got, err)
	}

	// origin/HEAD's target wins, and the local branch of that name is preferred
	// when it exists.
	refsRun(t, repo, "branch", "develop", "master")
	refsRun(t, repo, "update-ref", "refs/remotes/origin/develop", "master")
	refsRun(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	if got, err := DefaultBranch(context.Background(), repo); err != nil || got != "develop" {
		t.Fatalf("DefaultBranch = %q, %v; want local develop", got, err)
	}

	// With no local develop, the remote-tracking ref is the marker.
	refsRun(t, repo, "branch", "-D", "develop")
	if got, err := DefaultBranch(context.Background(), repo); err != nil || got != "origin/develop" {
		t.Fatalf("DefaultBranch = %q, %v; want origin/develop", got, err)
	}

	noDefault := refsRepo(t)
	refsRun(t, noDefault, "branch", "-M", "trunk")
	if _, err := DefaultBranch(context.Background(), noDefault); err == nil {
		t.Fatal("expected an error for a repository with no main/master/origin HEAD")
	}

	// With no local branch matching origin/HEAD's target, the remote-tracking
	// ref is the default marker.
	originOnly := refsRepo(t)
	refsRun(t, originOnly, "branch", "-M", "topic")
	refsRun(t, originOnly, "update-ref", "refs/remotes/origin/main", "topic")
	refsRun(t, originOnly, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	if got, err := DefaultBranch(context.Background(), originOnly); err != nil || got != "origin/main" {
		t.Fatalf("DefaultBranch = %q, %v; want origin/main", got, err)
	}
}

func TestIsAncestorPatchEquivalentAndCommitsOutside(t *testing.T) {
	repo := refsRepo(t)
	refsRun(t, repo, "branch", "-M", "main")

	refsRun(t, repo, "checkout", "-q", "-b", "feat")
	refsCommitFile(t, repo, "feat.txt", "feat\n", "feat work")
	featSHA := strings.TrimSpace(gitString(t, repo, "rev-parse", "HEAD"))
	refsRun(t, repo, "checkout", "-q", "main")

	if merged, err := IsAncestor(context.Background(), repo, "feat", "main"); err != nil || merged {
		t.Fatalf("IsAncestor before merge = %v, %v", merged, err)
	}
	if equivalent, err := PatchEquivalent(context.Background(), repo, "main", "feat"); err != nil || equivalent {
		t.Fatalf("PatchEquivalent before cherry-pick = %v, %v", equivalent, err)
	}

	refsCommitFile(t, repo, "main.txt", "main\n", "advance main")
	refsRun(t, repo, "cherry-pick", featSHA)
	if merged, err := IsAncestor(context.Background(), repo, "feat", "main"); err != nil || merged {
		t.Fatalf("IsAncestor after cherry-pick = %v, %v", merged, err)
	}
	if equivalent, err := PatchEquivalent(context.Background(), repo, "main", "feat"); err != nil || !equivalent {
		t.Fatalf("PatchEquivalent after cherry-pick = %v, %v", equivalent, err)
	}

	commits, err := CommitsOutside(context.Background(), repo, "main", "feat")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].SHA != featSHA || commits[0].Subject != "feat work" {
		t.Errorf("commits = %#v", commits)
	}

	refsRun(t, repo, "branch", "ancestor", "main")
	if merged, err := IsAncestor(context.Background(), repo, "ancestor", "main"); err != nil || !merged {
		t.Fatalf("IsAncestor for an ancestor branch = %v, %v", merged, err)
	}

	branches, err := ListBranches(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ancestor", "feat", "main"}
	if !reflect.DeepEqual(branches, want) {
		t.Errorf("ListBranches = %#v, want %#v", branches, want)
	}

	if err := DeleteBranch(context.Background(), repo, "feat", true); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if exists := strings.TrimSpace(gitString(t, repo, "for-each-ref", "--format=%(refname:short)", "refs/heads/feat")); exists != "" {
		t.Errorf("feat still exists: %q", exists)
	}
}

func TestStatusLinesSortedAndIsDirty(t *testing.T) {
	repo := refsRepo(t)
	if dirty, err := IsDirty(context.Background(), repo); err != nil || dirty {
		t.Fatalf("clean: dirty=%v err=%v", dirty, err)
	}
	for _, name := range []string{"zeta.txt", "alpha.txt"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := StatusLines(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "alpha.txt") || !strings.Contains(lines[1], "zeta.txt") {
		t.Errorf("StatusLines = %#v", lines)
	}
	if dirty, err := IsDirty(context.Background(), repo); err != nil || !dirty {
		t.Fatalf("dirty: dirty=%v err=%v", dirty, err)
	}
}

func TestStatusLinesIncludingIgnored(t *testing.T) {
	repo := refsRepo(t)
	refsCommitFile(t, repo, ".gitignore", "*.secret\n", "ignore secrets")
	if err := os.WriteFile(filepath.Join(repo, "only.secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := StatusLines(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("ignored file appeared in normal status: %#v", lines)
	}
	lines, err = StatusLinesIncludingIgnored(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "only.secret") || !strings.HasPrefix(lines[0], "!!") {
		t.Fatalf("ignored status = %#v", lines)
	}
}

// TestPatchEquivalentRejectsEvilMerge builds a merge commit whose parents are
// both ancestors of main but whose tree adds unique conflict-resolution
// content. git cherry ignores the merge and calls this equivalent; merge-tree
// must not.
func TestPatchEquivalentRejectsEvilMerge(t *testing.T) {
	repo := refsRepo(t)
	refsRun(t, repo, "branch", "-M", "main")
	refsCommitFile(t, repo, "one.txt", "one\n", "one")
	first := strings.TrimSpace(gitString(t, repo, "rev-parse", "HEAD"))
	refsCommitFile(t, repo, "two.txt", "two\n", "two")
	second := strings.TrimSpace(gitString(t, repo, "rev-parse", "HEAD"))

	refsRun(t, repo, "checkout", "-q", "-b", "content", second)
	refsCommitFile(t, repo, "evil.txt", "unique conflict resolution\n", "content")
	tree := strings.TrimSpace(gitString(t, repo, "rev-parse", "HEAD^{tree}"))
	evil := strings.TrimSpace(gitString(t, repo, "commit-tree", tree, "-p", first, "-p", second, "-m", "evil merge"))
	refsRun(t, repo, "branch", "feat/evil", evil)
	refsRun(t, repo, "checkout", "-q", "main")
	refsRun(t, repo, "branch", "-D", "content")

	cherry := strings.TrimSpace(gitString(t, repo, "cherry", "main", "feat/evil"))
	if strings.Contains(cherry, "+") {
		t.Fatalf("test setup: git cherry should miss the evil merge, got %q", cherry)
	}
	if merged, err := IsAncestor(context.Background(), repo, "feat/evil", "main"); err != nil || merged {
		t.Fatalf("IsAncestor = %v, %v; evil merge must not be an ancestor", merged, err)
	}
	if equivalent, err := PatchEquivalent(context.Background(), repo, "main", "feat/evil"); err != nil || equivalent {
		t.Fatalf("evil merge was called patch-equivalent: %v, %v", equivalent, err)
	}
}

// TestPatchEquivalentRejectsUpstreamRevert cherry-picks a commit and then
// reverts it upstream. git cherry still matches the patch-id; merge-tree sees
// that the branch would reintroduce the file and rejects it.
func TestPatchEquivalentRejectsUpstreamRevert(t *testing.T) {
	repo := refsRepo(t)
	refsRun(t, repo, "branch", "-M", "main")
	refsRun(t, repo, "checkout", "-q", "-b", "feat")
	refsCommitFile(t, repo, "feature.txt", "feature\n", "feature")
	featureSHA := strings.TrimSpace(gitString(t, repo, "rev-parse", "HEAD"))
	refsRun(t, repo, "checkout", "-q", "main")
	refsCommitFile(t, repo, "main.txt", "main\n", "advance main")
	refsRun(t, repo, "cherry-pick", featureSHA)
	refsRun(t, repo, "revert", "--no-edit", "HEAD")

	cherry := strings.TrimSpace(gitString(t, repo, "cherry", "main", "feat"))
	if strings.Contains(cherry, "+") {
		t.Fatalf("test setup: git cherry should match the reverted patch, got %q", cherry)
	}
	if equivalent, err := PatchEquivalent(context.Background(), repo, "main", "feat"); err != nil || equivalent {
		t.Fatalf("reverted patch was called patch-equivalent: %v, %v", equivalent, err)
	}
}

func TestStatusLinesIncludingInfoExclude(t *testing.T) {
	repo := refsRepo(t)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte("excluded.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "excluded.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := StatusLines(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("info/exclude file appeared in normal status: %#v", lines)
	}
	lines, err = StatusLinesIncludingIgnored(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "excluded.txt") || !strings.HasPrefix(lines[0], "!!") {
		t.Fatalf("ignored status = %#v", lines)
	}
}

func TestPatchEquivalentAcceptsEmptyCommit(t *testing.T) {
	repo := refsRepo(t)
	refsRun(t, repo, "branch", "-M", "main")
	refsRun(t, repo, "checkout", "-q", "-b", "empty")
	refsRun(t, repo, "commit", "--allow-empty", "-q", "-m", "empty")
	refsRun(t, repo, "checkout", "-q", "main")

	if merged, err := IsAncestor(context.Background(), repo, "empty", "main"); err != nil || merged {
		t.Fatalf("empty branch should not be an ancestor: %v, %v", merged, err)
	}
	if equivalent, err := PatchEquivalent(context.Background(), repo, "main", "empty"); err != nil || !equivalent {
		t.Fatalf("empty commit should be patch-equivalent: %v, %v", equivalent, err)
	}
}

func TestPatchEquivalentRejectsUnrelatedHistory(t *testing.T) {
	repo := refsRepo(t)
	refsRun(t, repo, "branch", "-M", "main")
	refsRun(t, repo, "checkout", "-q", "--orphan", "unrelated")
	refsRun(t, repo, "commit", "--allow-empty", "-q", "-m", "orphan")
	refsRun(t, repo, "checkout", "-q", "main")

	if _, err := PatchEquivalent(context.Background(), repo, "main", "unrelated"); err == nil {
		t.Fatal("unrelated histories must not be called patch-equivalent")
	}
}

func gitString(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}
