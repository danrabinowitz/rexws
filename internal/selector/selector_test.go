package selector

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// fixture is a resolver over two projects, a slash-named branch, and a
// detached worktree.
type fixture struct {
	resolver *Resolver
	base     string
	main     string
	featAuth string
	featAPI  string
	detached string
	other    string
}

func mkdir(t *testing.T, base, name string) string {
	t.Helper()
	path := filepath.Join(base, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := pathutil.Canonical(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	base := t.TempDir()
	f := fixture{
		base:     base,
		main:     mkdir(t, base, "repo"),
		featAuth: mkdir(t, base, "repo-feat-auth"),
		featAPI:  mkdir(t, base, "repo-feat-api"),
		detached: mkdir(t, base, "repo-detached"),
		other:    mkdir(t, base, "other"),
	}
	repo := config.Project{Name: "repo", Path: f.main}
	f.resolver = New([]Candidate{
		{Project: repo, Path: f.main, Branch: "main", SessionName: "repo/main"},
		{Project: repo, Path: f.featAuth, Branch: "feat/auth", SessionName: "repo/feat/auth"},
		{Project: repo, Path: f.featAPI, Branch: "feat/api", SessionName: "repo/feat/api"},
		{Project: repo, Path: f.detached, Branch: "0123456", SessionName: "repo/0123456", Detached: true},
		{Project: config.Project{Name: "other", Path: f.other}, Path: f.other, Branch: "dev", SessionName: "other/dev"},
	})
	return f
}

func TestResolveForms(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"exact project/branch", "repo/main", f.main},
		{"branch with slashes", "repo/feat/auth", f.featAuth},
		{"branch selector", "branch:feat/auth", f.featAuth},
		{"branch selector on detached", "branch:0123456", f.detached},
		{"path selector", "path:" + f.featAPI, f.featAPI},
		{"relative path selector", "path:" + filepath.Base(f.featAPI), f.featAPI},
		{"relative bare path", filepath.Base(f.featAuth), f.featAuth},
		{"name selector", "name:repo/feat/api", f.featAPI},
		{"exact detached identity", "repo/0123456", f.detached},
		{"bare path", f.featAuth, f.featAuth},
		{"substring of branch", "feat/auth", f.featAuth},
		{"substring ignores case", "FEAT/AUTH", f.featAuth},
		{"substring of project", "OTHER", f.other},
		{"substring of path", filepath.Base(f.featAPI), f.featAPI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate, err := f.resolver.ResolveAt(tt.input, f.base)
			if err != nil {
				t.Fatalf("ResolveAt(%q): %v", tt.input, err)
			}
			if candidate.Path != tt.want {
				t.Errorf("ResolveAt(%q) = %s, want %s", tt.input, candidate.Path, tt.want)
			}
		})
	}
}

func TestResolveExactProjectBranchPreservesIdentity(t *testing.T) {
	f := newFixture(t)
	candidate, err := f.resolver.ResolveAt("repo/feat/auth", f.base)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.SessionName != "repo/feat/auth" || candidate.Branch != "feat/auth" || candidate.Project.Name != "repo" {
		t.Errorf("candidate = %#v", candidate)
	}
	if candidate.ID() != "repo/feat/auth" {
		t.Errorf("ID() = %q", candidate.ID())
	}
}

func TestResolveActiveAndCurrent(t *testing.T) {
	f := newFixture(t)
	nested := mkdir(t, f.featAuth, "internal/deep")

	candidate, err := f.resolver.ResolveAt("active", f.featAuth)
	if err != nil {
		t.Fatalf("active at worktree root: %v", err)
	}
	if candidate.Path != f.featAuth {
		t.Errorf("active = %s, want %s", candidate.Path, f.featAuth)
	}

	candidate, err = f.resolver.ResolveAt("current", nested)
	if err != nil {
		t.Fatalf("current in subdirectory: %v", err)
	}
	if candidate.Path != f.featAuth {
		t.Errorf("current = %s, want %s", candidate.Path, f.featAuth)
	}
}

func TestResolveActiveUsesLongestContainingWorktree(t *testing.T) {
	base := t.TempDir()
	outer := mkdir(t, base, "outer")
	inner := mkdir(t, outer, "inner")
	deep := mkdir(t, inner, "deep")
	project := config.Project{Name: "repo"}
	resolver := New([]Candidate{
		{Project: project, Path: outer, Branch: "outer", SessionName: "repo/outer"},
		{Project: project, Path: inner, Branch: "inner", SessionName: "repo/inner"},
	})

	candidate, err := resolver.ResolveAt("active", deep)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if candidate.Path != inner {
		t.Errorf("active = %s, want the nested worktree %s", candidate.Path, inner)
	}
}

