// Package selector resolves the one selector grammar shared by every rexws
// command that names a workstream. The grammar is documented in README.md.
package selector

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// Sentinel errors let scripts distinguish an unmatched selector from an
// ambiguous one without parsing messages.
var (
	ErrNoMatch            = errors.New("no workstream matches the selector")
	ErrAmbiguous          = errors.New("selector matches more than one workstream")
	ErrNoActiveWorkstream = errors.New("current directory is not inside a workstream")
)

// Candidate is one selectable workstream.
type Candidate struct {
	Project     config.Project
	Path        string
	Branch      string
	SessionName string
	Detached    bool
}

// ID is the workstream's human identifier: its Rex session label.
func (c Candidate) ID() string { return c.SessionName }

// SkippedWorktree is a discovered worktree whose path could not be
// canonicalized. The type is shared with core so the selector and the
// workstream model skip the same candidates. It is left out of the resolver so
// one stale, unreadable, or symlink-broken worktree cannot break every
// selector; callers surface it when a selector could have matched it, or when
// a destructive action needs to know that the candidate set is incomplete.
type SkippedWorktree = core.SkippedWorktree

// NoMatchError reports that a selector resolved to no workstream.
type NoMatchError struct {
	Selector string
	Detail   string
}

func (e *NoMatchError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("no workstream matches %q: %s", e.Selector, e.Detail)
	}
	return fmt.Sprintf("no workstream matches %q; run `rexws list` to see known workstreams", e.Selector)
}

// Unwrap exposes ErrNoMatch to errors.Is.
func (e *NoMatchError) Unwrap() error { return ErrNoMatch }

// AmbiguousError reports that a selector matched several workstreams. It
// carries every match so callers and people can choose one.
type AmbiguousError struct {
	Selector   string
	Candidates []Candidate
}

func (e *AmbiguousError) Error() string {
	ids := make([]string, 0, len(e.Candidates))
	for _, candidate := range e.Candidates {
		ids = append(ids, fmt.Sprintf("%s (%s)", candidate.SessionName, candidate.Path))
	}
	return fmt.Sprintf("selector %q is ambiguous; it matches %d workstreams: %s; use `path:<path>` to choose one",
		e.Selector, len(ids), strings.Join(ids, ", "))
}

// Unwrap exposes ErrAmbiguous to errors.Is.
func (e *AmbiguousError) Unwrap() error { return ErrAmbiguous }

// AmbiguousCurrentError reports that several worktrees claim the same
// directory. It is the directory form of AmbiguousError: callers that resolved
// a directory rather than a selector (rexws here --dir) get a message about
// the directory instead of a selector they never typed.
type AmbiguousCurrentError struct {
	Dir        string
	Candidates []Candidate
}

func (e *AmbiguousCurrentError) Error() string {
	ids := make([]string, 0, len(e.Candidates))
	for _, candidate := range e.Candidates {
		ids = append(ids, fmt.Sprintf("%s (%s)", candidate.SessionName, candidate.Path))
	}
	return fmt.Sprintf("%s is claimed by %d registered workstreams: %s; the projects share this directory, so remove one registration",
		e.Dir, len(ids), strings.Join(ids, ", "))
}

// Unwrap exposes ErrAmbiguous to errors.Is, so a directory claimed twice keeps
// the ambiguous-selector exit code.
func (e *AmbiguousCurrentError) Unwrap() error { return ErrAmbiguous }

// NoActiveWorkstreamError reports that active or current was used outside
// every discovered worktree.
type NoActiveWorkstreamError struct {
	Dir string
}

func (e *NoActiveWorkstreamError) Error() string {
	return fmt.Sprintf("%s is not inside a registered workstream; run `rexws list` to see known worktrees", e.Dir)
}

// Unwrap exposes ErrNoActiveWorkstream to errors.Is.
func (e *NoActiveWorkstreamError) Unwrap() error { return ErrNoActiveWorkstream }

// Resolver resolves selectors against a fixed set of candidates.
type Resolver struct {
	candidates []Candidate
	skipped    []SkippedWorktree
}

// New returns a resolver over already-canonical candidates.
func New(candidates []Candidate) *Resolver {
	return &Resolver{candidates: append([]Candidate(nil), candidates...)}
}

