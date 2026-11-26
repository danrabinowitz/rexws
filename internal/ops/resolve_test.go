package ops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/selector"
)

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestSetStatusBySelectorForms(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	alpha := filepath.Join(root, "feat", "alpha")
	beta := filepath.Join(root, "feat", "beta")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/alpha", alpha)
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/beta", beta)
	alphaPath := canonical(t, alpha)
	betaPath := canonical(t, beta)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"branch selector", "branch:feat/alpha", alphaPath},
		{"path selector", "path:" + beta, betaPath},
		{"name selector", "name:repo/feat/alpha", alphaPath},
		{"bare path", betaPath, betaPath},
		{"case-insensitive substring", "BETA", betaPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			change, err := SetStatus(context.Background(), cfg, tt.input, config.StatusInProgress)
			if err != nil {
				t.Fatalf("SetStatus(%q): %v", tt.input, err)
			}
			if change.Path != tt.want {
				t.Errorf("SetStatus(%q) path = %s, want %s", tt.input, change.Path, tt.want)
			}
		})
	}
}

func TestSetStatusByActiveSelector(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	target := filepath.Join(root, "feat", "active")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/active", target)
	targetPath := canonical(t, target)
	nested := filepath.Join(target, "internal", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Chdir(nested)
	change, err := SetStatus(context.Background(), cfg, "active", config.StatusBlocked)
	if err != nil {
		t.Fatalf("SetStatus(active): %v", err)
	}
	if change.Path != targetPath {
		t.Errorf("path = %s, want %s", change.Path, targetPath)
	}
}

func TestResolveWorkstreamAmbiguousAndNoMatch(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	one := filepath.Join(root, "feat", "one")
	two := filepath.Join(root, "feat", "two")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/one", one)
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/two", two)

	_, err := ResolveWorkstream(context.Background(), cfg, "feat")
	if !errors.Is(err, selector.ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	var ambiguous *selector.AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %T, want *selector.AmbiguousError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Errorf("candidates = %#v, want two", ambiguous.Candidates)
	}
	for _, want := range []string{"repo/feat/one", "repo/feat/two", canonical(t, one), canonical(t, two)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not list %q: %v", want, err)
		}
	}

	noMatches := map[string]string{
		"unknown substring":     "missing",
		"unknown branch":        "branch:missing",
		"existing non-worktree": t.TempDir(),
	}
	for name, input := range noMatches {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveWorkstream(context.Background(), cfg, input)
			if !errors.Is(err, selector.ErrNoMatch) {
				t.Fatalf("ResolveWorkstream(%q) err = %v, want ErrNoMatch", input, err)
			}
		})
	}

	t.Chdir(t.TempDir())
	_, err = ResolveWorkstream(context.Background(), cfg, "active")
	if !errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Fatalf("active outside: err = %v, want ErrNoActiveWorkstream", err)
	}
}

func TestResolveHereAtProcessAndExplicitDirectories(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	target := filepath.Join(root, "feat", "here")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/here", target)
	targetPath := canonical(t, target)
	nested := filepath.Join(target, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	t.Chdir(outside)
	candidate, discovery, err := ResolveHere(context.Background(), cfg, nested)
	if err != nil {
		t.Fatalf("ResolveHere(%q): %v", nested, err)
	}
	if candidate.Path != targetPath || candidate.SessionName != "repo/feat/here" {
		t.Errorf("candidate = %#v", candidate)
	}
	if len(discovery.Inputs) != 1 || len(discovery.Inputs[0].Worktrees) != 2 {
		t.Errorf("discovery = %#v, want the snapshot used for resolution", discovery)
	}

	t.Chdir(nested)
	candidate, _, err = ResolveHere(context.Background(), cfg, "")
	if err != nil {
		t.Fatalf("ResolveHere(process cwd): %v", err)
	}
	if candidate.Path != targetPath {
		t.Errorf("process cwd path = %q, want %q", candidate.Path, targetPath)
	}

	t.Chdir(outside)
	if _, _, err := ResolveHere(context.Background(), cfg, ""); !errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Fatalf("outside err = %v, want ErrNoActiveWorkstream", err)
	}
	if _, _, err := ResolveHere(context.Background(), cfg, outside); !errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Fatalf("explicit outside err = %v, want ErrNoActiveWorkstream", err)
	}

	// A nonexistent --dir is bad input, not a workstream miss, and the error
	// names the flag and the path.
	missing := filepath.Join(outside, "missing")
	_, _, err = ResolveHere(context.Background(), cfg, missing)
	if err == nil || !strings.Contains(err.Error(), `--dir "`+missing+`"`) {
		t.Fatalf("missing --dir err = %v, want the flag and path named", err)
	}
	if errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Errorf("missing --dir err = %v, want a bad-input error", err)
	}
}