func TestResolveActiveOutsideEveryWorkstream(t *testing.T) {
	f := newFixture(t)
	for _, input := range []string{"active", "current"} {
		_, err := f.resolver.ResolveAt(input, f.base)
		if !errors.Is(err, ErrNoActiveWorkstream) {
			t.Fatalf("%s outside: err = %v, want ErrNoActiveWorkstream", input, err)
		}
		var noActive *NoActiveWorkstreamError
		if !errors.As(err, &noActive) {
			t.Fatalf("%s: err = %T, want *NoActiveWorkstreamError", input, err)
		}
		if !strings.Contains(err.Error(), f.base) {
			t.Errorf("%s error should name the directory: %v", input, err)
		}
	}
}

func TestResolveAmbiguousListsEveryCandidate(t *testing.T) {
	f := newFixture(t)
	_, err := f.resolver.ResolveAt("feat", f.base)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %T, want *AmbiguousError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want two", ambiguous.Candidates)
	}
	for _, want := range []string{"repo/feat/auth", "repo/feat/api", f.featAuth, f.featAPI} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not list %q: %v", want, err)
		}
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error does not say ambiguous: %v", err)
	}
}

func TestResolveBranchAmbiguousAcrossProjects(t *testing.T) {
	base := t.TempDir()
	left := mkdir(t, base, "left")
	right := mkdir(t, base, "right")
	resolver := New([]Candidate{
		{Project: config.Project{Name: "left"}, Path: left, Branch: "shared", SessionName: "left/shared"},
		{Project: config.Project{Name: "right"}, Path: right, Branch: "shared", SessionName: "right/shared"},
	})
	_, err := resolver.ResolveAt("branch:shared", base)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	for _, want := range []string{"left/shared", "right/shared"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not list %q: %v", want, err)
		}
	}
}

func TestResolveNoMatch(t *testing.T) {
	f := newFixture(t)
	tests := map[string]string{
		"unknown substring":     "does-not-exist",
		"unknown branch":        "branch:missing",
		"unknown name":          "name:missing",
		"name is exact":         "name:repo/feat",
		"name needs project":    "name:feat/auth",
		"unknown path":          "path:" + filepath.Join(f.base, "missing"),
		"empty selector":        "",
		"whitespace selector":   "   ",
		"empty branch prefix":   "branch:",
		"empty name prefix":     "name:",
		"empty path prefix":     "path:",
		"existing non-worktree": f.base,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := f.resolver.ResolveAt(input, f.base)
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("ResolveAt(%q) err = %v, want ErrNoMatch", input, err)
			}
			var noMatch *NoMatchError
			if !errors.As(err, &noMatch) {
				t.Fatalf("err = %T, want *NoMatchError", err)
			}
		})
	}

	if _, err := f.resolver.ResolveAt("   ", f.base); err == nil || strings.Contains(err.Error(), `"   "`) {
		t.Errorf("whitespace selector should be trimmed in the error: %v", err)
	}
}

func TestResolveExistingNonWorktreePathDoesNotFallThrough(t *testing.T) {
	f := newFixture(t)
	// The base directory exists and contains "feat" worktrees in its path, so a
	// fall-through to the substring rule would match them.
	_, err := f.resolver.ResolveAt(f.base, f.base)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	if !strings.Contains(err.Error(), "not a registered worktree") {
		t.Errorf("error should explain the path: %v", err)
	}
}

// TestResolveNonexistentPathShapedInputFallsThroughToSubstring pins the one
// case that may still reach the substring rule: the input has a path separator
// but does not exist, so it cannot be a path miss.
func TestResolveNonexistentPathShapedInputFallsThroughToSubstring(t *testing.T) {
	f := newFixture(t)
	candidate, err := f.resolver.ResolveAt("feat/auth", f.base)
	if err != nil {
		t.Fatalf("ResolveAt: %v", err)
	}
	if candidate.Path != f.featAuth {
		t.Errorf("candidate = %s, want %s", candidate.Path, f.featAuth)
	}
}

func TestResolveUniqueSubstringWithoutMatchingDirectory(t *testing.T) {
	f := newFixture(t)
	candidate, err := f.resolver.ResolveAt("AUTH", f.base)
	if err != nil {
		t.Fatalf("ResolveAt: %v", err)
	}
	if candidate.Path != f.featAuth {
		t.Errorf("candidate = %s, want %s", candidate.Path, f.featAuth)
	}
}