// FromInputs builds a resolver from discovered worktrees. Bare and prunable
// worktrees are skipped and every path is canonicalized, matching what list
// and the TUI display. A worktree that cannot be canonicalized is recorded in
// Skipped instead of failing the whole resolver, so one bad path cannot break
// unrelated selectors.
func FromInputs(inputs []core.Input) *Resolver {
	candidates := make([]Candidate, 0)
	var skipped []SkippedWorktree
	for _, input := range inputs {
		for _, wt := range input.Worktrees {
			path, branch, skippedWorktree, ok := core.WorktreeCandidate(input.Project, wt)
			if skippedWorktree != nil {
				skipped = append(skipped, *skippedWorktree)
				continue
			}
			if !ok {
				continue
			}
			candidates = append(candidates, Candidate{
				Project:     input.Project,
				Path:        path,
				Branch:      branch,
				SessionName: input.Project.Name + "/" + branch,
				Detached:    wt.Detached,
			})
		}
	}
	return &Resolver{candidates: candidates, skipped: skipped}
}

// Candidates returns a copy of the resolver's candidates.
func (r *Resolver) Candidates() []Candidate {
	return append([]Candidate(nil), r.candidates...)
}

// Skipped returns a copy of the discovered worktrees left out because their
// paths could not be canonicalized.
func (r *Resolver) Skipped() []SkippedWorktree {
	return append([]SkippedWorktree(nil), r.skipped...)
}

// warningsFor reports skipped worktrees the selector could have matched. A
// skipped worktree in an unrelated project stays quiet.
func (r *Resolver) warningsFor(raw string, cwd cwdSource) []string {
	if len(r.skipped) == 0 {
		return nil
	}
	input := strings.TrimSpace(raw)
	var affected []SkippedWorktree
	switch {
	case input == "active" || input == "current":
		dir, err := currentDir(cwd)
		if err == nil {
			if canonical, err := pathutil.Canonical(expandHome(dir)); err == nil {
				affected = r.skippedContaining(canonical)
			}
		}
	case strings.HasPrefix(input, "branch:"):
		value := strings.TrimSpace(strings.TrimPrefix(input, "branch:"))
		affected = r.skippedMatching(func(s SkippedWorktree) bool { return s.Branch == value })
	case strings.HasPrefix(input, "path:"):
		value := strings.TrimSpace(strings.TrimPrefix(input, "path:"))
		if skipped, ok := r.skippedAt(value, cwd); ok {
			affected = append(affected, skipped)
		}
	case strings.HasPrefix(input, "name:"):
		value := strings.TrimSpace(strings.TrimPrefix(input, "name:"))
		affected = r.skippedMatching(func(s SkippedWorktree) bool { return s.SessionName == value })
	default:
		switch {
		case len(r.bySessionName(input)) > 0:
			// The exact identity won before the substring rule, so only a
			// skipped worktree with the same identity could have changed it.
			affected = r.skippedMatching(func(s SkippedWorktree) bool { return s.SessionName == input })
		case r.isPathSelection(input, cwd):
			if skipped, ok := r.skippedAt(input, cwd); ok {
				affected = append(affected, skipped)
			}
		default:
			needle := strings.ToLower(input)
			affected = r.skippedMatching(func(s SkippedWorktree) bool {
				return s.SessionName == input || r.skippedPathEqual(s.Path, input, cwd) || skippedSubstring(s, needle)
			})
		}
	}
	return skippedWarnings(affected)
}

// warningsForDir reports skipped worktrees whose path contains dir, the rule
// behind active/current.
func (r *Resolver) warningsForDir(dir string) []string {
	return skippedWarnings(r.skippedContaining(dir))
}

func (r *Resolver) skippedContaining(dir string) []SkippedWorktree {
	var matches []SkippedWorktree
	for _, skipped := range r.skipped {
		abs, ok := bestEffortCanonical(skipped.Path, nil)
		if ok && lexicalPathContains(abs, dir) {
			matches = append(matches, skipped)
		}
	}
	return matches
}

func (r *Resolver) skippedMatching(match func(SkippedWorktree) bool) []SkippedWorktree {
	var matches []SkippedWorktree
	for _, skipped := range r.skipped {
		if match(skipped) {
			matches = append(matches, skipped)
		}
	}
	return matches
}

// skippedAt finds a skipped worktree by path. Both sides use best-effort
// canonicalization, because the skipped path itself does not exist but its
// existing ancestors may still contain symlinks.
func (r *Resolver) skippedAt(value string, cwd cwdSource) (SkippedWorktree, bool) {
	if len(r.skipped) == 0 {
		return SkippedWorktree{}, false
	}
	target, ok := bestEffortCanonical(value, cwd)
	if !ok {
		return SkippedWorktree{}, false
	}
	for _, skipped := range r.skipped {
		abs, ok := bestEffortCanonical(skipped.Path, cwd)
		if ok && abs == target {
			return skipped, true
		}
	}
	return SkippedWorktree{}, false
}

