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
}

// New returns a resolver over already-canonical candidates.
func New(candidates []Candidate) *Resolver {
	return &Resolver{candidates: append([]Candidate(nil), candidates...)}
}

// FromInputs builds a resolver from discovered worktrees. Bare and prunable
// worktrees are skipped and every path is canonicalized, matching what list
// and the TUI display.
func FromInputs(inputs []core.Input) (*Resolver, error) {
	candidates := make([]Candidate, 0)
	for _, input := range inputs {
		for _, wt := range input.Worktrees {
			if wt.Bare || wt.Prunable {
				continue
			}
			path, err := pathutil.Canonical(wt.Path)
			if err != nil {
				return nil, fmt.Errorf("project %s: %w", input.Project.Name, err)
			}
			branch := wt.Branch
			if branch == "" {
				branch = wt.ShortHead()
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
	return New(candidates), nil
}

// Candidates returns a copy of the resolver's candidates.
func (r *Resolver) Candidates() []Candidate {
	return append([]Candidate(nil), r.candidates...)
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
				return Candidate{}, &NoMatchError{Selector: input, Detail: err.Error()}
			}
			matches := r.byPath(canonical)
			if len(matches) == 0 {
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
		return Candidate{}, &NoMatchError{
			Selector: input,
			Detail:   fmt.Sprintf("%s exists but is not a registered worktree", canonical),
		}
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
	dir, err := pathutil.Canonical(raw)
	if err != nil {
		return Candidate{}, fmt.Errorf("resolve current directory: %w", err)
	}
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
	if len(matches) == 0 {
		return Candidate{}, &NoActiveWorkstreamError{Dir: dir}
	}
	return r.unique(selector, matches)
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
		sorted := append([]Candidate(nil), matches...)
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].SessionName != sorted[j].SessionName {
				return sorted[i].SessionName < sorted[j].SessionName
			}
			return sorted[i].Path < sorted[j].Path
		})
		return Candidate{}, &AmbiguousError{Selector: input, Candidates: sorted}
	}
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

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[len("~/"):])
}

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