func TestResolveExistingNonWorktreeSingleComponentIsAPathMiss(t *testing.T) {
	f := newFixture(t)
	decoy := filepath.Join(f.base, "feat")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	// The decoy directory is not a worktree. Falling through to the substring
	// rule would select feat/auth or feat/api, which is wrong for status and
	// dangerous for rm.
	_, err := f.resolver.ResolveAt("feat", f.base)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	if !strings.Contains(err.Error(), "not a registered worktree") {
		t.Errorf("error should explain the path: %v", err)
	}
}

func TestResolveActiveWithCaseDifferingCwd(t *testing.T) {
	f := newFixture(t)
	wrong := filepath.Join(filepath.Dir(f.featAuth), strings.ToUpper(filepath.Base(f.featAuth)))
	if _, err := os.Stat(wrong); err != nil {
		t.Skipf("case-sensitive filesystem: %v", err)
	}
	sub := filepath.Join(wrong, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if !pathContains(f.featAuth, sub) {
		t.Fatalf("pathContains should match %s and %s by filesystem identity", f.featAuth, sub)
	}
	candidate, err := f.resolver.ResolveAt("active", sub)
	if err != nil {
		t.Fatalf("active with case-differing cwd: %v", err)
	}
	if candidate.Path != f.featAuth {
		t.Errorf("active = %s, want %s", candidate.Path, f.featAuth)
	}
}

func TestResolveCaseDistinctPathsOnCaseSensitiveFilesystem(t *testing.T) {
	base := t.TempDir()
	lower := mkdir(t, base, "casework")
	upper := mkdir(t, base, "CASEWORK")
	lowerInfo, err := os.Stat(lower)
	if err != nil {
		t.Fatal(err)
	}
	upperInfo, err := os.Stat(upper)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(lowerInfo, upperInfo) {
		t.Skip("case-insensitive filesystem: the two names are the same directory")
	}

	resolver := New([]Candidate{{
		Project:     config.Project{Name: "repo"},
		Path:        lower,
		Branch:      "main",
		SessionName: "repo/main",
	}})
	if _, err := resolver.ResolveAt(upper, base); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("ResolveAt(%q) err = %v, want ErrNoMatch", upper, err)
	}
	candidate, err := resolver.ResolveAt(lower, base)
	if err != nil {
		t.Fatalf("ResolveAt(%q): %v", lower, err)
	}
	if candidate.Path != lower {
		t.Errorf("candidate = %s, want %s", candidate.Path, lower)
	}
}

func TestResolveDuplicateDetachedIdentitiesAreAmbiguous(t *testing.T) {
	f := newFixture(t)
	first := mkdir(t, f.base, "detached-a")
	second := mkdir(t, f.base, "detached-b")
	resolver := New([]Candidate{
		{Project: config.Project{Name: "repo"}, Path: second, Branch: "0123456", SessionName: "repo/0123456", Detached: true},
		{Project: config.Project{Name: "repo"}, Path: first, Branch: "0123456", SessionName: "repo/0123456", Detached: true},
	})
	_, err := resolver.ResolveAt("repo/0123456", f.base)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %T, want *AmbiguousError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want two", ambiguous.Candidates)
	}
	if ambiguous.Candidates[0].Path != first || ambiguous.Candidates[1].Path != second {
		t.Errorf("candidate order is not deterministic: %#v", ambiguous.Candidates)
	}
	if !strings.Contains(err.Error(), "path:") {
		t.Errorf("error should suggest path: disambiguation: %v", err)
	}
}

func TestResolvePathExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	worktree := mkdir(t, home, "wt")
	resolver := New([]Candidate{{
		Project:     config.Project{Name: "repo"},
		Path:        worktree,
		Branch:      "main",
		SessionName: "repo/main",
	}})
	candidate, err := resolver.ResolveAt("path:~/wt", home)
	if err != nil {
		t.Fatalf("path:~/wt: %v", err)
	}
	if candidate.Path != worktree {
		t.Errorf("candidate = %s, want %s", candidate.Path, worktree)
	}
}