// isPathSelection reports whether input resolved as an existing literal path,
// which wins over the substring rule.
func (r *Resolver) isPathSelection(input string, cwd cwdSource) bool {
	canonical, err := canonicalFrom(input, cwd)
	return err == nil && len(r.byPath(canonical)) > 0
}

func (r *Resolver) skippedPathEqual(skippedPath, input string, cwd cwdSource) bool {
	target, ok := bestEffortCanonical(input, cwd)
	if !ok {
		return false
	}
	abs, ok := bestEffortCanonical(skippedPath, cwd)
	if !ok {
		return false
	}
	return abs == target
}

func skippedSubstring(s SkippedWorktree, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(strings.ToLower(s.Project.Name), needle) ||
		strings.Contains(strings.ToLower(s.Branch), needle) ||
		strings.Contains(strings.ToLower(s.Path), needle)
}

// skippedWarnings renders affected skipped worktrees in a stable order.
func skippedWarnings(affected []SkippedWorktree) []string {
	if len(affected) == 0 {
		return nil
	}
	sort.SliceStable(affected, func(i, j int) bool {
		if affected[i].SessionName != affected[j].SessionName {
			return affected[i].SessionName < affected[j].SessionName
		}
		return affected[i].Path < affected[j].Path
	})
	warnings := make([]string, 0, len(affected))
	for _, skipped := range affected {
		warnings = append(warnings, skipped.Warning())
	}
	return warnings
}

// absolutePath makes path absolute against cwd without resolving symlinks, so
// a skipped path can still be compared.
func absolutePath(path string, cwd cwdSource) (string, bool) {
	if !filepath.IsAbs(path) {
		dir, err := currentDir(cwd)
		if err != nil {
			return "", false
		}
		path = filepath.Join(dir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return abs, true
}

// bestEffortCanonical makes path absolute and resolves symlinks in its
// deepest existing ancestor, then rejoins the missing suffix. Unlike
// pathutil.Canonical it succeeds for a path that does not exist, so a skipped
// worktree can compare equal to a selector path spelled through a symlink
// (/var/... versus /private/var/... on macOS, for example). A broken final
// symlink is deliberately compared by its lexical spelling: resolving it
// would name the missing target, which is a different path.
func bestEffortCanonical(path string, cwd cwdSource) (string, bool) {
	abs, ok := absolutePath(expandHome(path), cwd)
	if !ok {
		return "", false
	}
	return canonicalExistingPrefix(abs), true
}

func canonicalExistingPrefix(abs string) string {
	if canonical, err := filepath.EvalSymlinks(abs); err == nil {
		return canonical
	}
	current := filepath.Clean(abs)
	var suffix []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(abs)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
		canonical, err := filepath.EvalSymlinks(current)
		if err != nil {
			continue
		}
		for i := len(suffix) - 1; i >= 0; i-- {
			canonical = filepath.Join(canonical, suffix[i])
		}
		return filepath.Clean(canonical)
	}
}

// cwdSource supplies a current directory lazily, so selectors that do not
// need one never pay for os.Getwd.
type cwdSource func() (string, error)

// Resolve resolves a selector against the process working directory. The
// directory is read only when the selector needs it.
func (r *Resolver) Resolve(selector string) (Candidate, error) {
	return r.resolve(selector, nil)
}

// ResolveAt resolves a selector with an explicit current directory. Tests and
// frontends that already know the directory should use it over Resolve.
func (r *Resolver) ResolveAt(selector, cwd string) (Candidate, error) {
	return r.resolve(selector, func() (string, error) { return cwd, nil })
}

// ResolveWithWarnings resolves like Resolve and also reports skipped worktrees
// the selector could have matched. The warnings are empty when no skipped
// worktree is relevant, so unrelated selectors stay quiet.
func (r *Resolver) ResolveWithWarnings(selector string) (Candidate, []string, error) {
	return r.resolveWithWarnings(selector, nil)
}

// ResolveAtWithWarnings is ResolveAt with the same skipped-worktree warnings.
func (r *Resolver) ResolveAtWithWarnings(selector, cwd string) (Candidate, []string, error) {
	return r.resolveWithWarnings(selector, func() (string, error) { return cwd, nil })
}

func (r *Resolver) resolveWithWarnings(raw string, cwd cwdSource) (Candidate, []string, error) {
	candidate, err := r.resolve(raw, cwd)
	return candidate, r.warningsFor(raw, cwd), err
}

func (r *Resolver) resolve(raw string, cwd cwdSource) (Candidate, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return Candidate{}, &NoMatchError{Selector: input, Detail: "selector is empty"}
	}

	switch input {
	case "active", "current":
		return r.current(input, cwd)
	}
	if kind, value, ok := strings.Cut(input, ":"); ok {
		switch kind {
		case "branch":
			value = strings.TrimSpace(value)
			if value == "" {
				return Candidate{}, &NoMatchError{Selector: input, Detail: "branch name is empty"}
			}
			return r.unique(input, r.byBranch(value))
		case "path":
			value = strings.TrimSpace(value)
			if value == "" {
				return Candidate{}, &NoMatchError{Selector: input, Detail: "path is empty"}
			}
			canonical, err := canonicalFrom(value, cwd)
			if err != nil {
				if skipped, ok := r.skippedAt(value, cwd); ok {
					return Candidate{}, &NoMatchError{Selector: input, Detail: skipped.Detail()}
				}
				return Candidate{}, &NoMatchError{Selector: input, Detail: err.Error()}
			}
			matches := r.byPath(canonical)
			if len(matches) == 0 {
				if skipped, ok := r.skippedAt(canonical, cwd); ok {
					return Candidate{}, &NoMatchError{Selector: input, Detail: skipped.Detail()}
				}
				return Candidate{}, &NoMatchError{Selector: input, Detail: fmt.Sprintf("%s is not a registered worktree", canonical)}
			}
			return r.unique(input, matches)
		case "name":
			value = strings.TrimSpace(value)
			if value == "" {
				return Candidate{}, &NoMatchError{Selector: input, Detail: "workstream name is empty"}
			}
			return r.unique(input, r.bySessionName(value))
		}
	}

	// A bare selector keeps exact <project>/<branch> behavior first, then
	// accepts a literal path, and only then falls through to the substring
	// rule. An existing path that is not a worktree is a path miss: treating
	// it as a substring can silently select an unrelated workstream.
	if matches := r.bySessionName(input); len(matches) > 0 {
		return r.unique(input, matches)
	}
	if canonical, err := canonicalFrom(input, cwd); err == nil {
		if matches := r.byPath(canonical); len(matches) > 0 {
			return r.unique(input, matches)
		}
		if skipped, ok := r.skippedAt(canonical, cwd); ok {
			return Candidate{}, &NoMatchError{Selector: input, Detail: skipped.Detail()}
		}
		return Candidate{}, &NoMatchError{
			Selector: input,
			Detail:   fmt.Sprintf("%s exists but is not a registered worktree", canonical),
		}
	}
	if skipped, ok := r.skippedAt(input, cwd); ok {
		return Candidate{}, &NoMatchError{Selector: input, Detail: skipped.Detail()}
	}
	return r.unique(input, r.bySubstring(input))
}