func TestResolveHereWithNoProjectsSuggestsProjectAdd(t *testing.T) {
	t.Chdir(t.TempDir())
	_, _, err := ResolveHere(context.Background(), config.Default(), "")
	if !errors.Is(err, selector.ErrNoActiveWorkstream) {
		t.Fatalf("err = %v, want ErrNoActiveWorkstream", err)
	}
	if !strings.Contains(err.Error(), "rexws project add <path>") {
		t.Errorf("err = %v, want the project-add hint", err)
	}
}

func TestContextSelectorAvoidsLookalikeLabels(t *testing.T) {
	lookalike := core.Workstream{Project: "path:odd", Branch: "main", Path: "/wt/lookalike", SessionName: "path:odd/main"}
	if got := ContextSelector([]core.Workstream{lookalike}, lookalike); got != "path:/wt/lookalike" {
		t.Errorf("colon-named project selector = %q, want the path form", got)
	}
	spaced := core.Workstream{Project: " repo", Branch: "main", Path: "/wt/spaced", SessionName: " repo/main"}
	if got := ContextSelector([]core.Workstream{spaced}, spaced); got != "path:/wt/spaced" {
		t.Errorf("whitespace-named project selector = %q, want the path form", got)
	}
	dashed := core.Workstream{Project: "-weird", Branch: "main", Path: "/wt/dashed", SessionName: "-weird/main"}
	if got := ContextSelector([]core.Workstream{dashed}, dashed); got != "path:/wt/dashed" {
		t.Errorf("dash-named project selector = %q, want the path form", got)
	}
}

func TestContextSelectorRoundTripsThroughPrepareRemove(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	one := filepath.Join(root, "feat", "one")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/one", one)
	onePath := canonical(t, one)

	writeFakeRex(t, `case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[]}' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)

	other := core.Workstream{Project: "repo", Branch: "feat/two", Path: filepath.Join(root, "feat", "two"), SessionName: "repo/feat/two"}
	workstream := core.Workstream{Project: "repo", Branch: "feat/one", Path: onePath, SessionName: "repo/feat/one"}

	selector := ContextSelector([]core.Workstream{workstream, other}, workstream)
	if selector != "repo/feat/one" {
		t.Errorf("selector = %q, want the unique exact label", selector)
	}
	plan, err := PrepareRemove(context.Background(), cfg, selector)
	if err != nil {
		t.Fatalf("PrepareRemove(%q): %v", selector, err)
	}
	if plan.Path != onePath {
		t.Errorf("plan path = %q, want %q", plan.Path, onePath)
	}

	// A second worktree on the same branch makes the label ambiguous, so the
	// selector must fall back to the path form and still resolve.
	dup := filepath.Join(root, "feat", "one-dup")
	gitRun(t, repo, "worktree", "add", "-q", "--force", dup, "feat/one")
	dupPath := canonical(t, dup)
	duplicate := core.Workstream{Project: "repo", Branch: "feat/one", Path: dupPath, SessionName: "repo/feat/one"}
	selector = ContextSelector([]core.Workstream{workstream, duplicate}, duplicate)
	if selector != "path:"+dupPath {
		t.Fatalf("selector = %q, want the path form", selector)
	}
	plan, err = PrepareRemove(context.Background(), cfg, selector)
	if err != nil {
		t.Fatalf("PrepareRemove(%q): %v", selector, err)
	}
	if plan.Path != dupPath {
		t.Errorf("plan path = %q, want %q", plan.Path, dupPath)
	}

	// A detached worktree always uses the path form.
	detached := filepath.Join(root, "detached")
	gitRun(t, repo, "worktree", "add", "-q", "--detach", detached, "HEAD")
	detachedPath := canonical(t, detached)
	detachedWS := core.Workstream{Project: "repo", Branch: "0123456", Path: detachedPath, SessionName: "repo/0123456", Detached: true}
	selector = ContextSelector([]core.Workstream{detachedWS}, detachedWS)
	if selector != "path:"+detachedPath {
		t.Fatalf("detached selector = %q, want the path form", selector)
	}
	plan, err = PrepareRemoveWithOptions(context.Background(), cfg, selector, RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("PrepareRemoveWithOptions(%q): %v", selector, err)
	}
	if plan.Path != detachedPath {
		t.Errorf("plan path = %q, want %q", plan.Path, detachedPath)
	}
}

func TestResolveWorkstreamSurfacesDiscoveryErrors(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	one := filepath.Join(root, "feat", "one")
	two := filepath.Join(root, "feat", "two")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/one", one)
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/two", two)
	cfg.Projects = append(cfg.Projects, config.Project{Name: "bad", Path: filepath.Join(t.TempDir(), "missing")})

	for _, tt := range []struct {
		name  string
		input string
		is    error
	}{
		{"no match", "missing", selector.ErrNoMatch},
		{"ambiguous", "feat", selector.ErrAmbiguous},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveWorkstream(context.Background(), cfg, tt.input)
			if !errors.Is(err, tt.is) {
				t.Fatalf("err = %v, want %v", err, tt.is)
			}
			var projectErr ProjectError
			if !errors.As(err, &projectErr) || projectErr.Project != "bad" {
				t.Fatalf("err = %v, want the unreadable project in the chain", err)
			}
		})
	}
}

func TestPrepareRemoveSurfacesDiscoveryWarning(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)
	target := filepath.Join(root, "feat", "warn")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/warn", target)
	cfg.Projects = append(cfg.Projects, config.Project{Name: "bad", Path: filepath.Join(t.TempDir(), "missing")})
	writeFakeRex(t, `case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[]}' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)

	plan, err := PrepareRemove(context.Background(), cfg, "branch:feat/warn")
	if err != nil {
		t.Fatalf("PrepareRemove: %v", err)
	}
	if len(plan.Warnings) != 1 {
		t.Fatalf("warnings = %#v, want one", plan.Warnings)
	}
	var projectErr ProjectError
	if !errors.As(plan.Warnings[0], &projectErr) || projectErr.Project != "bad" {
		t.Fatalf("warning = %v, want the unreadable project", plan.Warnings[0])
	}
}