func TestCurrentNamesDirectoryWithoutSelector(t *testing.T) {
	f := newFixture(t)
	nested := mkdir(t, f.featAuth, "internal")

	candidate, err := f.resolver.Current(nested)
	if err != nil {
		t.Fatalf("Current(%q): %v", nested, err)
	}
	if candidate.Path != f.featAuth {
		t.Errorf("Current = %s, want %s", candidate.Path, f.featAuth)
	}

	_, err = f.resolver.Current(f.base)
	if !errors.Is(err, ErrNoActiveWorkstream) {
		t.Fatalf("Current outside err = %v, want ErrNoActiveWorkstream", err)
	}
	if strings.Contains(err.Error(), "selector") {
		t.Errorf("outside error names a selector: %v", err)
	}

	// The same directory registered twice (for example under two project
	// names) is an error about the directory, not about a selector.
	both := New([]Candidate{
		{Project: config.Project{Name: "repo"}, Path: f.featAuth, Branch: "feat/auth", SessionName: "repo/feat/auth"},
		{Project: config.Project{Name: "other"}, Path: f.featAuth, Branch: "feat/auth", SessionName: "other/feat/auth"},
	})
	_, err = both.Current(f.featAuth)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("claimed dir err = %v, want ErrAmbiguous", err)
	}
	var ambiguous *AmbiguousCurrentError
	if !errors.As(err, &ambiguous) || len(ambiguous.Candidates) != 2 {
		t.Fatalf("err = %#v, want *AmbiguousCurrentError with two candidates", err)
	}
	if strings.Contains(err.Error(), "selector") {
		t.Errorf("claimed-dir error names a selector: %v", err)
	}
	for _, want := range []string{f.featAuth, "claimed by 2", "repo/feat/auth", "other/feat/auth", "remove one registration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("claimed-dir error does not mention %q: %v", want, err)
		}
	}
}

func TestCurrentExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	worktree := mkdir(t, home, "wt")
	resolver := New([]Candidate{{
		Project:     config.Project{Name: "repo"},
		Path:        worktree,
		Branch:      "main",
		SessionName: "repo/main",
	}})
	candidate, err := resolver.Current("~/wt")
	if err != nil {
		t.Fatalf("Current(~/wt): %v", err)
	}
	if candidate.Path != worktree {
		t.Errorf("candidate = %s, want %s", candidate.Path, worktree)
	}
}

func TestFromInputsSkipsBareAndPrunableAndUsesShortHead(t *testing.T) {
	base := t.TempDir()
	main := mkdir(t, base, "repo")
	detached := mkdir(t, base, "repo-detached")
	nested := mkdir(t, detached, "sub")
	inputs := []core.Input{{
		Project: config.Project{Name: "repo", Path: main},
		Worktrees: []git.Worktree{
			{Path: main, Head: "aaaaaaaabbbbbbbb", Branch: "main"},
			{Path: detached, Head: "0123456789abcdef", Detached: true},
			{Path: filepath.Join(base, "bare"), Bare: true},
			{Path: filepath.Join(base, "gone"), Branch: "gone", Prunable: true},
		},
	}}
	resolver := FromInputs(inputs)
	candidates := resolver.Candidates()
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want 2", candidates)
	}
	var found Candidate
	for _, candidate := range candidates {
		if candidate.Detached {
			found = candidate
		}
	}
	if found.Path != detached || found.Branch != "0123456" || found.SessionName != "repo/0123456" {
		t.Fatalf("detached candidate = %#v", found)
	}

	for _, input := range []string{"repo/0123456", "branch:0123456", "active", "current"} {
		cwd := detached
		if input == "active" || input == "current" {
			cwd = nested
		}
		candidate, err := resolver.ResolveAt(input, cwd)
		if err != nil {
			t.Fatalf("ResolveAt(%q): %v", input, err)
		}
		if candidate.Path != detached {
			t.Errorf("ResolveAt(%q) = %s, want %s", input, candidate.Path, detached)
		}
	}
}

func TestFromInputsSkipsUnreadableWorktreeAndKeepsValidCandidates(t *testing.T) {
	base := t.TempDir()
	main := mkdir(t, base, "repo")
	feat := mkdir(t, base, "repo-feat-auth")
	missing := filepath.Join(base, "missing")
	project := config.Project{Name: "repo", Path: main}

	resolver := FromInputs([]core.Input{{
		Project: project,
		Worktrees: []git.Worktree{
			{Path: main, Branch: "main"},
			{Path: feat, Branch: "feat/auth"},
			{Path: missing, Branch: "feat/auth"},
		},
	}})

	if got := resolver.Candidates(); len(got) != 2 {
		t.Fatalf("candidates = %#v, want the two readable worktrees", got)
	}
	skipped := resolver.Skipped()
	if len(skipped) != 1 {
		t.Fatalf("skipped = %#v, want one", skipped)
	}
	if skipped[0].Project.Name != "repo" || skipped[0].Path != missing ||
		skipped[0].Branch != "feat/auth" || skipped[0].Err == nil {
		t.Errorf("skipped worktree = %#v", skipped[0])
	}
	for _, want := range []string{"project repo", missing, "feat/auth", "unreadable", "skipped"} {
		if !strings.Contains(skipped[0].Warning(), want) {
			t.Errorf("warning does not mention %q: %s", want, skipped[0].Warning())
		}
	}
}