// current resolves active/current to the workstream whose path contains the
// current directory. The deepest containing worktree wins.
func (r *Resolver) current(selector string, cwd cwdSource) (Candidate, error) {
	raw, err := currentDir(cwd)
	if err != nil {
		return Candidate{}, fmt.Errorf("resolve current directory: %w", err)
	}
	candidate, err := r.Current(raw)
	if err == nil {
		return candidate, nil
	}
	var ambiguous *AmbiguousCurrentError
	if errors.As(err, &ambiguous) {
		return r.unique(selector, ambiguous.Candidates)
	}
	if errors.Is(err, ErrNoActiveWorkstream) {
		return Candidate{}, err
	}
	return Candidate{}, fmt.Errorf("resolve current directory: %w", err)
}

// CurrentWithWarnings resolves like Current and also reports skipped
// worktrees whose path contains dir, which could have changed the result.
func (r *Resolver) CurrentWithWarnings(dir string) (Candidate, []string, error) {
	candidate, err := r.Current(dir)
	canonical, canonicalErr := pathutil.Canonical(expandHome(dir))
	if canonicalErr != nil {
		return candidate, nil, err
	}
	return candidate, r.warningsForDir(canonical), err
}

// Current resolves the workstream whose path contains dir, the rule behind the
// active/current selectors. The deepest containing worktree wins; a tie is an
// error. It names the directory, never a selector, so callers that resolved a
// directory directly (rexws here --dir) get a message they can act on. A
// leading ~ or ~/ in dir expands to the home directory, matching path:.
func (r *Resolver) Current(dir string) (Candidate, error) {
	canonical, err := pathutil.Canonical(expandHome(dir))
	if err != nil {
		return Candidate{}, err
	}
	matches := r.containing(canonical)
	switch len(matches) {
	case 0:
		return Candidate{}, &NoActiveWorkstreamError{Dir: canonical}
	case 1:
		return matches[0], nil
	default:
		return Candidate{}, &AmbiguousCurrentError{Dir: canonical, Candidates: sortedCandidates(matches)}
	}
}