func TestResolveDuplicateDetachedIdentities(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	first := filepath.Join(root, "detached-a")
	second := filepath.Join(root, "detached-b")
	gitRun(t, repo, "worktree", "add", "-q", "--detach", first, "HEAD")
	gitRun(t, repo, "worktree", "add", "-q", "--detach", second, "HEAD")
	short := gitOutput(t, first, "rev-parse", "--short=7", "HEAD")

	_, err := ResolveWorkstream(context.Background(), cfg, "repo/"+short)
	if !errors.Is(err, selector.ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	var ambiguous *selector.AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %T, want *selector.AmbiguousError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Errorf("candidates = %#v, want two", ambiguous.Candidates)
	}
	for _, want := range []string{canonical(t, first), canonical(t, second), "path:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

func TestResolveDetachedWorktree(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	target := filepath.Join(root, "detached")
	gitRun(t, repo, "worktree", "add", "-q", "--detach", target, "HEAD")
	targetPath := canonical(t, target)
	short := gitOutput(t, target, "rev-parse", "--short=7", "HEAD")

	for _, input := range []string{"branch:" + short, "repo/" + short, "name:repo/" + short} {
		candidate, err := ResolveWorkstream(context.Background(), cfg, input)
		if err != nil {
			t.Fatalf("ResolveWorkstream(%q): %v", input, err)
		}
		if candidate.Path != targetPath || candidate.Branch != short || !candidate.Detached {
			t.Errorf("ResolveWorkstream(%q) = %#v", input, candidate)
		}
	}
}

func TestPrepareRemoveBySelector(t *testing.T) {
	repo := testRepo(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := testConfig(t, repo, root)

	target := filepath.Join(root, "feat", "selected")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/selected", target)
	targetPath := canonical(t, target)
	writeFakeRex(t, `case "$*" in
  "session ls --json") printf '%s\n' '{"sessions":[]}' ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`)

	plan, err := PrepareRemove(context.Background(), cfg, "branch:feat/selected")
	if err != nil {
		t.Fatalf("PrepareRemove: %v", err)
	}
	if plan.Path != targetPath || plan.Branch != "feat/selected" || plan.SessionName != "repo/feat/selected" {
		t.Errorf("plan = %#v", plan)
	}
}