// TestSkippedWorktreeWarnsOnlyForAffectedSelectors pins that one bad candidate
// neither breaks unrelated selectors nor stays silent when it could have
// changed the result.
func TestSkippedWorktreeWarnsOnlyForAffectedSelectors(t *testing.T) {
	base := t.TempDir()
	main := mkdir(t, base, "repo")
	feat := mkdir(t, base, "repo-feat-auth")
	missing := filepath.Join(base, "missing")
	project := config.Project{Name: "repo", Path: main}

	resolver := FromInputs([]core.Input{{
		Project: project,
		Worktrees: []git.Worktree{
			{Path: main, Branch: "main"},
			{Path: feat, Branch: "feat/auth"},
			{Path: missing, Branch: "feat/auth"},
		},
	}})

	tests := []struct {
		name        string
		input       string
		wantPath    string
		wantWarning bool
	}{
		{"exact identity with unreadable duplicate", "repo/feat/auth", feat, true},
		{"path selector on readable worktree", "path:" + feat, feat, false},
		{"unique substring matching the skip", "auth", feat, true},
		{"exact identity of an unrelated branch", "repo/main", main, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate, warnings, err := resolver.ResolveAtWithWarnings(tt.input, base)
			if err != nil {
				t.Fatalf("ResolveAtWithWarnings(%q): %v", tt.input, err)
			}
			if candidate.Path != tt.wantPath {
				t.Errorf("candidate = %s, want %s", candidate.Path, tt.wantPath)
			}
			if got := len(warnings) > 0; got != tt.wantWarning {
				t.Fatalf("warnings = %#v, want warning=%v", warnings, tt.wantWarning)
			}
			if tt.wantWarning {
				if len(warnings) != 1 || !strings.Contains(warnings[0], missing) ||
					!strings.Contains(warnings[0], "project repo") {
					t.Errorf("warnings = %#v, want the unreadable worktree and project", warnings)
				}
			}
		})
	}
}

func TestResolveWithWarningsIsQuietWhenAllCandidatesAreReadable(t *testing.T) {
	f := newFixture(t)
	for _, input := range []string{"repo/main", "path:" + f.featAPI, "feat/auth"} {
		candidate, warnings, err := f.resolver.ResolveAtWithWarnings(input, f.base)
		if err != nil {
			t.Fatalf("ResolveAtWithWarnings(%q): %v", input, err)
		}
		if candidate.SessionName == "" {
			t.Errorf("ResolveAtWithWarnings(%q) returned no candidate", input)
		}
		if len(warnings) != 0 {
			t.Errorf("ResolveAtWithWarnings(%q) warnings = %#v, want none", input, warnings)
		}
	}
}