// containing returns the deepest candidates whose worktree contains dir.
func (r *Resolver) containing(dir string) []Candidate {
	var matches []Candidate
	longest := -1
	for _, candidate := range r.candidates {
		if !pathContains(candidate.Path, dir) {
			continue
		}
		switch {
		case len(candidate.Path) > longest:
			matches = []Candidate{candidate}
			longest = len(candidate.Path)
		case len(candidate.Path) == longest:
			matches = append(matches, candidate)
		}
	}
	return matches
}

func (r *Resolver) byBranch(branch string) []Candidate {
	var matches []Candidate
	for _, candidate := range r.candidates {
		if candidate.Branch == branch {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func (r *Resolver) bySessionName(name string) []Candidate {
	var matches []Candidate
	for _, candidate := range r.candidates {
		if candidate.SessionName == name {
			matches = append(matches, candidate)
		}
	}
	return matches
}

// byPath matches a canonical path exactly, then by filesystem identity. The
// identity pass only helps on case-insensitive filesystems, where the
// filesystem may resolve a differently-cased spelling while the string keeps
// that casing; on a case-sensitive filesystem two distinct directories stay
// distinct.
func (r *Resolver) byPath(path string) []Candidate {
	if matches := r.byExactPath(path); len(matches) > 0 {
		return matches
	}
	target, err := os.Stat(path)
	if err != nil {
		return nil
	}
	var matches []Candidate
	for _, candidate := range r.candidates {
		info, err := os.Stat(candidate.Path)
		if err == nil && os.SameFile(target, info) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func (r *Resolver) byExactPath(path string) []Candidate {
	var matches []Candidate
	for _, candidate := range r.candidates {
		if candidate.Path == path {
			matches = append(matches, candidate)
		}
	}
	return matches
}

func (r *Resolver) bySubstring(input string) []Candidate {
	needle := strings.ToLower(input)
	if needle == "" {
		return nil
	}
	var matches []Candidate
	for _, candidate := range r.candidates {
		if strings.Contains(strings.ToLower(candidate.Project.Name), needle) ||
			strings.Contains(strings.ToLower(candidate.Branch), needle) ||
			strings.Contains(strings.ToLower(candidate.Path), needle) {
			matches = append(matches, candidate)
		}
	}
	return matches
}

// unique turns a match set into one candidate, a no-match error, or an
// ambiguity error that lists every candidate.
func (r *Resolver) unique(input string, matches []Candidate) (Candidate, error) {
	switch len(matches) {
	case 0:
		return Candidate{}, &NoMatchError{Selector: input}
	case 1:
		return matches[0], nil
	default:
		return Candidate{}, &AmbiguousError{Selector: input, Candidates: sortedCandidates(matches)}
	}
}

// sortedCandidates orders matches by identity, then path, so every error lists
// the same candidates in the same order.
func sortedCandidates(matches []Candidate) []Candidate {
	sorted := append([]Candidate(nil), matches...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].SessionName != sorted[j].SessionName {
			return sorted[i].SessionName < sorted[j].SessionName
		}
		return sorted[i].Path < sorted[j].Path
	})
	return sorted
}

func currentDir(cwd cwdSource) (string, error) {
	if cwd != nil {
		return cwd()
	}
	return os.Getwd()
}

// canonicalFrom resolves a path that may be relative to cwd. A leading ~ or
// ~/ expands to the home directory.
func canonicalFrom(path string, cwd cwdSource) (string, error) {
	path = expandHome(path)
	if !filepath.IsAbs(path) {
		dir, err := currentDir(cwd)
		if err != nil {
			return "", err
		}
		path = filepath.Join(dir, path)
	}
	return pathutil.Canonical(path)
}

func expandHome(path string) string { return pathutil.ExpandHome(path) }

// pathContains reports whether dir is worktree itself or lies under it. It
// compares path components first, then filesystem identity so a
// case-differing spelling still resolves on a case-insensitive volume.
func pathContains(worktree, dir string) bool {
	if lexicalPathContains(worktree, dir) {
		return true
	}
	target, err := os.Stat(worktree)
	if err != nil {
		return false
	}
	for ancestor := dir; ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Stat(ancestor)
		if err == nil && os.SameFile(info, target) {
			return true
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return false
		}
	}
}

func lexicalPathContains(worktree, dir string) bool {
	rel, err := filepath.Rel(worktree, dir)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