func TestPathSelectorNamesUnreadableWorktree(t *testing.T) {
	base := t.TempDir()
	main := mkdir(t, base, "repo")
	missing := filepath.Join(base, "missing")
	resolver := FromInputs([]core.Input{{
		Project: config.Project{Name: "repo", Path: main},
		Worktrees: []git.Worktree{
			{Path: main, Branch: "main"},
			{Path: missing, Branch: "feat/gone"},
		},
	}})

	_, err := resolver.ResolveAt("path:"+missing, base)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	for _, want := range []string{missing, "project repo", "could not be read", "skipped"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// TestSkippedWorktreeMatchesAcrossSymlinkSpellings pins that a typed skipped
// error does not depend on which side of a symlink the path is spelled. On
// macOS /var/... and /private/var/... are the same directory.
func TestSkippedWorktreeMatchesAcrossSymlinkSpellings(t *testing.T) {
	base := t.TempDir()
	real := mkdir(t, base, "real")
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	main := mkdir(t, real, "repo")
	missingViaAlias := filepath.Join(alias, "missing")
	resolver := FromInputs([]core.Input{{
		Project: config.Project{Name: "repo", Path: main},
		Worktrees: []git.Worktree{
			{Path: main, Branch: "main"},
			{Path: missingViaAlias, Branch: "feat/gone"},
		},
	}})

	// The selector spells the skipped path through the real directory.
	realSpelling := filepath.Join(real, "missing")
	_, warnings, err := resolver.ResolveAtWithWarnings("path:"+realSpelling, base)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	for _, want := range []string{missingViaAlias, "project repo", "could not be read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("typed error does not mention %q: %v", want, err)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], missingViaAlias) {
		t.Errorf("warnings = %#v, want the skipped worktree", warnings)
	}
}

func TestSkippedWorktreeRelevanceAcrossSelectorForms(t *testing.T) {
	base := t.TempDir()
	readable := mkdir(t, base, "readable")
	missing := filepath.Join(base, "missing")
	resolver := FromInputs([]core.Input{{
		Project: config.Project{Name: "repo", Path: readable},
		Worktrees: []git.Worktree{
			{Path: readable, Branch: "feat/readable"},
			{Path: missing, Branch: "feat/skipped"},
		},
	}})

	tests := []struct {
		name        string
		input       string
		wantPath    string
		wantErr     error
		wantWarning bool
	}{
		{"branch names the skipped worktree", "branch:feat/skipped", "", ErrNoMatch, true},
		{"branch names the readable worktree", "branch:feat/readable", readable, nil, false},
		{"name names the skipped worktree", "name:repo/feat/skipped", "", ErrNoMatch, true},
		{"name names the readable worktree", "name:repo/feat/readable", readable, nil, false},
		{"active inside the readable worktree", "active", readable, nil, false},
		{"exact identity of the readable worktree", "repo/feat/readable", readable, nil, false},
		{"substring matching both", "feat", readable, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := base
			if tt.input == "active" {
				cwd = readable
			}
			candidate, warnings, err := resolver.ResolveAtWithWarnings(tt.input, cwd)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("ResolveAtWithWarnings(%q): %v", tt.input, err)
			}
			if tt.wantErr == nil && candidate.Path != tt.wantPath {
				t.Errorf("candidate = %s, want %s", candidate.Path, tt.wantPath)
			}
			if got := len(warnings) > 0; got != tt.wantWarning {
				t.Errorf("warnings = %#v, want warning=%v", warnings, tt.wantWarning)
			}
		})
	}
}

func TestBrokenSymlinkSkippedWorktreeComparesLexically(t *testing.T) {
	base := t.TempDir()
	main := mkdir(t, base, "repo")
	broken := filepath.Join(base, "broken")
	if err := os.Symlink(filepath.Join(base, "gone"), broken); err != nil {
		t.Fatal(err)
	}
	project := config.Project{Name: "repo", Path: main}
	resolver := FromInputs([]core.Input{{
		Project: project,
		Worktrees: []git.Worktree{
			{Path: main, Branch: "main"},
			{Path: broken, Branch: "feat/gone"},
		},
	}})

	_, warnings, err := resolver.ResolveAtWithWarnings("path:"+broken, base)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	for _, want := range []string{broken, "project repo", "could not be read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("typed error does not mention %q: %v", want, err)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], broken) {
		t.Errorf("warnings = %#v, want the broken-symlink worktree", warnings)
	}
}

// TestCurrentWarningsReportSkippedWorktreeContainingCwd exercises the
// active/current relevance path directly. A genuinely skipped path cannot
// contain an existing cwd (every ancestor would then canonicalize), so the
// synthetic resolver supplies the containment shape.
func TestCurrentWarningsReportSkippedWorktreeContainingCwd(t *testing.T) {
	base := t.TempDir()
	worktree := mkdir(t, base, "repo")
	nested := mkdir(t, worktree, "internal")
	project := config.Project{Name: "repo", Path: worktree}
	resolver := &Resolver{
		candidates: []Candidate{{
			Project: project, Path: worktree, Branch: "main", SessionName: "repo/main",
		}},
		skipped: []SkippedWorktree{{
			Project: project, Path: worktree, Branch: "gone", SessionName: "repo/gone",
			Err: errors.New("unreadable"),
		}},
	}

	candidate, warnings, err := resolver.CurrentWithWarnings(nested)
	if err != nil {
		t.Fatalf("CurrentWithWarnings: %v", err)
	}
	if candidate.Path != worktree {
		t.Errorf("candidate = %s, want %s", candidate.Path, worktree)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], worktree) ||
		!strings.Contains(warnings[0], "gone") {
		t.Errorf("warnings = %#v, want the skipped worktree containing cwd", warnings)
	}
}
